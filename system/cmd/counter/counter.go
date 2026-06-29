package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/dedup"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/id"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

type counterState struct {
	Counts map[string]int64 `json:"counts"`
}

type counterDelta struct {
	ClientID string `json:"client_id"`
	Count    int64  `json:"count"`
}

func newProcess(outputMW middleware.Middleware) node.ProcessFunc {
	txnCounts := make(map[string]int64)
	deduper := dedup.New()
	stateDir := config.EnvOrDefault("STATE_DIR", "")
	freq := node.CheckpointFreqFromEnv(10000)
	sm := node.NewStateManager("counter", "counter", stateDir, freq)

	cp, entries, err := sm.Recover()
	if err == nil && cp != nil {
		var st counterState
		if json.Unmarshal(cp.State, &st) == nil {
			txnCounts = st.Counts
			if txnCounts == nil {
				txnCounts = make(map[string]int64)
			}
			log.Printf("[counter] recovered %d clients from checkpoint", len(txnCounts))
		}
		for _, entry := range entries {
			var d counterDelta
			if json.Unmarshal(entry.Delta, &d) == nil {
				txnCounts[d.ClientID] += d.Count

				deduper.Mark(entry.BatchID)
			}
		}
		log.Printf("[counter] recovery done: %d WAL entries replayed", len(entries))
	}

	batchCount := 0

	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
			if deduper.Seen(batch.BatchID) {
				log.Printf("[counter] duplicate batch: %s", batch.BatchID)
				return protocol.Batch{}, false
			}
		}

		if batch.Type == protocol.BatchTypeTransactions {
			d := counterDelta{ClientID: batch.ClientID, Count: int64(len(batch.Transactions))}

			if sm.Enabled() && batch.BatchID != "" {
				deltaData, err := json.Marshal(d)
				if err != nil {
					log.Printf("[counter] marshal delta: %v", err)
					return protocol.Batch{}, false
				}
				if err := sm.AppendWAL(batch.BatchID, deltaData); err != nil {
					log.Printf("[counter] WAL append: %v", err)
					return protocol.Batch{}, false
				}
			}

			txnCounts[batch.ClientID] += d.Count

			if batch.BatchID != "" {
				deduper.Mark(batch.BatchID)

			}

			batchCount++
			if sm.Enabled() && batchCount%freq == 0 {
				stateData, _ := json.Marshal(counterState{Counts: txnCounts})
				if err := sm.SaveCheckpoint(stateData); err != nil {
					log.Printf("[counter] checkpoint: %v", err)
				}
			}

			return protocol.Batch{}, false
		}

		if batch.Type == protocol.BatchTypeEOF {
			total, exists := txnCounts[batch.ClientID]
			if exists {
				delete(txnCounts, batch.ClientID)
			}
			log.Printf("[counter] client=%s total=%d — emitting count batch", batch.ClientID, total)

			sendBatch(outputMW, protocol.Batch{
				BatchID:  id.Aggregator("counter", batch.ClientID, 0, 0, 1),
				Type:     protocol.BatchTypeCount,
				ClientID: batch.ClientID,
				Count:    total,
			})

			return protocol.Batch{
				Type:     protocol.BatchTypeEOF,
				ClientID: batch.ClientID,
				BatchID:  id.AggregatorEOF("counter", 1, batch.ClientID),
			}, true
		}

		return protocol.Batch{}, false
	}
}

func sendBatch(mw middleware.Middleware, batch protocol.Batch) {
	data, err := json.Marshal(batch)
	if err != nil {
		log.Printf("[counter] marshal batch: %v", err)
		return
	}
	if err := mw.Send(middleware.Message{Body: string(data)}); err != nil {
		log.Printf("[counter] send batch: %v", err)
	}
}
