package main

import (
	"encoding/json"
	"log"
	"sort"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/dedup"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/id"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

const chunkSize = 10_000

type scatterGather struct {
	state    map[string]map[string]*sgEntry
	outputMW middleware.Middleware
	dedup    *dedup.BatchDeduplicator
	sm       *node.StateManager
}

func newScatterGather(outputMW middleware.Middleware) *scatterGather {
	stateDir := config.EnvOrDefault("STATE_DIR", "")
	freq := node.CheckpointFreqFromEnv(1000)
	return &scatterGather{
		state:    make(map[string]map[string]*sgEntry),
		outputMW: outputMW,
		dedup:    dedup.New(),
		sm:       node.NewStateManager("scatter_gather", "scatter_gather", stateDir, freq),
	}
}

func (sg *scatterGather) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
		if sg.dedup.Seen(batch.BatchID) {
			return protocol.Batch{}, false
		}
	}
	if batch.Type == protocol.BatchTypeEOF {
		if sg.flush(batch.ClientID) {
			return batch, true
		}
		return protocol.Batch{}, false
	}
	if batch.Type != protocol.BatchTypeScatterGather {
		return protocol.Batch{}, false
	}
	log.Printf("[scatter_gather] batch client=%s items=%d", batch.ClientID, len(batch.ScatterGatherItems))

	// 1. Compute delta
	delta := sg.computeDelta(batch)

	// 2. Write delta to WAL
	if sg.sm.Enabled() && batch.BatchID != "" {
		deltaData, err := json.Marshal(delta)
		if err != nil {
			log.Printf("[scatter_gather] marshal delta: %v", err)
			return protocol.Batch{}, false
		}
		if err := sg.sm.AppendWAL(batch.BatchID, deltaData); err != nil {
			log.Printf("[scatter_gather] WAL append: %v", err)
			return protocol.Batch{}, false
		}
	}

	// 3. Apply delta
	sg.applyDelta(delta)

	// 4. Mark & checkpoint
	if batch.BatchID != "" {
		sg.dedup.Mark(batch.BatchID)
		sg.sm.MarkApplied(batch.BatchID)
	}

	if sg.sm.ShouldCheckpoint() {
		sg.checkpoint()
	}

	return protocol.Batch{}, false
}

func (sg *scatterGather) computeDelta(batch protocol.Batch) sgDelta {
	d := sgDelta{
		ClientID: batch.ClientID,
		Entries:  make(map[string]*sgEntry),
	}
	for _, item := range batch.ScatterGatherItems {
		sgKey := item.FromBank + "|" + item.FromAccount + "|" + item.ToBank + "|" + item.ToAccount
		if _, ok := d.Entries[sgKey]; !ok {
			d.Entries[sgKey] = &sgEntry{
				FromBank:    item.FromBank,
				FromAccount: item.FromAccount,
				ToBank:      item.ToBank,
				ToAccount:   item.ToAccount,
			}
		}
		d.Entries[sgKey].Count++
	}
	return d
}

func (sg *scatterGather) applyDelta(delta sgDelta) {
	byKey, ok := sg.state[delta.ClientID]
	if !ok {
		byKey = make(map[string]*sgEntry)
		sg.state[delta.ClientID] = byKey
	}
	for key, entry := range delta.Entries {
		existing, ok := byKey[key]
		if !ok {
			existing = &sgEntry{
				FromBank:    entry.FromBank,
				FromAccount: entry.FromAccount,
				ToBank:      entry.ToBank,
				ToAccount:   entry.ToAccount,
			}
			byKey[key] = existing
		}
		existing.Count += entry.Count
	}
}

func (sg *scatterGather) checkpoint() {
	data, err := json.Marshal(sg.state)
	if err != nil {
		log.Printf("[scatter_gather] marshal state: %v", err)
		return
	}
	if err := sg.sm.SaveCheckpoint(data); err != nil {
		log.Printf("[scatter_gather] save checkpoint: %v", err)
	}
}

