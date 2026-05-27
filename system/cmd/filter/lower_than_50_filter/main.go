package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Amount < 50 Filter — keeps only transactions whose AmountPaid is less than 50 USD.
//
// Entrada (data + EOFs):
//   - Shared queue (INPUT_QUEUE_NAME) bound to INPUT_EXCHANGE with key ""
//
// Salida:
//   - Queue OUTPUT_QUEUE (q1_data, inline EOFs for sink_1)

func main() {
	svc := node.New("amt50_filter")
	conn := svc.Conn()

	inputMW := config.SharedQueue("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", []string{""}, conn)
	defer inputMW.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	svc.Run(inputMW, outputMW, func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type != protocol.BatchTypeTransactions {
			return protocol.Batch{}, false
		}
		out := make([]protocol.Transaction, 0, len(batch.Transactions))
		for _, t := range batch.Transactions {
			if t.AmountPaid < 50 {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		return protocol.Batch{Type: batch.Type, ClientID: batch.ClientID, Transactions: out}, true
	})
}
