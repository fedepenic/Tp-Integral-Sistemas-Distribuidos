package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// USD < 1 Filter — keeps only transactions whose AmountPaid (in USD) is less than 1.
//
// Entrada (data + EOFs):
//   - Queue INPUT_QUEUE (converted_usd, inline EOFs from currency_converter)
//     Transactions arrive with AmountPaid already converted to USD.
//
// Salida:
//   - Queue OUTPUT_QUEUE (q5_filtered, inline EOFs for counter_q5)

func main() {
	svc := node.New("usd_lower_than_one")
	conn := svc.Conn()

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	var seen, passed int

	svc.Run(inputMW, outputMW, func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type != protocol.BatchTypeTransactions {
			return protocol.Batch{}, false
		}
		out := make([]protocol.Transaction, 0, len(batch.Transactions))
		for _, t := range batch.Transactions {
			seen++
			ok := t.AmountPaid < 1.0
			if seen <= 5 {
				log.Printf("[lower_than_1_filter] txn=%d amount=%.6f pass=%v", seen, t.AmountPaid, ok)
			}
			if ok {
				passed++
				if passed%1000 == 0 {
					log.Printf("[lower_than_1_filter] passed=%d seen=%d", passed, seen)
				}
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		return protocol.Batch{Type: batch.Type, ClientID: batch.ClientID, Transactions: out}, true
	})
}
