package main

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/dedup"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

const avgPerFormatDataType = "avg_per_format"

type joinQ3 struct {
	mu      sync.Mutex
	states  map[string]joinQ3State
	deduper *dedup.BatchDeduplicator
	sm      *node.StateManager
}

func newJoinQ3() *joinQ3 {
	stateDir := config.EnvOrDefault("STATE_DIR", "")
	freq := node.CheckpointFreqFromEnv(10000)
	return &joinQ3{
		states:  make(map[string]joinQ3State),
		deduper: dedup.New(),
		sm:      node.NewStateManager("join_q3", "join_q3", stateDir, freq),
	}
}

func (j *joinQ3) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
		if j.deduper.Seen(batch.BatchID) {
			return protocol.Batch{}, false
		}
	}

	if batch.Type == protocol.BatchTypeEOF {
		j.mu.Lock()
		if state, ok := j.states[batch.ClientID]; ok {
			pending := 0
			for _, txns := range state.PendingTxns {
				pending += len(txns)
			}
			if pending > 0 {
				log.Printf("[join_q3] %d transactions discarded at EOF (no average for payment_format)", pending)
			}
			delete(j.states, batch.ClientID)
		}
		j.mu.Unlock()
		return protocol.Batch{}, false
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	state, ok := j.states[batch.ClientID]
	if !ok {
		state = joinQ3State{
			ThresholdsByFormat: make(map[string]float64),
			PendingTxns:        make(map[string][]pendingTxn),
		}
	}

	var results []protocol.Transaction
	isAvg := batch.DataType == avgPerFormatDataType && len(batch.Records) > 0

	if isAvg {
		var avgs []avgPerFormatResult
		if err := json.Unmarshal(batch.Records, &avgs); err != nil {
			log.Printf("[join_q3] malformed avg_per_format records: %v", err)
			j.states[batch.ClientID] = state
			return protocol.Batch{}, false
		}
		for _, avg := range avgs {
			threshold := avg.AvgAmount / 100.0
			state.ThresholdsByFormat[avg.PaymentFormat] = threshold
			for _, ptx := range state.PendingTxns[avg.PaymentFormat] {
				if ptx.AmountPaid < threshold {
					results = append(results, protocol.Transaction{
						PaymentFormat: avg.PaymentFormat,
						AmountPaid:    ptx.AmountPaid,
						FromBank:      ptx.FromBank,
						FromAccount:   ptx.FromAccount,
						AvgForFormat:  avg.AvgAmount,
					})
				}
			}
			delete(state.PendingTxns, avg.PaymentFormat)
		}
	}

	if batch.Type == protocol.BatchTypeTransactions {
		for _, tx := range batch.Transactions {
			threshold, hasThreshold := state.ThresholdsByFormat[tx.PaymentFormat]
			if !hasThreshold {
				state.PendingTxns[tx.PaymentFormat] = append(state.PendingTxns[tx.PaymentFormat], pendingTxn{
					PaymentFormat: tx.PaymentFormat,
					AmountPaid:    tx.AmountPaid,
					FromBank:      tx.FromBank,
					FromAccount:   tx.FromAccount,
				})
			} else if tx.AmountPaid < threshold {
				tx.AvgForFormat = threshold * 100.0
				results = append(results, tx)
			}
		}
	}

	// Build delta
	delta := joinQ3Delta{ClientID: batch.ClientID, Resolved: results}
	if isAvg {
		var avgs []avgPerFormatResult
		json.Unmarshal(batch.Records, &avgs)
		delta.Avgs = avgs
	}
	if batch.Type == protocol.BatchTypeTransactions {
		delta.Txns = make([]pendingTxn, len(batch.Transactions))
		for i, tx := range batch.Transactions {
			delta.Txns[i] = pendingTxn{
				PaymentFormat: tx.PaymentFormat,
				AmountPaid:    tx.AmountPaid,
				FromBank:      tx.FromBank,
				FromAccount:   tx.FromAccount,
			}
		}
	}

	// Write WAL
	if j.sm.Enabled() && batch.BatchID != "" {
		deltaData, err := json.Marshal(delta)
		if err != nil {
			log.Printf("[join_q3] marshal delta: %v", err)
			j.states[batch.ClientID] = state
			return protocol.Batch{}, false
		}
		if err := j.sm.AppendWAL(batch.BatchID, deltaData); err != nil {
			log.Printf("[join_q3] WAL append: %v", err)
			j.states[batch.ClientID] = state
			return protocol.Batch{}, false
		}
	}

	j.states[batch.ClientID] = state
	if batch.BatchID != "" {
		j.deduper.Mark(batch.BatchID)
		j.sm.MarkApplied(batch.BatchID)
	}

	if j.sm.ShouldCheckpoint() {
		j.checkpoint()
	}

	if len(results) == 0 {
		return protocol.Batch{}, false
	}

	return protocol.Batch{
		Type:         protocol.BatchTypeTransactions,
		ClientID:     batch.ClientID,
		BatchID:      batch.BatchID,
		Transactions: results,
	}, true
}

