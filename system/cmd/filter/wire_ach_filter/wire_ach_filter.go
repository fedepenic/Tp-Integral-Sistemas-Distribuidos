package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func newProcess() node.ProcessFunc {
	var seen, passed int
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type != protocol.BatchTypeTransactions {
			return protocol.Batch{}, false
		}
		out := make([]protocol.Transaction, 0, len(batch.Transactions))
		for _, t := range batch.Transactions {
			seen++
			in := filterInput{
				PaymentFormat: t.PaymentFormat,
				FromBank:      t.FromBank,
				FromAccount:   t.FromAccount,
				ToBank:        t.ToBank,
				ToAccount:     t.ToAccount,
				AmountPaid:    t.AmountPaid,
			}
			ok := in.PaymentFormat == formatWire || in.PaymentFormat == formatACH
			if seen <= 5 {
				log.Printf("[wireach_filter] txn=%d format=%q pass=%v", seen, in.PaymentFormat, ok)
			}
			if ok {
				passed++
				if passed%1000 == 0 {
					log.Printf("[wireach_filter] passed=%d seen=%d", passed, seen)
				}
				out = append(out, protocol.Transaction{
					FromBank:    in.FromBank,
					FromAccount: in.FromAccount,
					ToBank:      in.ToBank,
					ToAccount:   in.ToAccount,
					AmountPaid:  in.AmountPaid,
				})
			}
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		return protocol.Batch{Type: batch.Type, ClientID: batch.ClientID, Transactions: out}, true
	}
}
