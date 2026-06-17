package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/id"
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

		out := make([]filterInput, 0, len(batch.Transactions))
		for _, t := range batch.Transactions {
			if t.PaymentCurrency == targetCurrency {
				out = append(out, filterInput{
					PaymentCurrency: t.PaymentCurrency,
					Timestamp:       t.Timestamp,
					PaymentFormat:   t.PaymentFormat,
					FromBank:        t.FromBank,
					FromAccount:     t.FromAccount,
					ToBank:          t.ToBank,
					ToAccount:       t.ToAccount,
					AmountPaid:      t.AmountPaid,
				})
			}
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		sendToQ2(directMW, batch.ClientID, out, keyPrefix, partitions, batch.BatchID)
		fanout := make([]protocol.Transaction, 0, len(out))
		for _, in := range out {
			fanout = append(fanout, protocol.Transaction{
				Timestamp:     in.Timestamp,
				PaymentFormat: in.PaymentFormat,
				FromBank:      in.FromBank,
				FromAccount:   in.FromAccount,
				ToBank:        in.ToBank,
				ToAccount:     in.ToAccount,
				AmountPaid:    in.AmountPaid,
			})
		}
		return protocol.Batch{Type: batch.Type, ClientID: batch.ClientID, Transactions: fanout, BatchID: batch.BatchID}, true
	}
}

func sendToQ2(mw middleware.Middleware, clientID string, inputs []filterInput, keyPrefix string, partitions int, paternID string) {
	grouped := make(map[int][]protocol.Transaction)
	for _, in := range inputs {
		p := worker.PartitionForKey(in.FromBank, partitions)
		grouped[p] = append(grouped[p], protocol.Transaction{
			FromBank:    in.FromBank,
			FromAccount: in.FromAccount,
			AmountPaid:  in.AmountPaid,
		})
	}
	for p, group := range grouped {
		key := worker.RoutingKey(keyPrefix, p)
		b := protocol.Batch{
			Type:         protocol.BatchTypeTransactions,
			ClientID:     clientID,
			Transactions: group,
			BatchID:      id.Child(paternID, p),
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
