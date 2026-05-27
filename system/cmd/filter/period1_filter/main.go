package main

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Period 1 Filter (USD pipeline) — keeps transactions in [2022-09-01, 2022-09-05].
//
// Filtered transactions are routed to three downstream exchanges:
//   1. OUTPUT_Q3_EXCHANGE  (key = PaymentFormat) → AvgPerFormat / JoinerQ3
//   2. OUTPUT_Q4_FO_EXCHANGE (key = FromAccount) → FanOut detector
//   3. OUTPUT_Q4_FI_EXCHANGE (key = ToAccount)   → FanIn detector
//
// Entrada (data + EOFs):
//   - Queue INPUT_QUEUE (usd_for_p1, inline EOFs from upstream)

const dateLayout = "2006-01-02"

func main() {
	svc := node.New("period1_filter")
	conn := svc.Conn()

	start, _ := time.Parse(dateLayout, "2022-09-01")
	end, _ := time.Parse(dateLayout, "2022-09-05")
	end = end.Add(24 * time.Hour)

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	// Primary output (Q3). Key "" allows Send to work for inline EOF propagation.
	outQ3MW := config.Exchange("OUTPUT_Q3_EXCHANGE", []string{""}, conn)
	defer outQ3MW.Close()

	outFOMW := config.Exchange("OUTPUT_Q4_FO_EXCHANGE", []string{}, conn)
	defer outFOMW.Close()

	outFIMW := config.Exchange("OUTPUT_Q4_FI_EXCHANGE", []string{}, conn)
	defer outFIMW.Close()

	svc.Run(inputMW, outQ3MW, func(batch protocol.Batch) (protocol.Batch, bool) {
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
		sendGroupedByKey(outQ3MW, batch.ClientID, out, func(t protocol.Transaction) string { return t.PaymentFormat })
		sendGroupedByKey(outFOMW, batch.ClientID, out, func(t protocol.Transaction) string { return t.FromAccount })
		sendGroupedByKey(outFIMW, batch.ClientID, out, func(t protocol.Transaction) string { return t.ToAccount })
		return protocol.Batch{}, false // already sent grouped; node sends EOF via outQ3MW
	})
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
