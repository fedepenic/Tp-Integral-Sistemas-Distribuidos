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

func newProcess(
	outQ3MW, outFOMW, outFIMW middleware.Middleware,
	q3KeyPrefix string, q3Partitions int,
	foKeyPrefix string, foPartitions int,
	fiKeyPrefix string, fiPartitions int,
) node.ProcessFunc {
	start, _ := time.Parse(dateLayout, periodStart)
	end, _ := time.Parse(dateLayout, periodEnd)
	end = end.Add(24 * time.Hour)

	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeEOF {
			sendPartitionedEOF(outQ3MW, batch, q3KeyPrefix, q3Partitions)
			sendPartitionedEOF(outFOMW, batch, foKeyPrefix, foPartitions)
			sendPartitionedEOF(outFIMW, batch, fiKeyPrefix, fiPartitions)
			return batch, true
		}
		if batch.Type != protocol.BatchTypeTransactions {
			return protocol.Batch{}, false
		}
		out := make([]filterInput, 0, len(batch.Transactions))
		for _, t := range batch.Transactions {
			date := strings.ReplaceAll(t.Timestamp[:10], "/", "-")
			ts, err := time.Parse(dateLayout, date)
			if err != nil || ts.Before(start) || !ts.Before(end) {
				continue
			}
			out = append(out, filterInput{
				Timestamp:     t.Timestamp,
				PaymentFormat: t.PaymentFormat,
				AmountPaid:    t.AmountPaid,
				FromBank:      t.FromBank,
				FromAccount:   t.FromAccount,
				ToBank:        t.ToBank,
				ToAccount:     t.ToAccount,
			})
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		sendQ3Partitioned(outQ3MW, batch.ClientID, out, q3KeyPrefix, q3Partitions)
		sendQ4Partitioned(outFOMW, batch.ClientID, out, foKeyPrefix, foPartitions,
			func(in filterInput) string { return in.FromBank + "|" + in.FromAccount })
		sendQ4Partitioned(outFIMW, batch.ClientID, out, fiKeyPrefix, fiPartitions,
			func(in filterInput) string { return in.ToBank + "|" + in.ToAccount })
		return protocol.Batch{}, false
	}
}

func sendQ3Partitioned(mw middleware.Middleware, clientID string, inputs []filterInput, keyPrefix string, partitions int) {
	grouped := make(map[int][]protocol.Transaction)
	for _, in := range inputs {
		p := worker.PartitionForKey(in.PaymentFormat, partitions)
		grouped[p] = append(grouped[p], protocol.Transaction{
			PaymentFormat: in.PaymentFormat,
			AmountPaid:    in.AmountPaid,
		})
	}
	for p, group := range grouped {
		sendTxnBatch(mw, clientID, group, worker.RoutingKey(keyPrefix, p))
	}
}

func sendQ4Partitioned(mw middleware.Middleware, clientID string, inputs []filterInput, keyPrefix string, partitions int, keyFn func(filterInput) string) {
	grouped := make(map[int][]protocol.Transaction)
	for _, in := range inputs {
		p := worker.PartitionForKey(keyFn(in), partitions)
		grouped[p] = append(grouped[p], protocol.Transaction{
			FromBank:    in.FromBank,
			FromAccount: in.FromAccount,
			ToBank:      in.ToBank,
			ToAccount:   in.ToAccount,
		})
	}
	for p, group := range grouped {
		sendTxnBatch(mw, clientID, group, worker.RoutingKey(keyPrefix, p))
	}
}

func sendTxnBatch(mw middleware.Middleware, clientID string, txns []protocol.Transaction, key string) {
	b := protocol.Batch{Type: protocol.BatchTypeTransactions, ClientID: clientID, Transactions: txns}
	data, err := json.Marshal(b)
	if err != nil {
		log.Printf("[period1_filter] marshal batch key=%s: %v", key, err)
		return
	}
	if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
		log.Printf("[period1_filter] send batch key=%s: %v", key, err)
	}
}

func sendPartitionedEOF(mw middleware.Middleware, batch protocol.Batch, keyPrefix string, partitions int) {
	data, err := json.Marshal(batch)
	if err != nil {
		log.Printf("[period1_filter] marshal EOF: %v", err)
		return
	}
	for p := 0; p < partitions; p++ {
		key := worker.RoutingKey(keyPrefix, p)
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[period1_filter] send EOF partition=%d: %v", p, err)
		}
	}
}
