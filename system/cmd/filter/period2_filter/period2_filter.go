package main

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

const dateLayout = "2006-01-02"

func newProcess(outputMW middleware.Middleware, keyPrefix string, partitions int) node.ProcessFunc {
	start, _ := time.Parse(dateLayout, "2022-09-06")
	end, _ := time.Parse(dateLayout, "2022-09-15")
	end = end.Add(24 * time.Hour)

	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeEOF {
			sendQ3EOF(outputMW, batch, keyPrefix, partitions)
			return batch, true
		}
		if batch.Type != protocol.BatchTypeTransactions {
			return protocol.Batch{}, false
		}
		out := make([]protocol.Transaction, 0, len(batch.Transactions))
		for _, t := range batch.Transactions {
			date := strings.ReplaceAll(t.Timestamp[:10], "/", "-")
			ts, err := time.Parse(dateLayout, date)
			if err != nil || ts.Before(start) || !ts.Before(end) {
				continue
			}
			out = append(out, t)
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		sendPartitioned(outputMW, batch.ClientID, out, keyPrefix, partitions)
		return protocol.Batch{}, false
	}
}

// sendPartitioned routes transactions to the correct join_q3 instance by
// hashing each transaction's PaymentFormat to a partition key.
func sendPartitioned(mw middleware.Middleware, clientID string, txns []protocol.Transaction, keyPrefix string, partitions int) {
	grouped := make(map[int][]protocol.Transaction)
	for _, t := range txns {
		p := worker.PartitionForKey(t.PaymentFormat, partitions)
		grouped[p] = append(grouped[p], t)
	}
	for p, group := range grouped {
		key := worker.RoutingKey(keyPrefix, p)
		b := protocol.Batch{Type: protocol.BatchTypeTransactions, ClientID: clientID, Transactions: group}
		data, err := json.Marshal(b)
		if err != nil {
			log.Printf("[period2_filter] marshal batch partition=%d: %v", p, err)
			continue
		}
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[period2_filter] send batch partition=%d: %v", p, err)
		}
	}
}

// sendQ3EOF fans the EOF out to every join_q3 partition.
func sendQ3EOF(mw middleware.Middleware, batch protocol.Batch, keyPrefix string, partitions int) {
	data, err := json.Marshal(batch)
	if err != nil {
		log.Printf("[period2_filter] marshal EOF: %v", err)
		return
	}
	for p := 0; p < partitions; p++ {
		key := worker.RoutingKey(keyPrefix, p)
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[period2_filter] send EOF partition=%d: %v", p, err)
		}
	}
}