func (sg *scatterGather) recover() {
	cp, entries, err := sg.sm.Recover()
	if err != nil {
		log.Printf("[scatter_gather] recovery error: %v", err)
		return
	}
	if cp == nil {
		log.Printf("[scatter_gather] no checkpoint — starting fresh")
		return
	}

	var state map[string]map[string]*sgEntry
	if err := json.Unmarshal(cp.State, &state); err != nil {
		log.Printf("[scatter_gather] unmarshal checkpoint: %v", err)
		return
	}
	sg.state = state
	log.Printf("[scatter_gather] recovered %d clients from checkpoint", len(state))

	for _, entry := range entries {
		var delta sgDelta
		if err := json.Unmarshal(entry.Delta, &delta); err != nil {
			log.Printf("[scatter_gather] invalid WAL entry: %v", err)
			continue
		}
		sg.applyDelta(delta)
		sg.sm.MarkApplied(entry.BatchID)
		sg.dedup.Mark(entry.BatchID)
	}
	log.Printf("[scatter_gather] recovery done: %d WAL entries replayed", len(entries))
}

func (sg *scatterGather) flush(clientID string) bool {
	byKey, ok := sg.state[clientID]
	if !ok {
		return true
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	chunkCount := 0

	passed, total := 0, len(byKey)
	chunk := make([]scatterGatherResult, 0, chunkSize)

	for _, key := range keys {
		entry := byKey[key]

		count := entry.Count
		if count <= scatterThreshold {
			continue
		}

		if entry.FromBank == entry.ToBank &&
			entry.FromAccount == entry.ToAccount {
			continue
		}

		passed++

		chunk = append(chunk, scatterGatherResult{
			FromBank:    entry.FromBank,
			FromAccount: entry.FromAccount,
			ToBank:      entry.ToBank,
			ToAccount:   entry.ToAccount,
			TargetCount: count,
		})

		if len(chunk) >= chunkSize {
			if err := sg.sendChunk(
				clientID,
				chunk,
				chunkCount,
			); err != nil {
				log.Printf("[scatter_gather] send chunk=%d: %v", chunkCount, err)
				return false
			}

			chunk = make([]scatterGatherResult, 0, chunkSize)
			chunkCount++
		}
	}

	if len(chunk) > 0 {
		if err := sg.sendChunk(
			clientID,
			chunk,
			chunkCount,
		); err != nil {
			log.Printf("[scatter_gather] send chunk=%d: %v", chunkCount, err)
			return false
		}
	}

	log.Printf(
		"[scatter_gather] flush client=%s total_pairs=%d passed_threshold=%d",
		clientID,
		total,
		passed,
	)

	delete(sg.state, clientID)
	sg.sendEOF(clientID)
	return true
}

func (sg *scatterGather) sendChunk(
	clientID string,
	results []scatterGatherResult,
	chunkCount int,
) error {
	instance := config.MustEnvInt("INSTANCE_ID")
	out := protocol.Batch{
		Type:     protocol.BatchTypeData,
		ClientID: clientID,
		DataType: "scatter_gather_result",
		Records:  mustMarshal(results),
		BatchID:  id.Aggregator("scatter_gather", clientID, 0, chunkCount, instance),
	}

	data, err := json.Marshal(out)
	if err != nil {
		return err
	}

	return sg.outputMW.Send(
		middleware.Message{
			Body: string(data),
		},
	)
}

func (sg *scatterGather) sendEOF(clientID string) {
	instance := config.MustEnvInt("INSTANCE_ID")

	eofBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: "scatter_gather_result",
		BatchID: id.AggregatorEOF(
			"scatter_gather", instance, clientID),
	}

	eofData, err := json.Marshal(eofBatch)
	if err != nil {
		log.Printf("[scatter_gather] marshal EOF: %v", err)
		return
	}

	if err := sg.outputMW.Send(
		middleware.Message{
			Body: string(eofData),
		},
	); err != nil {
		log.Printf("[scatter_gather] send EOF: %v", err)
	}
}

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func newProcess(sg *scatterGather) node.ProcessFunc {
	return sg.process
}
