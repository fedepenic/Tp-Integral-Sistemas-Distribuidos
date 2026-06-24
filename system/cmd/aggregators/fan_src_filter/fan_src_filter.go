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
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

const (
	minDistinctDests = 5
	chunkSize        = 1000
)

type fanSrcFilter struct {
	state        map[string]map[string]*srcEntry
	outFOMW      middleware.Middleware
	outFIMW      middleware.Middleware
	foKeyPrefix  string
	foPartitions int
	fiKeyPrefix  string
	fiPartitions int
	dedup        *dedup.BatchDeduplicator
	sm           *node.StateManager
}

func newFanSrcFilter(
	outFOMW, outFIMW middleware.Middleware,
	foKeyPrefix string, foPartitions int,
	fiKeyPrefix string, fiPartitions int,
) *fanSrcFilter {
	stateDir := config.EnvOrDefault("STATE_DIR", "")
	freq := node.CheckpointFreqFromEnv(10000)
	return &fanSrcFilter{
		state:        make(map[string]map[string]*srcEntry),
		outFOMW:      outFOMW,
		outFIMW:      outFIMW,
		foKeyPrefix:  foKeyPrefix,
		foPartitions: foPartitions,
		fiKeyPrefix:  fiKeyPrefix,
		fiPartitions: fiPartitions,
		dedup:        dedup.New(),
		sm:           node.NewStateManager("fan_src_filter", "fan_src_filter", stateDir, freq),
	}
}

func (f *fanSrcFilter) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
		if f.dedup.Seen(batch.BatchID) {
			log.Printf("[fan_src_filter] duplicate batch: %s", batch.BatchID)
			return protocol.Batch{}, false
		}
	}

	if batch.Type == protocol.BatchTypeEOF {
		if f.flush(batch.ClientID, batch.BatchID) {
			return batch, true
		}
		return protocol.Batch{}, false
	}
	if batch.Type != protocol.BatchTypeTransactions {
		return protocol.Batch{}, false
	}
	log.Printf("[fan_src_filter] batch client=%s txns=%d", batch.ClientID, len(batch.Transactions))

	// 1. Compute delta
	delta := f.computeDelta(batch)

	// 2. Write delta to WAL
	if f.sm.Enabled() && batch.BatchID != "" {
		deltaData, err := json.Marshal(delta)
		if err != nil {
			log.Printf("[fan_src_filter] marshal delta: %v", err)
			return protocol.Batch{}, false
		}
		if err := f.sm.AppendWAL(batch.BatchID, deltaData); err != nil {
			log.Printf("[fan_src_filter] WAL append: %v", err)
			return protocol.Batch{}, false
		}
	}

	// 3. Apply delta
	f.applyDelta(delta)

	// 4. Mark & checkpoint
	if batch.BatchID != "" {
		f.dedup.Mark(batch.BatchID)
		f.sm.MarkApplied(batch.BatchID)
	}

	if f.sm.ShouldCheckpoint() {
		f.checkpoint()
	}

	return protocol.Batch{}, false
}

func (f *fanSrcFilter) computeDelta(batch protocol.Batch) srcDelta {
	d := srcDelta{
		ClientID: batch.ClientID,
		Entries:  make(map[string]*srcEntry),
	}
	for _, tx := range batch.Transactions {
		fromKey := tx.FromBank + "|" + tx.FromAccount
		entry, ok := d.Entries[fromKey]
		if !ok {
			entry = &srcEntry{
				FromBank:   tx.FromBank,
				FromAcct:   tx.FromAccount,
				DistinctTo: make(map[string]bool),
			}
			d.Entries[fromKey] = entry
		}
		entry.DistinctTo[tx.ToBank+"|"+tx.ToAccount] = true
		entry.SrcRefs = append(entry.SrcRefs, protocol.SrcRef{
			FromBank:    tx.FromBank,
			FromAccount: tx.FromAccount,
			ToBank:      tx.ToBank,
			ToAccount:   tx.ToAccount,
		})
	}
	return d
}

func (f *fanSrcFilter) applyDelta(delta srcDelta) {
	byFrom, ok := f.state[delta.ClientID]
	if !ok {
		byFrom = make(map[string]*srcEntry)
		f.state[delta.ClientID] = byFrom
	}
	for key, entry := range delta.Entries {
		existing, ok := byFrom[key]
		if !ok {
			existing = &srcEntry{
				FromBank:   entry.FromBank,
				FromAcct:   entry.FromAcct,
				DistinctTo: make(map[string]bool),
			}
			byFrom[key] = existing
		}
		for k := range entry.DistinctTo {
			existing.DistinctTo[k] = true
		}
		existing.SrcRefs = append(existing.SrcRefs, entry.SrcRefs...)
	}
}

func (f *fanSrcFilter) checkpoint() {
	data, err := json.Marshal(f.state)
	if err != nil {
		log.Printf("[fan_src_filter] marshal state: %v", err)
		return
	}
	if err := f.sm.SaveCheckpoint(data); err != nil {
		log.Printf("[fan_src_filter] save checkpoint: %v", err)
	}
}

