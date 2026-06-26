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

const chunkSize = 700

type fanOut struct {
	state            map[string]map[string]*fanOutEntry
	outputMW         middleware.Middleware
	outputKeyPrefix  string
	outputPartitions int
	deduper          *dedup.BatchDeduplicator
	sm               *node.StateManager
}

func newFanOut(outputMW middleware.Middleware) *fanOut {
	stateDir := config.EnvOrDefault("STATE_DIR", "")
	freq := node.CheckpointFreqFromEnv(10000)
	return &fanOut{
		state:            make(map[string]map[string]*fanOutEntry),
		outputMW:         outputMW,
		outputKeyPrefix:  config.EnvOrDefault("OUTPUT_KEY_PREFIX", "joinersg"),
		outputPartitions: config.MustEnvInt("OUTPUT_PARTITIONS"),
		deduper:          dedup.New(),
		sm:               node.NewStateManager("fan_out", "fan_out", stateDir, freq),
	}
}

func (f *fanOut) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
		if f.deduper.Seen(batch.BatchID) {
			log.Printf("[fan_out] discarded duplicate batch client=%s id=%s", batch.ClientID, batch.BatchID)
			return protocol.Batch{}, false
		}
	}

	if batch.Type == protocol.BatchTypeEOF {
		if f.flush(batch.ClientID) {
			return batch, true
		}
		return protocol.Batch{}, false
	}
	if batch.Type == protocol.BatchTypeSrcRef {
		return f.processSrcRef(batch)
	}
	if batch.Type != protocol.BatchTypeTransactions {
		return protocol.Batch{}, false
	}
	log.Printf("[fan_out] batch client=%s txns=%d", batch.ClientID, len(batch.Transactions))

	// 1. Compute delta (new refs for this batch)
	delta := f.computeDelta(batch)

	// 2. Write delta to WAL
	if f.sm.Enabled() && batch.BatchID != "" {
		deltaData, err := json.Marshal(delta)
		if err != nil {
			log.Printf("[fan_out] marshal delta: %v", err)
			return protocol.Batch{}, false
		}
		if err := f.sm.AppendWAL(batch.BatchID, deltaData); err != nil {
			log.Printf("[fan_out] WAL append: %v", err)
			return protocol.Batch{}, false
		}
	}

	// 3. Apply delta to global state
	f.applyDelta(delta)

	// 4. Mark & checkpoint
	if batch.BatchID != "" {
		f.deduper.Mark(batch.BatchID)
	}

	if f.sm.ShouldCheckpoint() {
		f.checkpoint()
	}

	return protocol.Batch{}, false
}

func (f *fanOut) processSrcRef(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" {
		if f.deduper.Seen(batch.BatchID) {
			log.Printf("[fan_out] discarded duplicate src_ref batch client=%s id=%s", batch.ClientID, batch.BatchID)
			return protocol.Batch{}, false
		}
	}

	delta := f.computeDeltaFromSrcRefs(batch)

	if f.sm.Enabled() && batch.BatchID != "" {
		deltaData, err := json.Marshal(delta)
		if err != nil {
			log.Printf("[fan_out] marshal delta: %v", err)
			return protocol.Batch{}, false
		}
		if err := f.sm.AppendWAL(batch.BatchID, deltaData); err != nil {
			log.Printf("[fan_out] WAL append: %v", err)
			return protocol.Batch{}, false
		}
	}

	f.applyDelta(delta)

	if batch.BatchID != "" {
		f.deduper.Mark(batch.BatchID)
	}

	if f.sm.ShouldCheckpoint() {
		f.checkpoint()
	}

	return protocol.Batch{}, false
}

func (f *fanOut) computeDeltaFromSrcRefs(batch protocol.Batch) fanOutDelta {
	d := fanOutDelta{
		ClientID: batch.ClientID,
		Entries:  make(map[string]fanOutEntry),
	}
	for _, ref := range batch.SrcRefs {
		fromKey := ref.FromBank + "|" + ref.FromAccount
		entry, ok := d.Entries[fromKey]
		if !ok {
			entry = fanOutEntry{
				FromBank: ref.FromBank,
				FromAcct: ref.FromAccount,
			}
		}
		entry.Refs = append(entry.Refs, accountRef{Bank: ref.ToBank, Account: ref.ToAccount})
		d.Entries[fromKey] = entry
	}
	return d
}

func (f *fanOut) computeDelta(batch protocol.Batch) fanOutDelta {
	d := fanOutDelta{
		ClientID: batch.ClientID,
		Entries:  make(map[string]fanOutEntry),
	}
	for _, tx := range batch.Transactions {
		fromKey := tx.FromBank + "|" + tx.FromAccount
		entry, ok := d.Entries[fromKey]
		if !ok {
			entry = fanOutEntry{
				FromBank: tx.FromBank,
				FromAcct: tx.FromAccount,
			}
		}
		entry.Refs = append(entry.Refs, accountRef{Bank: tx.ToBank, Account: tx.ToAccount})
		d.Entries[fromKey] = entry
	}
	return d
}

