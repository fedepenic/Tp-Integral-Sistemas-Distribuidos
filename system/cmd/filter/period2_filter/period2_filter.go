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

func newProcess(outputMW middleware.Middleware, keyPrefix string, partitions int) node.ProcessFunc {
	start, _ := time.Parse(dateLayout, periodStart)
	end, _ := time.Parse(dateLayout, periodEnd)
	end = end.Add(24 * time.Hour)

	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeEOF {
			sendEOF(outputMW, batch, keyPrefix, partitions)
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
			})
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		sendPartitioned(outputMW, batch.ClientID, out, keyPrefix, partitions)
		return protocol.Batch{}, false
	}
}

func sendPartitioned(mw middleware.Middleware, clientID string, inputs []filterInput, keyPrefix string, partitions int) {
	grouped := make(map[int][]protocol.Transaction)
	for _, in := range inputs {
		p := worker.PartitionForKey(in.PaymentFormat, partitions)
		grouped[p] = append(grouped[p], protocol.Transaction{
			PaymentFormat: in.PaymentFormat,
			AmountPaid:    in.AmountPaid,
			FromBank:      in.FromBank,
			FromAccount:   in.FromAccount,
		})
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

func sendEOF(mw middleware.Middleware, batch protocol.Batch, keyPrefix string, partitions int) {
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
