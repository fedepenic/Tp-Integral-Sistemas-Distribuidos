package main

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/dedup"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

type joinQ2 struct {
	mu       sync.Mutex
	states   map[string]joinQ2State
	deduper  *dedup.BatchDeduplicator
	sm       *node.StateManager
	outputMW middleware.Middleware
}

func newJoinQ2(outputMW middleware.Middleware) *joinQ2 {
	stateDir := config.EnvOrDefault("STATE_DIR", "")
	freq := node.CheckpointFreqFromEnv(10000)
	return &joinQ2{
		states:   make(map[string]joinQ2State),
		deduper:  dedup.New(),
		sm:       node.NewStateManager("join_q2", "join_q2", stateDir, freq),
		outputMW: outputMW,
	}
}

func (j *joinQ2) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
		if j.deduper.Seen(batch.BatchID) {
			// Already applied (delta in WAL). Its output is re-emitted from the
			// WAL during recovery, so suppressing this re-delivery is safe. Log
			// it to make the crash loss-window visible.
			log.Printf("[join_q2] dedup re-delivery suppressed: client=%s batch_id=%s data_type=%s records_bytes=%d",
				batch.ClientID, batch.BatchID, batch.DataType, len(batch.Records))
			return protocol.Batch{}, false
		}
	}

	if batch.Type == protocol.BatchTypeEOF {
		j.mu.Lock()
		if state, ok := j.states[batch.ClientID]; ok {
			unmatched := 0
			for _, pending := range state.PendingMaxByBank {
				unmatched += len(pending)
			}
			if unmatched > 0 {
				log.Printf("[join_q2] %d unmatched max_per_bank results at EOF for client=%s", unmatched, batch.ClientID)
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
		state = joinQ2State{
			AccountsByBank:   make(map[string]protocol.AccountRef),
			PendingMaxByBank: make(map[string][]maxPerBankResult),
		}
	}

	var results []maxPerBankResult
	isAccounts := batch.DataType == "accounts" || batch.Type == protocol.BatchTypeAccounts

	// Build delta from batch data (before computing results, to capture input)
	delta := joinQ2Delta{ClientID: batch.ClientID}
	if isAccounts {
		// Read accounts from Records (new AccountRef format) or fall back to batch.Accounts
		if len(batch.Records) > 0 {
			var refs []protocol.AccountRef
			if err := json.Unmarshal(batch.Records, &refs); err != nil {
				log.Printf("[join_q2] malformed account refs: %v", err)
				j.states[batch.ClientID] = state
				return protocol.Batch{}, false
			}
			delta.Accounts = refs
		} else {
			for _, a := range batch.Accounts {
				delta.Accounts = append(delta.Accounts, protocol.AccountRef{BankName: a.BankName, BankID: a.BankID})
			}
		}
	}
	if batch.DataType == "max_per_bank" && len(batch.Records) > 0 {
		var rawResults []maxPerBankResult
		if err := json.Unmarshal(batch.Records, &rawResults); err != nil {
			log.Printf("[join_q2] malformed max_per_bank records: %v", err)
			j.states[batch.ClientID] = state
			return protocol.Batch{}, false
		}
		delta.MaxResults = rawResults
	}

	// Process accounts
	if isAccounts {
		for _, account := range delta.Accounts {
			state.AccountsByBank[account.BankID] = account
			if pending, exists := state.PendingMaxByBank[account.BankID]; exists {
				for _, res := range pending {
					results = append(results, maxPerBankResult{
						BankID:        res.BankID,
						BankName:      account.BankName,
						SourceAccount: res.SourceAccount,
						MaxAmountUSD:  res.MaxAmountUSD,
					})
				}
				delete(state.PendingMaxByBank, account.BankID)
			}
		}
	}

	// Process max_per_bank
	if batch.DataType == "max_per_bank" && len(batch.Records) > 0 {
		var rawResults []maxPerBankResult
		if err := json.Unmarshal(batch.Records, &rawResults); err != nil {
			log.Printf("[join_q2] malformed max_per_bank records: %v", err)
			j.states[batch.ClientID] = state
			return protocol.Batch{}, false
		}
		for _, res := range rawResults {
			if account, exists := state.AccountsByBank[res.BankID]; exists {
				results = append(results, maxPerBankResult{
					BankID:        res.BankID,
					BankName:      account.BankName,
					SourceAccount: res.SourceAccount,
					MaxAmountUSD:  res.MaxAmountUSD,
				})
			} else {
				state.PendingMaxByBank[res.BankID] = append(state.PendingMaxByBank[res.BankID], res)
			}
		}
	}

	delta.Resolved = results

	// Write WAL before finalizing state mutation
	if j.sm.Enabled() && batch.BatchID != "" {
		deltaData, err := json.Marshal(delta)
		if err != nil {
			log.Printf("[join_q2] marshal delta: %v", err)
			j.states[batch.ClientID] = state
			return protocol.Batch{}, false
		}
		if err := j.sm.AppendWAL(batch.BatchID, deltaData); err != nil {
			log.Printf("[join_q2] WAL append: %v", err)
			j.states[batch.ClientID] = state
			return protocol.Batch{}, false
		}
	}

	// Persist state to map
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

	records, err := json.Marshal(results)
	if err != nil {
		log.Printf("[join_q2] marshal results: %v", err)
		return protocol.Batch{}, false
	}
	return protocol.Batch{
		Type:     protocol.BatchTypeData,
		ClientID: batch.ClientID,
		DataType: "max_per_bank",
		BatchID:  batch.BatchID,
		Records:  json.RawMessage(records),
	}, true
}

func (j *joinQ2) checkpoint() {
	data, err := json.Marshal(j.states)
	if err != nil {
		log.Printf("[join_q2] marshal states: %v", err)
		return
	}
	if err := j.sm.SaveCheckpoint(data); err != nil {
		log.Printf("[join_q2] save checkpoint: %v", err)
	}
}

func (j *joinQ2) recover() {
	cp, entries, err := j.sm.Recover()
	if err != nil {
		log.Printf("[join_q2] recovery error: %v", err)
		return
	}
	if cp == nil && len(entries) == 0 {
		log.Printf("[join_q2] no checkpoint — starting fresh")
		return
	}

	if cp != nil {
		var states map[string]joinQ2State
		if err := json.Unmarshal(cp.State, &states); err != nil {
			log.Printf("[join_q2] unmarshal checkpoint: %v", err)
			return
		}
		for cid, st := range states {
			if st.AccountsByBank == nil {
				st.AccountsByBank = make(map[string]protocol.AccountRef)
			}
			if st.PendingMaxByBank == nil {
				st.PendingMaxByBank = make(map[string][]maxPerBankResult)
			}
			states[cid] = st
		}
		j.states = states
		log.Printf("[join_q2] recovered %d clients from checkpoint", len(states))
	} else {
		log.Printf("[join_q2] no checkpoint, replaying %d WAL entries from scratch", len(entries))
	}

	reemitted := 0
	for _, entry := range entries {
		var delta joinQ2Delta
		if err := json.Unmarshal(entry.Delta, &delta); err != nil {
			log.Printf("[join_q2] invalid WAL entry: %v", err)
			continue
		}
		j.applyDelta(delta)
		j.sm.MarkApplied(entry.BatchID)
		j.deduper.Mark(entry.BatchID)

		// Re-emit the results this batch produced. The WAL entry is persisted
		// (and the batch marked seen) BEFORE the output is sent and acked on the
		// live path, so a crash in that window loses the output and the
		// re-delivery is later suppressed by dedup. Re-emitting here closes that
		// gap; the output BatchID equals entry.BatchID, so anything already sent
		// is recognised as a duplicate by downstream (sink) dedup.
		if n := j.emitResolved(entry.BatchID, delta); n > 0 {
			reemitted += n
		}
	}
	log.Printf("[join_q2] recovery done: %d WAL entries replayed, %d resolved results re-emitted downstream", len(entries), reemitted)
}

// emitResolved re-sends the resolved max_per_bank results of a recovered WAL
// entry downstream, using entry.BatchID as the output BatchID exactly as the
// live path does, so downstream dedup recognises already-sent results as
// duplicates. Returns the number of results re-emitted.
func (j *joinQ2) emitResolved(batchID string, delta joinQ2Delta) int {
	if j.outputMW == nil || len(delta.Resolved) == 0 {
		return 0
	}
	records, err := json.Marshal(delta.Resolved)
	if err != nil {
		log.Printf("[join_q2] recovery re-emit marshal client=%s batch_id=%s: %v", delta.ClientID, batchID, err)
		return 0
	}
	out := protocol.Batch{
		Type:     protocol.BatchTypeData,
		ClientID: delta.ClientID,
		DataType: "max_per_bank",
		BatchID:  batchID,
		Records:  json.RawMessage(records),
	}
	data, err := json.Marshal(out)
	if err != nil {
		log.Printf("[join_q2] recovery re-emit marshal batch client=%s batch_id=%s: %v", delta.ClientID, batchID, err)
		return 0
	}
	if err := j.outputMW.Send(middleware.Message{Body: string(data)}); err != nil {
		log.Printf("[join_q2] recovery re-emit send client=%s batch_id=%s: %v", delta.ClientID, batchID, err)
		return 0
	}
	log.Printf("[join_q2] recovery re-emit: client=%s batch_id=%s results=%d", delta.ClientID, batchID, len(delta.Resolved))
	return len(delta.Resolved)
}

func (j *joinQ2) applyDelta(delta joinQ2Delta) {
	state, ok := j.states[delta.ClientID]
	if !ok {
		state = joinQ2State{
			AccountsByBank:   make(map[string]protocol.AccountRef),
			PendingMaxByBank: make(map[string][]maxPerBankResult),
		}
	}

	// Re-apply account additions
	for _, account := range delta.Accounts {
		state.AccountsByBank[account.BankID] = account
		// Try to resolve any pending that now match
		if pending, exists := state.PendingMaxByBank[account.BankID]; exists {
			for range pending {
				log.Printf("[join_q2] recovery: resolved pending max_per_bank for bank=%s", account.BankID)
			}
			delete(state.PendingMaxByBank, account.BankID)
		}
	}

	// Re-apply max_per_bank additions
	for _, res := range delta.MaxResults {
		if _, exists := state.AccountsByBank[res.BankID]; !exists {
			state.PendingMaxByBank[res.BankID] = append(state.PendingMaxByBank[res.BankID], res)
		}
	}

	j.states[delta.ClientID] = state

	// NOTE: resolved results are NOT re-sent here. applyDelta only rebuilds
	// in-memory join state. Re-emission of delta.Resolved happens in recover()
	// via emitResolved, which is the path that closes the crash loss-window.
}
