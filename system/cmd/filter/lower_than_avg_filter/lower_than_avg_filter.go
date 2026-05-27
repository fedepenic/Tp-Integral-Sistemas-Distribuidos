package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func newProcess() node.ProcessFunc {
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type != protocol.BatchTypeTransactions {
			return protocol.Batch{}, false
		}
		out := make([]protocol.Transaction, 0, len(batch.Transactions))
		for _, t := range batch.Transactions {
			if t.AmountPaid < t.AvgForFormat/100.0 {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		return protocol.Batch{Type: batch.Type, ClientID: batch.ClientID, Transactions: out}, true
	}
}
