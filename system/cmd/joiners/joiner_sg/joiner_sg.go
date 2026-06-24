package main

import (
	"encoding/json"
	"log"
	"sort"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/dedup"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/id"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

const chunkSize = 10_000

type joinerSG struct {
	mu              sync.Mutex
	states          map[string]sgState
	deduper         *dedup.BatchDeduplicator
	sm              *node.StateManager
	outputMW        middleware.Middleware
	outputKeyPrefix string
	outputPartitions int
}

func newJoinerSG(outputMW middleware.Middleware, outputKeyPrefix string, outputPartitions int) *joinerSG {
	stateDir := config.EnvOrDefault("STATE_DIR", "")
	freq := node.CheckpointFreqFromEnv(10000)
	return &joinerSG{
		states:           make(map[string]sgState),
		deduper:          dedup.New(),
		sm:               node.NewStateManager("joiner_sg", "joiner_sg", stateDir, freq),
		outputMW:         outputMW,
		outputKeyPrefix:  outputKeyPrefix,
		outputPartitions: outputPartitions,
	}
}

func (j *joinerSG) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
		if j.deduper.Seen(batch.BatchID) {
			return protocol.Batch{}, false
		}
	}

	if batch.Type == protocol.BatchTypeEOF {
		j.mu.Lock()
		delete(j.states, batch.ClientID)
		j.mu.Unlock()

		for i := 0; i < j.outputPartitions; i++ {
			routingKey := worker.RoutingKey(j.outputKeyPrefix, i)
			eof := protocol.Batch{
				Type:     protocol.BatchTypeEOF,
				ClientID: batch.ClientID,
				BatchID: id.AggregatorEOF(
					"joiner_sg",
					config.MustEnvInt("INSTANCE_ID"),
					batch.ClientID,
				),
			}
			data, err := json.Marshal(eof)
			if err != nil {
				log.Printf("[joiner_sg] marshal EOF partition=%d: %v", i, err)
				continue
			}
			if err := j.outputMW.SendWithKey(middleware.Message{Body: string(data)}, routingKey); err != nil {
				log.Printf("[joiner_sg] send EOF partition=%d: %v", i, err)
			}
		}
		return batch, true
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	state, ok := j.states[batch.ClientID]
	if !ok {
		state = newSGState()
	}

	var items []protocol.ScatterGatherItem
	delta := sgDelta{ClientID: batch.ClientID}

	switch batch.DataType {
	case "fanout_result":
		var results []fanOutResult
		if err := json.Unmarshal(batch.Records, &results); err != nil {
			log.Printf("[joiner_sg] malformed fanout_result: %v", err)
			j.states[batch.ClientID] = state
			return protocol.Batch{}, false
		}
		log.Printf("[joiner_sg] fanout_result client=%s results=%d", batch.ClientID, len(results))
		delta.FanOut = results

		for _, fo := range results {
			key := middleKey(fo.MiddleBank, fo.MiddleAccount)
			state.FanOutByMid[key] = append(state.FanOutByMid[key], fo)
			for _, fi := range state.FanInByMid[key] {
				items = append(items, makeScatterGatherItem(fo, fi))
			}
		}

	case "fanin_result":
		var results []fanInResult
		if err := json.Unmarshal(batch.Records, &results); err != nil {
			log.Printf("[joiner_sg] malformed fanin_result: %v", err)
			j.states[batch.ClientID] = state
			return protocol.Batch{}, false
		}
		log.Printf("[joiner_sg] fanin_result client=%s results=%d", batch.ClientID, len(results))
		delta.FanIn = results

		for _, fi := range results {
			key := middleKey(fi.MiddleBank, fi.MiddleAccount)
			state.FanInByMid[key] = append(state.FanInByMid[key], fi)
			for _, fo := range state.FanOutByMid[key] {
				items = append(items, makeScatterGatherItem(fo, fi))
			}
		}
	}

	// Write WAL
	if j.sm.Enabled() && batch.BatchID != "" {
		deltaData, err := json.Marshal(delta)
		if err != nil {
			log.Printf("[joiner_sg] marshal delta: %v", err)
			j.states[batch.ClientID] = state
			return protocol.Batch{}, false
		}
		if err := j.sm.AppendWAL(batch.BatchID, deltaData); err != nil {
			log.Printf("[joiner_sg] WAL append: %v", err)
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

	log.Printf("[joiner_sg] produced items=%d client=%s", len(items), batch.ClientID)
	sendChunks(j.outputMW, batch.ClientID, items, j.outputKeyPrefix, j.outputPartitions, batch.BatchID)
	return protocol.Batch{}, false
}

func (j *joinerSG) checkpoint() {
	data, err := json.Marshal(j.states)
	if err != nil {
		log.Printf("[joiner_sg] marshal states: %v", err)
		return
	}
	if err := j.sm.SaveCheckpoint(data); err != nil {
		log.Printf("[joiner_sg] save checkpoint: %v", err)
	}
}

func (j *joinerSG) recover() {
	cp, entries, err := j.sm.Recover()
	if err != nil {
		log.Printf("[joiner_sg] recovery error: %v", err)
		return
	}
	if cp == nil {
		log.Printf("[joiner_sg] no checkpoint — starting fresh")
		return
	}

	var states map[string]sgState
	if err := json.Unmarshal(cp.State, &states); err != nil {
		log.Printf("[joiner_sg] unmarshal checkpoint: %v", err)
		return
	}
	for cid, st := range states {
		if st.FanOutByMid == nil {
			st.FanOutByMid = make(map[string][]fanOutResult)
		}
		if st.FanInByMid == nil {
			st.FanInByMid = make(map[string][]fanInResult)
		}
		states[cid] = st
	}
	j.states = states
	log.Printf("[joiner_sg] recovered %d clients from checkpoint", len(states))

	for _, entry := range entries {
		var delta sgDelta
		if err := json.Unmarshal(entry.Delta, &delta); err != nil {
			log.Printf("[joiner_sg] invalid WAL entry: %v", err)
			continue
		}
		j.applyDelta(delta)
		j.sm.MarkApplied(entry.BatchID)
		j.deduper.Mark(entry.BatchID)
	}
	log.Printf("[joiner_sg] recovery done: %d WAL entries replayed", len(entries))
}

func (j *joinerSG) applyDelta(delta sgDelta) {
	state, ok := j.states[delta.ClientID]
	if !ok {
		state = newSGState()
	}

	for _, fo := range delta.FanOut {
		key := middleKey(fo.MiddleBank, fo.MiddleAccount)
		state.FanOutByMid[key] = append(state.FanOutByMid[key], fo)
	}

	for _, fi := range delta.FanIn {
		key := middleKey(fi.MiddleBank, fi.MiddleAccount)
		state.FanInByMid[key] = append(state.FanInByMid[key], fi)
	}

	j.states[delta.ClientID] = state
}

func sendChunks(outputMW middleware.Middleware, clientID string, items []protocol.ScatterGatherItem, keyPrefix string, partitions int, parentBatchID string) {
	sort.Slice(items, func(i, j int) bool {
		a := items[i]
		b := items[j]

		ka := a.FromBank +
			"|" + a.FromAccount +
			"|" + a.ToBank +
			"|" + a.ToAccount

		kb := b.FromBank +
			"|" + b.FromAccount +
			"|" + b.ToBank +
			"|" + b.ToAccount

		return ka < kb
	})

	grouped := make(map[int][]protocol.ScatterGatherItem)
	for _, item := range items {
		key := item.FromBank + item.FromAccount + item.ToBank + item.ToAccount
		p := worker.PartitionForKey(key, partitions)
		grouped[p] = append(grouped[p], item)
	}

	for partition, partItems := range grouped {
		routingKey := worker.RoutingKey(keyPrefix, partition)
		chunkIndex := 0
		for len(partItems) > 0 {
			end := chunkSize
			if end > len(partItems) {
				end = len(partItems)
			}
			out := protocol.Batch{
				Type:               protocol.BatchTypeScatterGather,
				ClientID:           clientID,
				BatchID:            id.Joiner(parentBatchID, partition, chunkIndex),
				ScatterGatherItems: partItems[:end],
			}
			partItems = partItems[end:]
			data, err := json.Marshal(out)
			if err != nil {
				log.Printf("[joiner_sg] marshal chunk partition=%d: %v", partition, err)
				continue
			}
			chunkIndex++
			if err := outputMW.SendWithKey(middleware.Message{Body: string(data)}, routingKey); err != nil {
				log.Printf("[joiner_sg] send chunk partition=%d: %v", partition, err)
			}
		}
	}
}
