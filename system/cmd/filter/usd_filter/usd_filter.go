package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

func newProcess(directMW middleware.Middleware, keyPrefix string, partitions int) node.ProcessFunc {
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeEOF {
			sendQ2EOF(directMW, batch, keyPrefix, partitions)
			return protocol.Batch{}, false
		}

		out := make([]protocol.Transaction, 0, len(batch.Transactions))
		for _, t := range batch.Transactions {
			if t.PaymentCurrency == "US Dollar" {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		sendToQ2(directMW, batch.ClientID, out, keyPrefix, partitions)
		return protocol.Batch{Type: batch.Type, ClientID: batch.ClientID, Transactions: out}, true
	}
}

// sendToQ2 groups transactions by hash(FromBank) and routes each group to the
// correct max_per_bank instance via a per-partition routing key.
func sendToQ2(mw middleware.Middleware, clientID string, txns []protocol.Transaction, keyPrefix string, partitions int) {
	grouped := make(map[int][]protocol.Transaction)
	for _, t := range txns {
		p := worker.PartitionForKey(t.FromBank, partitions)
		grouped[p] = append(grouped[p], t)
	}
	for p, group := range grouped {
		key := worker.RoutingKey(keyPrefix, p)
		b := protocol.Batch{
			Type:         protocol.BatchTypeTransactions,
			ClientID:     clientID,
			Transactions: group,
		}
		data, err := json.Marshal(b)
		if err != nil {
			log.Printf("[usd_filter] marshal Q2 batch partition=%d: %v", p, err)
			continue
		}
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[usd_filter] send to Q2 partition=%d: %v", p, err)
		}
	}
}

// sendQ2EOF fans the EOF out to every max_per_bank partition so each instance
// knows the upstream is done.
func sendQ2EOF(mw middleware.Middleware, batch protocol.Batch, keyPrefix string, partitions int) {
	data, err := json.Marshal(batch)
	if err != nil {
		log.Printf("[usd_filter] marshal Q2 EOF: %v", err)
		return
	}
	for p := 0; p < partitions; p++ {
		key := worker.RoutingKey(keyPrefix, p)
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[usd_filter] send Q2 EOF partition=%d: %v", p, err)
		}
	}
}
