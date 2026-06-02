package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func newProcess(directMW middleware.Middleware, directKey string) node.ProcessFunc {
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeEOF {
			sendQ2EOF(directMW, batch, directKey)
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
		sendToQ2(directMW, batch.ClientID, out, directKey)
		return protocol.Batch{Type: batch.Type, ClientID: batch.ClientID, Transactions: out}, true
	}
}

func sendToQ2(mw middleware.Middleware, clientID string, txns []protocol.Transaction, key string) {
	b := protocol.Batch{
		Type:         protocol.BatchTypeTransactions,
		ClientID:     clientID,
		Transactions: txns,
	}
	data, err := json.Marshal(b)
	if err != nil {
		log.Printf("[usd_filter] marshal Q2 batch: %v", err)
		return
	}
	if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
		log.Printf("[usd_filter] send to Q2 exchange: %v", err)
	}
}

func sendQ2EOF(mw middleware.Middleware, batch protocol.Batch, key string) {
	data, err := json.Marshal(batch)
	if err != nil {
		log.Printf("[usd_filter] marshal Q2 EOF: %v", err)
		return
	}
	if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
		log.Printf("[usd_filter] send Q2 EOF: %v", err)
	}
}
