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

const chunkSize = 1000

type fanIn struct {
	state            map[string]map[string]*fanInEntry
	outputMW         middleware.Middleware
	outputKeyPrefix  string
	outputPartitions int
	deduper          *dedup.BatchDeduplicator
	sm               *node.StateManager
}

func newFanIn(outputMW middleware.Middleware) *fanIn {
	stateDir := config.EnvOrDefault("STATE_DIR", "")
	freq := node.CheckpointFreqFromEnv(10000)
	return &fanIn{
		state:            make(map[string]map[string]*fanInEntry),
		outputMW:         outputMW,
		outputKeyPrefix:  config.EnvOrDefault("OUTPUT_KEY_PREFIX", "joinersg"),
		outputPartitions: config.MustEnvInt("OUTPUT_PARTITIONS"),
		deduper:          dedup.New(),
		sm:               node.NewStateManager("fan_in", "fan_in", stateDir, freq),
	}
}

func (f *fanIn) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
		if f.deduper.Seen(batch.BatchID) {
			log.Printf("[fan_in] discarded duplicate batch client=%s id=%s", batch.ClientID, batch.BatchID)
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
	log.Printf("[fan_in] batch client=%s txns=%d", batch.ClientID, len(batch.Transactions))

	// 1. Compute delta
	delta := f.computeDelta(batch)

	// 2. Write delta to WAL
	if f.sm.Enabled() && batch.BatchID != "" {
		deltaData, err := json.Marshal(delta)
		if err != nil {
			log.Printf("[fan_in] marshal delta: %v", err)
			return protocol.Batch{}, false
		}
		if err := f.sm.AppendWAL(batch.BatchID, deltaData); err != nil {
			log.Printf("[fan_in] WAL append: %v", err)
			return protocol.Batch{}, false
		}
	}

	// 3. Apply delta
	f.applyDelta(delta)

	// 4. Mark & checkpoint
	if batch.BatchID != "" {
		f.deduper.Mark(batch.BatchID)
		f.sm.MarkApplied(batch.BatchID)
	}

	if f.sm.ShouldCheckpoint() {
		f.checkpoint()
	}

	return protocol.Batch{}, false
}

func (f *fanIn) processSrcRef(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" {
		if f.deduper.Seen(batch.BatchID) {
			log.Printf("[fan_in] discarded duplicate src_ref batch client=%s id=%s", batch.ClientID, batch.BatchID)
			return protocol.Batch{}, false
		}
	}

	delta := f.computeDeltaFromSrcRefs(batch)

	if f.sm.Enabled() && batch.BatchID != "" {
		deltaData, err := json.Marshal(delta)
		if err != nil {
			log.Printf("[fan_in] marshal delta: %v", err)
			return protocol.Batch{}, false
		}
		if err := f.sm.AppendWAL(batch.BatchID, deltaData); err != nil {
			log.Printf("[fan_in] WAL append: %v", err)
			return protocol.Batch{}, false
		}
	}

	f.applyDelta(delta)

	if batch.BatchID != "" {
		f.deduper.Mark(batch.BatchID)
		f.sm.MarkApplied(batch.BatchID)
	}

	if f.sm.ShouldCheckpoint() {
		f.checkpoint()
	}

	return protocol.Batch{}, false
}

func (f *fanIn) computeDeltaFromSrcRefs(batch protocol.Batch) fanInDelta {
	d := fanInDelta{
		ClientID: batch.ClientID,
		Entries:  make(map[string]fanInEntry),
	}
	for _, ref := range batch.SrcRefs {
		toKey := ref.ToBank + "|" + ref.ToAccount
		entry, ok := d.Entries[toKey]
		if !ok {
			entry = fanInEntry{
				ToBank: ref.ToBank,
				ToAcct: ref.ToAccount,
			}
		}
		entry.Refs = append(entry.Refs, accountRef{Bank: ref.FromBank, Account: ref.FromAccount})
		d.Entries[toKey] = entry
	}
	return d
}

func (f *fanIn) computeDelta(batch protocol.Batch) fanInDelta {
	d := fanInDelta{
		ClientID: batch.ClientID,
		Entries:  make(map[string]fanInEntry),
	}
	for _, tx := range batch.Transactions {
		toKey := tx.ToBank + "|" + tx.ToAccount
		entry, ok := d.Entries[toKey]
		if !ok {
			entry = fanInEntry{
				ToBank: tx.ToBank,
				ToAcct: tx.ToAccount,
			}
		}
		entry.Refs = append(entry.Refs, accountRef{Bank: tx.FromBank, Account: tx.FromAccount})
		d.Entries[toKey] = entry
	}
	return d
}

