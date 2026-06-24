package node

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/health"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// ProcessFunc is the business logic of any pipeline node.
// It receives a batch and returns the output batch and whether to send it.
// Return ok=false to discard the batch without sending anything downstream.
type ProcessFunc func(batch protocol.Batch) (result protocol.Batch, ok bool)

// Node is the base for all pipeline nodes. It handles:
//   - RabbitMQ connection settings
//   - Sequential consume → process → produce loop
//   - Per-client EOF counting: only calls ProcessFunc with the EOF once
//     UPSTREAM_INSTANCES EOFs have been received for that client
//
// Unlike Scalable, Node has a single consumer goroutine and no peer
// coordination — it is meant for terminal or single-instance nodes
// (sink, counter) that do not need to broadcast EOFs to siblings.
//
// Required environment variables:
//
//	UPSTREAM_INSTANCES   — upstream EOF count per client (default 1)
//	RABBITMQ_HOST, RABBITMQ_PORT
//	NODE_EOF_STATE       — path to EOF state file (optional, default empty)
type Node struct {
	conn          middleware.ConnSettings
	upstreamCount int
	persister     *eofPersister
	name          string
}

// NewNode reads connection settings and UPSTREAM_INSTANCES from the environment.
// If NODE_EOF_STATE is set, EOF recovery state is persisted to that path.
func NewNode() *Node {
	n := newNode()
	return &n
}

func newNode() Node {
	health.StartIfEnabled()

	upstream := 1
	if v := os.Getenv("UPSTREAM_INSTANCES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			upstream = n
		}
	}

	var persister *eofPersister
	name := "node"
	if path := os.Getenv("NODE_EOF_STATE"); path != "" {
		persister = newEOFPersister(path)
		name = nodeNameFromEOFState(path)
	}

	return Node{
		conn:          config.ConnSettings(),
		upstreamCount: upstream,
		persister:     persister,
		name:          name,
	}
}

// Conn returns the RabbitMQ connection settings.
func (n *Node) Conn() middleware.ConnSettings { return n.conn }

// UpstreamCount returns the number of upstream EOFs expected per client.
func (n *Node) UpstreamCount() int { return n.upstreamCount }

func nodeNameFromEOFState(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext == "" {
		return base
	}
	return base[:len(base)-len(ext)]
}

// payloadCount returns the number of data records a batch carries across all
// payload kinds. A data batch discarded after EOF with payloadCount > 0 is a
// strong signal of data loss (genuinely unprocessed data arriving too late),
// whereas a zero-payload batch is a harmless redelivery.
func payloadCount(batch protocol.Batch) int {
	n := len(batch.Transactions) + len(batch.Accounts) + len(batch.ScatterGatherItems) + len(batch.SrcRefs)
	if batch.Records != nil {
		n += len(batch.Records)
	}
	return n
}

// logDataAfterEOF logs a data batch that arrived after its client's EOF was
// forwarded. If it carries payload the discard is flagged as a possible loss so
// it stands out in the logs; otherwise it is a benign redelivery.
func logDataAfterEOF(nodeName, reason string, batch protocol.Batch) {
	if payloadCount(batch) > 0 {
		log.Printf("[%s] POSSIBLE-LOSS data after EOF (%s) carries payload — discarding %s", nodeName, reason, batchLogSummary(batch))
		return
	}
	log.Printf("[%s] data after EOF (%s) — discarding %s", nodeName, reason, batchLogSummary(batch))
}

func batchLogSummary(batch protocol.Batch) string {
	recordsLen := 0
	if batch.Records != nil {
		recordsLen = len(batch.Records)
	}
	return "client=" + batch.ClientID +
		" type=" + string(batch.Type) +
		" query=" + batch.QueryID +
		" data_type=" + batch.DataType +
		" batch_id=" + batch.BatchID +
		" txns=" + strconv.Itoa(len(batch.Transactions)) +
		" accounts=" + strconv.Itoa(len(batch.Accounts)) +
		" sg_items=" + strconv.Itoa(len(batch.ScatterGatherItems)) +
		" src_refs=" + strconv.Itoa(len(batch.SrcRefs)) +
		" records_bytes=" + strconv.Itoa(recordsLen) +
		" count=" + strconv.FormatInt(batch.Count, 10)
}

