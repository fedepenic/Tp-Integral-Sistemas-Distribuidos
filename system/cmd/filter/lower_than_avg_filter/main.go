package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Amount < avg/100 Filter (Q3 final filter)
//
// Keeps transactions whose AmountPaid is below 1% of the average for their
// payment format. The average arrives pre-computed in AvgForFormat, set by
// the upstream joiner.
//
// Entrada (data + EOFs):
//   - Queue INPUT_QUEUE (q3_candidates, inline EOFs from upstream)
//
// Salida:
//   - Queue OUTPUT_QUEUE (q3_data)

func main() {
	svc := node.New("amt_avg_filter")
	conn := svc.Conn()

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	svc.Run(inputMW, outputMW, func(batch protocol.Batch) (protocol.Batch, bool) {
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
	})
}