func (f *fanIn) applyDelta(delta fanInDelta) {
	byTo, ok := f.state[delta.ClientID]
	if !ok {
		byTo = make(map[string]*fanInEntry)
		f.state[delta.ClientID] = byTo
	}
	for key, entry := range delta.Entries {
		existing, ok := byTo[key]
		if !ok {
			existing = &fanInEntry{ToBank: entry.ToBank, ToAcct: entry.ToAcct}
			byTo[key] = existing
		}
		existing.Refs = append(existing.Refs, entry.Refs...)
	}
}

func (f *fanIn) checkpoint() {
	data, err := json.Marshal(f.state)
	if err != nil {
		log.Printf("[fan_in] marshal state: %v", err)
		return
	}
	if err := f.sm.SaveCheckpoint(data); err != nil {
		log.Printf("[fan_in] save checkpoint: %v", err)
	}
}

func (f *fanIn) recover() {
	cp, entries, err := f.sm.Recover()
	if err != nil {
		log.Printf("[fan_in] recovery error: %v", err)
		return
	}
	if cp == nil && len(entries) == 0 {
		log.Printf("[fan_in] no checkpoint — starting fresh")
		return
	}

	if cp != nil {
		var state map[string]map[string]*fanInEntry
		if err := json.Unmarshal(cp.State, &state); err != nil {
			log.Printf("[fan_in] unmarshal checkpoint: %v", err)
			return
		}
		f.state = state
		log.Printf("[fan_in] recovered %d clients from checkpoint", len(state))
	} else {
		log.Printf("[fan_in] no checkpoint, replaying %d WAL entries from scratch", len(entries))
	}

	for _, entry := range entries {
		var delta fanInDelta
		if err := json.Unmarshal(entry.Delta, &delta); err != nil {
			log.Printf("[fan_in] invalid WAL entry: %v", err)
			continue
		}
		f.applyDelta(delta)
		f.sm.MarkApplied(entry.BatchID)
		f.deduper.Mark(entry.BatchID)
	}
	log.Printf("[fan_in] recovery done: %d WAL entries replayed", len(entries))
}

func (f *fanIn) flush(clientID string) bool {
	byTo, ok := f.state[clientID]
	if !ok {
		return true
	}
	log.Printf("[fan_in] flush client=%s dest_accounts=%d", clientID, len(byTo))

	partitioned := make(map[int][]fanInResult)
	total := 0

	keys := make([]string, 0, len(byTo))
	for k := range byTo {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	chunkCountByPartition := make(map[int]int)
	for _, key := range keys {
		entry := byTo[key]
		for _, from := range entry.Refs {
			total++
			res := fanInResult{
				MiddleBank:    from.Bank,
				MiddleAccount: from.Account,
				ToBank:        entry.ToBank,
				ToAccount:     entry.ToAcct,
			}

			partition := worker.PartitionForKey(
				res.MiddleAccount,
				f.outputPartitions,
			)

			partitioned[partition] = append(
				partitioned[partition],
				res,
			)

			if len(partitioned[partition]) >= chunkSize {
				if err := f.sendPartition(clientID, partitioned[partition], partition, chunkCountByPartition[partition]); err != nil {
					log.Printf("[fan_in] send partition=%d: %v", partition, err)
					return false
				}

				partitioned[partition] = nil
				chunkCountByPartition[partition]++
			}
		}
	}

	log.Printf("[fan_in] flush client=%s emitting results=%d", clientID, total)
	for partition, results := range partitioned {
		if len(results) == 0 {
			continue
		}

		if err := f.sendPartition(clientID, results, partition, chunkCountByPartition[partition]); err != nil {
			log.Printf("[fan_in] send partition=%d: %v", partition, err)
			return false
		}
	}

	delete(f.state, clientID)
	f.sendEOF(clientID)
	return true
}

func (f *fanIn) sendPartition(clientID string, results []fanInResult, partition int, chunkCount int) error {
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
		DataType: "fanin_result",
		Records:  raw,
		BatchID:  id.Aggregator("fanin_result", clientID, partition, chunkCount, instance),
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

func (f *fanIn) sendEOF(clientID string) {
	instance := config.MustEnvInt("INSTANCE_ID")
	eofBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: "fanin_result",
		BatchID:  id.AggregatorEOF("fanin", instance, clientID),
	}

	eofData, err := json.Marshal(eofBatch)
	if err != nil {
		log.Printf("[fan_in] marshal EOF: %v", err)
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
			log.Printf(
				"[fan_in] send EOF partition=%d: %v",
				i,
				err,
			)
		}
	}
}