func (j *joinQ3) checkpoint() {
	data, err := json.Marshal(j.states)
	if err != nil {
		log.Printf("[join_q3] marshal states: %v", err)
		return
	}
	if err := j.sm.SaveCheckpoint(data); err != nil {
		log.Printf("[join_q3] save checkpoint: %v", err)
	}
}

func (j *joinQ3) recover() {
	cp, entries, err := j.sm.Recover()
	if err != nil {
		log.Printf("[join_q3] recovery error: %v", err)
		return
	}
	if cp == nil && len(entries) == 0 {
		log.Printf("[join_q3] no checkpoint — starting fresh")
		return
	}

	if cp != nil {
		var states map[string]joinQ3State
		if err := json.Unmarshal(cp.State, &states); err != nil {
			log.Printf("[join_q3] unmarshal checkpoint: %v", err)
			return
		}
		for cid, st := range states {
			if st.ThresholdsByFormat == nil {
				st.ThresholdsByFormat = make(map[string]float64)
			}
			if st.PendingTxns == nil {
				st.PendingTxns = make(map[string][]pendingTxn)
			}
			states[cid] = st
		}
		j.states = states
		log.Printf("[join_q3] recovered %d clients from checkpoint", len(states))
	} else {
		log.Printf("[join_q3] no checkpoint, replaying %d WAL entries from scratch", len(entries))
	}

	for _, entry := range entries {
		var delta joinQ3Delta
		if err := json.Unmarshal(entry.Delta, &delta); err != nil {
			log.Printf("[join_q3] invalid WAL entry: %v", err)
			continue
		}
		j.applyDelta(delta)
		j.sm.MarkApplied(entry.BatchID)
		j.deduper.Mark(entry.BatchID)
	}
	log.Printf("[join_q3] recovery done: %d WAL entries replayed", len(entries))
}

func (j *joinQ3) applyDelta(delta joinQ3Delta) {
	state, ok := j.states[delta.ClientID]
	if !ok {
		state = joinQ3State{
			ThresholdsByFormat: make(map[string]float64),
			PendingTxns:        make(map[string][]pendingTxn),
		}
	}

	for _, avg := range delta.Avgs {
		threshold := avg.AvgAmount / 100.0
		state.ThresholdsByFormat[avg.PaymentFormat] = threshold
		delete(state.PendingTxns, avg.PaymentFormat)
	}

	for _, tx := range delta.Txns {
		if _, hasThreshold := state.ThresholdsByFormat[tx.PaymentFormat]; !hasThreshold {
			state.PendingTxns[tx.PaymentFormat] = append(state.PendingTxns[tx.PaymentFormat], tx)
		}
	}

	j.states[delta.ClientID] = state
}
