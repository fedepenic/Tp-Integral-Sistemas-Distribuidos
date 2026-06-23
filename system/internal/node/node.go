package node

import (
	"encoding/json"
	"log"
	"os"
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
	if path := os.Getenv("NODE_EOF_STATE"); path != "" {
		persister = newEOFPersister(path)
	}

	return Node{
		conn:          config.ConnSettings(),
		upstreamCount: upstream,
		persister:     persister,
	}
}

// Conn returns the RabbitMQ connection settings.
func (n *Node) Conn() middleware.ConnSettings { return n.conn }

// UpstreamCount returns the number of upstream EOFs expected per client.
func (n *Node) UpstreamCount() int { return n.upstreamCount }

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
				log.Printf("[node] EOF already forwarded for client=%s — ignoring", clientID)
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
				log.Printf("[node] EOF client=%s (%d/%d) batch=%s", clientID, count, n.upstreamCount, batchID)
				ack()
				return
			}

			// Barrier met — mark as forwarded so future duplicates are ignored.
			st.EOFForwarded[clientID] = struct{}{}
			delete(st.EOFCounts, clientID)
			st.persist()

			log.Printf("[node] EOF barrier complete for client=%s — forwarding", clientID)
		}

		// If the EOF was already forwarded for this client (recovery case),
		// discard any data batches that arrive after the stream completed.
		if batch.Type != protocol.BatchTypeEOF {
			if _, forwarded := st.EOFForwarded[batch.ClientID]; forwarded {
				log.Printf("[node] data after EOF for client=%s — discarding", batch.ClientID)
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