func (f *fanSrcFilter) recover() {
	cp, entries, err := f.sm.Recover()
	if err != nil {
		log.Printf("[fan_src_filter] recovery error: %v", err)
		return
	}
	if cp == nil {
		log.Printf("[fan_src_filter] no checkpoint — starting fresh")
		return
	}

	var state map[string]map[string]*srcEntry
	if err := json.Unmarshal(cp.State, &state); err != nil {
		log.Printf("[fan_src_filter] unmarshal checkpoint: %v", err)
		return
	}
	f.state = state
	log.Printf("[fan_src_filter] recovered %d clients from checkpoint", len(state))

	for _, entry := range entries {
		var delta srcDelta
		if err := json.Unmarshal(entry.Delta, &delta); err != nil {
			log.Printf("[fan_src_filter] invalid WAL entry: %v", err)
			continue
		}
		f.applyDelta(delta)
		f.sm.MarkApplied(entry.BatchID)
		f.dedup.Mark(entry.BatchID)
	}
	log.Printf("[fan_src_filter] recovery done: %d WAL entries replayed", len(entries))
}

func (f *fanSrcFilter) flush(clientID string, parentBatchID string) bool {
	byFrom, ok := f.state[clientID]
	if !ok {
		return true
	}

	foPart := make(map[int][]protocol.SrcRef)
	fiPart := make(map[int][]protocol.SrcRef)
	foChunkCount := make(map[int]int)
	fiChunkCount := make(map[int]int)

	fromKeys := make([]string, 0, len(byFrom))
	for k := range byFrom {
		fromKeys = append(fromKeys, k)
	}
	sort.Strings(fromKeys)

	for _, key := range fromKeys {
		entry := byFrom[key]
		if len(entry.DistinctTo) <= minDistinctDests {
			continue
		}
		for _, ref := range entry.SrcRefs {
			foP := worker.PartitionForKey(ref.FromBank+"|"+ref.FromAccount, f.foPartitions)
			foPart[foP] = append(foPart[foP], ref)
			if len(foPart[foP]) >= chunkSize {
				if !f.sendPartitionedChunk(f.outFOMW, clientID, foPart[foP], foP, f.foKeyPrefix, foChunkCount[foP]) {
					return false
				}
				foPart[foP] = nil
				foChunkCount[foP]++
			}

			fiP := worker.PartitionForKey(ref.ToBank+"|"+ref.ToAccount, f.fiPartitions)
			fiPart[fiP] = append(fiPart[fiP], ref)
			if len(fiPart[fiP]) >= chunkSize {
				if !f.sendPartitionedChunk(f.outFIMW, clientID, fiPart[fiP], fiP, f.fiKeyPrefix, fiChunkCount[fiP]) {
					return false
				}
				fiPart[fiP] = nil
				fiChunkCount[fiP]++
			}
		}
	}

	log.Printf("[fan_src_filter] flush client=%s total_sources=%d parent_batch=%s",
		clientID, len(byFrom), parentBatchID)

	for p, txns := range foPart {
		if len(txns) > 0 {
			if !f.sendPartitionedChunk(f.outFOMW, clientID, txns, p, f.foKeyPrefix, foChunkCount[p]) {
				return false
			}
		}
	}
	for p, txns := range fiPart {
		if len(txns) > 0 {
			if !f.sendPartitionedChunk(f.outFIMW, clientID, txns, p, f.fiKeyPrefix, fiChunkCount[p]) {
				return false
			}
		}
	}

	delete(f.state, clientID)

	sendEOF(f.outFOMW, clientID, f.foKeyPrefix, f.foPartitions)
	sendEOF(f.outFIMW, clientID, f.fiKeyPrefix, f.fiPartitions)
	return true
}

func (f *fanSrcFilter) sendPartitionedChunk(mw middleware.Middleware, clientID string, refs []protocol.SrcRef, partition int, keyPrefix string, chunkCount int) bool {
	key := worker.RoutingKey(keyPrefix, partition)
	instance := config.MustEnvInt("INSTANCE_ID")
	out := protocol.Batch{
		Type:    protocol.BatchTypeSrcRef,
		ClientID: clientID,
		SrcRefs: refs,
		BatchID: id.Aggregator("fan_src_filter", clientID, partition, chunkCount, instance),
	}
	data, err := json.Marshal(out)
	if err != nil {
		log.Printf("[fan_src_filter] marshal batch key=%s: %v", key, err)
		return false
	}
	if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
		log.Printf("[fan_src_filter] send batch key=%s: %v", key, err)
		return false
	}
	return true
}

func sendEOF(mw middleware.Middleware, clientID, keyPrefix string, partitions int) {
	instance := config.MustEnvInt("INSTANCE_ID")
	for i := 0; i < partitions; i++ {
		key := worker.RoutingKey(keyPrefix, i)
		eof := protocol.Batch{
			Type:     protocol.BatchTypeEOF,
			ClientID: clientID,
			BatchID:  id.AggregatorEOF("fan_src_filter", instance, clientID),
		}
		data, err := json.Marshal(eof)
		if err != nil {
			log.Printf("[fan_src_filter] marshal EOF key=%s: %v", key, err)
			continue
		}
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[fan_src_filter] send EOF key=%s: %v", key, err)
		}
	}
}
