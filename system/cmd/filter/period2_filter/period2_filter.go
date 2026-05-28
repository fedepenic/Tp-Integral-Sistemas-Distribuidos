package main

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

const dateLayout = "2006-01-02"

func newProcess(outputMW middleware.Middleware) node.ProcessFunc {
	start, _ := time.Parse(dateLayout, "2022-09-06")
	end, _ := time.Parse(dateLayout, "2022-09-15")
	end = end.Add(24 * time.Hour)

	return func(batch protocol.Batch) (protocol.Batch, bool) {
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
		sendGroupedByKey(outputMW, batch.ClientID, out, func(t protocol.Transaction) string {
			return t.PaymentFormat
		})
		return protocol.Batch{}, false // sent grouped above; node handles EOF via outputMW
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
			log.Printf("[period2_filter] marshal batch key=%s: %v", k, err)
			continue
		}
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, k); err != nil {
			log.Printf("[period2_filter] send batch key=%s: %v", k, err)
		}
	}
}