// Run starts consuming from inputMW. For each data batch it calls fn and
// sends the result to outputMW. EOFs are counted per client; fn is called
// with the EOF only after upstreamCount EOFs have arrived for that client.
func (n *Node) Run(inputMW, outputMW middleware.Middleware, fn ProcessFunc) {
	if n.persister == nil {
		n.runVolatile(inputMW, outputMW, fn)
	} else {
		n.runPersistent(inputMW, outputMW, fn)
	}
}

func (n *Node) runVolatile(inputMW, outputMW middleware.Middleware, fn ProcessFunc) {
	eofCounts := make(map[string]int)
	seenEOFs := make(map[string]struct{})

	if err := inputMW.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
		var batch protocol.Batch
		if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
			log.Printf("[node] malformed message — discarding: %v", err)
			ack()
			return
		}

		if batch.Type == protocol.BatchTypeEOF {
			if batch.BatchID != "" {
				if _, ok := seenEOFs[batch.BatchID]; ok {
					log.Printf("[node] duplicate EOF received — ignoring: client=%s batch_id=%s", batch.ClientID, batch.BatchID)
					ack()
					return
				}
				seenEOFs[batch.BatchID] = struct{}{}
			}
			eofCounts[batch.ClientID]++
			if eofCounts[batch.ClientID] < n.upstreamCount {
				ack()
				return
			}
			delete(eofCounts, batch.ClientID)
		}

		result, ok := fn(batch)
		if !ok {
			ack()
			return
		}

		data, err := json.Marshal(result)
		if err != nil {
			log.Printf("[node] marshal result: %v", err)
			nack()
			return
		}
		if err := outputMW.Send(middleware.Message{Body: string(data)}); err != nil {
			log.Printf("[node] send to output: %v", err)
			nack()
			return
		}
		ack()
	}); err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
		log.Printf("[node] consumer error: %v", err)
	}
}

func (n *Node) runPersistent(inputMW, outputMW middleware.Middleware, fn ProcessFunc) {
	st := n.persister

	if err := inputMW.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
		var batch protocol.Batch
		if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
			log.Printf("[node] malformed message — discarding: %v", err)
			ack()
			return
		}

		if batch.Type == protocol.BatchTypeEOF {
			clientID := batch.ClientID
			batchID := batch.BatchID

			// Already forwarded for this client? Ignore all future EOFs.
			if _, forwarded := st.EOFForwarded[clientID]; forwarded {
				log.Printf("[%s] EOF already forwarded — ignoring %s", n.name, batchLogSummary(batch))
				ack()
				return
			}

			// Dedup by BatchID.
			// If already seen: this is a re-delivery from a crash. The counter
			// already includes this BatchID, so skip incrementing. If the
			// barrier is already met (counter >= threshold), proceed to forward.
			alreadySeen := false
			if batchID != "" {
				if _, seen := st.SeenEOFs[batchID]; seen {
					alreadySeen = true
				} else {
					st.SeenEOFs[batchID] = struct{}{}
				}
			}

			if !alreadySeen {
				st.EOFCounts[clientID]++
				st.persist()
			}

			count := st.EOFCounts[clientID]
			if count < n.upstreamCount {
				log.Printf("[%s] EOF barrier progress %d/%d %s", n.name, count, n.upstreamCount, batchLogSummary(batch))
				ack()
				return
			}

			// Barrier met — mark as forwarded so future duplicates are ignored.
			st.EOFForwarded[clientID] = struct{}{}
			delete(st.EOFCounts, clientID)
			st.persist()

			log.Printf("[%s] EOF barrier complete — forwarding %s", n.name, batchLogSummary(batch))
		}

		// If the EOF was already forwarded for this client (recovery case),
		// discard any data batches that arrive after the stream completed.
		if batch.Type != protocol.BatchTypeEOF {
			if _, forwarded := st.EOFForwarded[batch.ClientID]; forwarded {
				logDataAfterEOF(n.name, "already forwarded", batch)
				ack()
				return
			}
		}

		result, ok := fn(batch)
		if !ok {
			ack()
			return
		}

		data, err := json.Marshal(result)
		if err != nil {
			log.Printf("[node] marshal result: %v", err)
			nack()
			return
		}
		if err := outputMW.Send(middleware.Message{Body: string(data)}); err != nil {
			log.Printf("[node] send to output: %v", err)
			nack()
			return
		}
		ack()
	}); err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
		log.Printf("[node] consumer error: %v", err)
	}
}
