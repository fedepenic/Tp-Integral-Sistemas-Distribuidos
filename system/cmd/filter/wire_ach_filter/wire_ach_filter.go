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
			ok := t.PaymentFormat == "Wire" || t.PaymentFormat == "ACH"
			if seen <= 5 {
				log.Printf("[wireach_filter] txn=%d format=%q pass=%v", seen, t.PaymentFormat, ok)
			}
			if ok {
				passed++
				if passed%1000 == 0 {
					log.Printf("[wireach_filter] passed=%d seen=%d", passed, seen)
				}
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		return protocol.Batch{Type: batch.Type, ClientID: batch.ClientID, Transactions: out}, true
	}
}
