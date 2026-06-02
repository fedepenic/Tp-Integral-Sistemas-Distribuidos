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

func newProcess(outQ3MW, outFOMW, outFIMW middleware.Middleware, q3KeyPrefix string, q3Partitions int) node.ProcessFunc {
	start, _ := time.Parse(dateLayout, "2022-09-01")
	end, _ := time.Parse(dateLayout, "2022-09-05")
	end = end.Add(24 * time.Hour)

	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeEOF {
			sendQ3EOF(outQ3MW, batch, q3KeyPrefix, q3Partitions)
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
		sendQ3Partitioned(outQ3MW, batch.ClientID, out, q3KeyPrefix, q3Partitions)
		sendGroupedByKey(outFOMW, batch.ClientID, out, func(t protocol.Transaction) string { return t.FromAccount })
		sendGroupedByKey(outFIMW, batch.ClientID, out, func(t protocol.Transaction) string { return t.ToAccount })
		return protocol.Batch{}, false
	}
}

// sendQ3Partitioned routes transactions to avg_per_payment_format instances by
// hashing each transaction's PaymentFormat to a partition key.
func sendQ3Partitioned(mw middleware.Middleware, clientID string, txns []protocol.Transaction, keyPrefix string, partitions int) {
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
			log.Printf("[period1_filter] marshal Q3 batch partition=%d: %v", p, err)
			continue
		}
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[period1_filter] send Q3 batch partition=%d: %v", p, err)
		}
	}
}

// sendQ3EOF fans the EOF out to every avg_per_payment_format partition.
func sendQ3EOF(mw middleware.Middleware, batch protocol.Batch, keyPrefix string, partitions int) {
	data, err := json.Marshal(batch)
	if err != nil {
		log.Printf("[period1_filter] marshal Q3 EOF: %v", err)
		return
	}
	for p := 0; p < partitions; p++ {
		key := worker.RoutingKey(keyPrefix, p)
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[period1_filter] send Q3 EOF partition=%d: %v", p, err)
		}
	}
}

func sendGroupedByKey(mw middleware.Middleware, clientID string, txns []protocol.Transaction, keyFn func(protocol.Transaction) string) {
	groups := make(map[string][]protocol.Transaction)
	for _, t := range txns {
		k := keyFn(t)
		groups[k] = append(groups[k], t)
	}
	for k, group := range groups {
		b := protocol.Batch{Type: protocol.BatchTypeTransactions, ClientID: clientID, Transactions: group}
		data, err := json.Marshal(b)
		if err != nil {
			log.Printf("[period1_filter] marshal batch key=%s: %v", k, err)
			continue
		}
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, k); err != nil {
			log.Printf("[period1_filter] send batch key=%s: %v", k, err)
		}
	}
}
