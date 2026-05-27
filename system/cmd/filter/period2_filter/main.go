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

// Period 2 Filter — keeps transactions in [2022-09-06, 2022-09-15].
//
// Transactions are sent to the output exchange grouped by PaymentFormat so
// that the downstream JoinerQ3 receives them partitioned by format.
//
// Entrada (data + EOFs):
//   - Queue INPUT_QUEUE (usd_for_q3p2, inline EOFs from upstream)
//
// Salida:
//   - Exchange OUTPUT_EXCHANGE (usd_period2, key = PaymentFormat)

const dateLayout = "2006-01-02"

func main() {
	svc := node.New("period2_filter")
	conn := svc.Conn()

	start, _ := time.Parse(dateLayout, "2022-09-06")
	end, _ := time.Parse(dateLayout, "2022-09-15")
	end = end.Add(24 * time.Hour)

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	// Use key "" so that Send works for inline EOF propagation.
	outputMW := config.Exchange("OUTPUT_EXCHANGE", []string{""}, conn)
	defer outputMW.Close()

	svc.Run(inputMW, outputMW, func(batch protocol.Batch) (protocol.Batch, bool) {
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
		return protocol.Batch{}, false // already sent grouped; node sends EOF via outputMW
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
			log.Printf("[period2_filter] marshal batch key=%s: %v", k, err)
			continue
		}
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, k); err != nil {
			log.Printf("[period2_filter] send batch key=%s: %v", k, err)
		}
	}
}
