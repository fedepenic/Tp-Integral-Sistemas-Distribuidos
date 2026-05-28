package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func newProcess(directMW middleware.Middleware) node.ProcessFunc {
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		out := make([]protocol.Transaction, 0, len(batch.Transactions))
		for _, t := range batch.Transactions {
			if t.PaymentCurrency == "US Dollar" {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		sendByBank(directMW, batch.ClientID, out)
		return protocol.Batch{Type: batch.Type, ClientID: batch.ClientID, Transactions: out}, true
	}
}

// sendByBank routes filtered transactions to the Q2 direct exchange,
// grouped by source bank. No EOF is sent — the Q2 pipeline is not yet implemented.
func sendByBank(mw middleware.Middleware, clientID string, txns []protocol.Transaction) {
	groups := make(map[string][]protocol.Transaction)
	for _, t := range txns {
		groups[t.FromBank] = append(groups[t.FromBank], t)
	}
	for bank, group := range groups {
		b := protocol.Batch{
			Type:         protocol.BatchTypeTransactions,
			ClientID:     clientID,
			Transactions: group,
		}
		data, err := json.Marshal(b)
		if err != nil {
			log.Printf("[usd_filter] marshal Q2 batch bank=%s: %v", bank, err)
			continue
		}
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, bank); err != nil {
			log.Printf("[usd_filter] send to Q2 exchange bank=%s: %v", bank, err)
		}
	}
}