func (f *fanOut) applyDelta(delta fanOutDelta) {
	byFrom, ok := f.state[delta.ClientID]
	if !ok {
		byFrom = make(map[string]*fanOutEntry)
		f.state[delta.ClientID] = byFrom
	}
	for key, entry := range delta.Entries {
		existing, ok := byFrom[key]
		if !ok {
			existing = &fanOutEntry{FromBank: entry.FromBank, FromAcct: entry.FromAcct}
			byFrom[key] = existing
		}
		existing.Refs = append(existing.Refs, entry.Refs...)
	}
}

func (f *fanOut) checkpoint() {
	data, err := json.Marshal(f.state)
	if err != nil {
		log.Printf("[fan_out] marshal state: %v", err)
		return
	}
	if err := f.sm.SaveCheckpoint(data); err != nil {
		log.Printf("[fan_out] save checkpoint: %v", err)
	}
}

func (f *fanOut) recover() {
	cp, entries, err := f.sm.Recover()
	if err != nil {
		log.Printf("[fan_out] recovery error: %v", err)
		return
	}
	if cp == nil && len(entries) == 0 {
		log.Printf("[fan_out] no checkpoint — starting fresh")
		return
	}

	if cp != nil {
		var state map[string]map[string]*fanOutEntry
		if err := json.Unmarshal(cp.State, &state); err != nil {
			log.Printf("[fan_out] unmarshal checkpoint: %v", err)
			return
		}
		f.state = state
		log.Printf("[fan_out] recovered %d clients from checkpoint", len(state))
	} else {
		log.Printf("[fan_out] no checkpoint, replaying %d WAL entries from scratch", len(entries))
	}

	for _, entry := range entries {
		var delta fanOutDelta
		if err := json.Unmarshal(entry.Delta, &delta); err != nil {
			log.Printf("[fan_out] invalid WAL entry: %v", err)
			continue
		}
		f.applyDelta(delta)
		
		f.deduper.Mark(entry.BatchID)
	}
	log.Printf("[fan_out] recovery done: %d WAL entries replayed", len(entries))
}

func (f *fanOut) flush(clientID string) bool {
	byFrom, ok := f.state[clientID]
	if !ok {
		return true
	}
	log.Printf("[fan_out] flush client=%s src_accounts=%d", clientID, len(byFrom))

	partitioned := make(map[int][]fanOutResult)
	keys := make([]string, 0, len(byFrom))
	for k := range byFrom {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	chunkCountByPartition := make(map[int]int)

	for _, key := range keys {
		entry := byFrom[key]
		for _, to := range entry.Refs {
			res := fanOutResult{
				FromBank:      entry.FromBank,
				FromAccount:   entry.FromAcct,
				MiddleBank:    to.Bank,
				MiddleAccount: to.Account,
			}

			partition := worker.PartitionForKey(res.MiddleAccount, f.outputPartitions)

			partitioned[partition] = append(partitioned[partition], res)

			if len(partitioned[partition]) >= chunkSize {
				if err := f.sendPartition(clientID, partitioned[partition], partition, chunkCountByPartition[partition]); err != nil {
					log.Printf("[fan_out] send partition=%d: %v", partition, err)
					return false
				}

				partitioned[partition] = nil
				chunkCountByPartition[partition]++
			}
		}
	}

	log.Printf("[fan_out] flush client=%s emitting results", clientID)
	for partition, results := range partitioned {
		if len(results) == 0 {
			continue
		}

		if err := f.sendPartition(clientID, results, partition, chunkCountByPartition[partition]); err != nil {
			log.Printf("[fan_out] send partition=%d: %v", partition, err)
			return false
		}
	}

	delete(f.state, clientID)
	f.sendEOF(clientID)
	return true
}

func (f *fanOut) sendPartition(clientID string, results []fanOutResult, partition int, chunkCount int) error {
	routingKey := worker.RoutingKey(
		f.outputKeyPrefix,
		partition,
	)

	raw, err := json.Marshal(results)
	if err != nil {
		return err
	}

	instance := config.MustEnvInt("INSTANCE_ID")
	out := protocol.Batch{
		Type:     protocol.BatchTypeData,
		ClientID: clientID,
		DataType: "fanout_result",
		Records:  raw,
		BatchID:  id.Aggregator("fanout", clientID, partition, chunkCount, instance),
	}

	data, err := json.Marshal(out)
	if err != nil {
		return err
	}

	return f.outputMW.SendWithKey(
		middleware.Message{
			Body: string(data),
		},
		routingKey,
	)
}

func (f *fanOut) sendEOF(clientID string) {
	instance := config.MustEnvInt("INSTANCE_ID")
	eofBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: "fanout_result",
		BatchID:  id.AggregatorEOF("fanout", instance, clientID),
	}
	eofData, err := json.Marshal(eofBatch)
	if err != nil {
		log.Printf("[fan_out] marshal EOF: %v", err)
		return
	}
	for i := 0; i < f.outputPartitions; i++ {
		routingKey := worker.RoutingKey(
			f.outputKeyPrefix,
			i,
		)

		if err := f.outputMW.SendWithKey(
			middleware.Message{
				Body: string(eofData),
			},
			routingKey,
		); err != nil {
			log.Printf("[fan_out] send EOF partition=%d: %v", i, err)
		}
	}
}
