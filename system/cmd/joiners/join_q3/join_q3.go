package main

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

const avgPerFormatDataType = "avg_per_format"

func newProcess() node.ProcessFunc {
	var mu sync.Mutex
	states := make(map[string]joinQ3State)

	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeEOF {
			mu.Lock()
			state, ok := states[batch.ClientID]
			if ok {
				pending := 0
				for _, txns := range state.pendingTxns {
					pending += len(txns)
				}
				if pending > 0 {
					log.Printf("[join_q3] %d transactions discarded at EOF (no average for payment_format)", pending)
				}
				delete(states, batch.ClientID)
			}
			mu.Unlock()
			return protocol.Batch{}, false
		}

		mu.Lock()
		defer mu.Unlock()

		state, ok := states[batch.ClientID]
		if !ok {
			state = joinQ3State{
				thresholdsByFormat: make(map[string]float64),
				pendingTxns:        make(map[string][]protocol.Transaction),
			}
		}

		var results []protocol.Transaction

		if batch.DataType == avgPerFormatDataType && len(batch.Records) > 0 {
			var avgs []avgPerFormatResult
			if err := json.Unmarshal(batch.Records, &avgs); err != nil {
				log.Printf("[join_q3] malformed avg_per_format records: %v", err)
				states[batch.ClientID] = state
				return protocol.Batch{}, false
			}
			for _, avg := range avgs {
				threshold := avg.AvgAmount / 100.0
				state.thresholdsByFormat[avg.PaymentFormat] = threshold
				for _, tx := range state.pendingTxns[avg.PaymentFormat] {
					if tx.AmountPaid < threshold {
						tx.AvgForFormat = avg.AvgAmount
						results = append(results, tx)
					}
				}
				delete(state.pendingTxns, avg.PaymentFormat)
			}
		}

		if batch.Type == protocol.BatchTypeTransactions {
			for _, tx := range batch.Transactions {
				threshold, hasThreshold := state.thresholdsByFormat[tx.PaymentFormat]
				if !hasThreshold {
					state.pendingTxns[tx.PaymentFormat] = append(state.pendingTxns[tx.PaymentFormat], tx)
				} else if tx.AmountPaid < threshold {
					tx.AvgForFormat = threshold * 100.0
					results = append(results, tx)
				}
			}
		}

		states[batch.ClientID] = state

		if len(results) == 0 {
			return protocol.Batch{}, false
		}

		return protocol.Batch{
			Type:         protocol.BatchTypeTransactions,
			ClientID:     batch.ClientID,
			Transactions: results,
		}, true
	}
}
