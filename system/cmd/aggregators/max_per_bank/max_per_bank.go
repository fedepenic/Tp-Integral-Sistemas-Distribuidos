package main

import (
	"encoding/json"
	"log"
	"sort"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/id"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

const chunkSize = 1000

type maxPerBank struct {
	state            map[string]map[string]maxPerBankState
	outputMW         middleware.Middleware
	outputKeyPrefix  string
	outputPartitions int
	sm               *node.StateManager
}

func newMaxPerBank(outputMW middleware.Middleware) *maxPerBank {
	stateDir := config.EnvOrDefault("STATE_DIR", "")
	freq := node.CheckpointFreqFromEnv(10000)
	return &maxPerBank{
		state:            make(map[string]map[string]maxPerBankState),
		outputMW:         outputMW,
		outputKeyPrefix:  config.EnvOrDefault("OUTPUT_KEY_PREFIX", "joinq2"),
		outputPartitions: config.MustEnvInt("OUTPUT_PARTITIONS"),
		sm:               node.NewStateManager("max_per_bank", "max_per_bank", stateDir, freq),
	}
}

func (m *maxPerBank) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.Type == protocol.BatchTypeEOF {
		if m.flush(batch.ClientID) {
			return batch, true
		}
		return protocol.Batch{}, false
	}

	delta := m.computeDelta(batch)

	if m.sm.Enabled() && batch.BatchID != "" {
		deltaData, err := json.Marshal(delta)
		if err != nil {
			log.Printf("[max_per_bank] marshal delta: %v", err)
			return protocol.Batch{}, false
		}
		if err := m.sm.AppendWAL(batch.BatchID, deltaData); err != nil {
			log.Printf("[max_per_bank] WAL append: %v", err)
			return protocol.Batch{}, false
		}
	}

	m.applyDelta(delta)

	if batch.BatchID != "" {

	}

	if m.sm.ShouldCheckpoint() {
		m.checkpoint()
	}

	return protocol.Batch{}, false
}

func (m *maxPerBank) computeDelta(batch protocol.Batch) maxPerBankDelta {
	d := maxPerBankDelta{
		ClientID: batch.ClientID,
		Banks:    make(map[string]maxPerBankState),
	}
	for _, t := range batch.Transactions {
		cur, exists := d.Banks[t.FromBank]
		if !exists {
			cur = m.state[batch.ClientID][t.FromBank]
		}
		d.Banks[t.FromBank] = accumulate(cur, t)
	}
	return d
}

func (m *maxPerBank) applyDelta(delta maxPerBankDelta) {
	banks, ok := m.state[delta.ClientID]
	if !ok {
		banks = make(map[string]maxPerBankState)
		m.state[delta.ClientID] = banks
	}
	for bankID, newState := range delta.Banks {
		cur := banks[bankID]
		if !cur.HasValue || newState.MaxAmountUSD > cur.MaxAmountUSD {
			banks[bankID] = newState
		}
	}
}

func (m *maxPerBank) checkpoint() {
	data, err := json.Marshal(m.state)
	if err != nil {
		log.Printf("[max_per_bank] marshal state for checkpoint: %v", err)
		return
	}
	if err := m.sm.SaveCheckpoint(data); err != nil {
		log.Printf("[max_per_bank] save checkpoint: %v", err)
	}
}

func (m *maxPerBank) recover() {
	cp, entries, err := m.sm.Recover()
	if err != nil {
		log.Printf("[max_per_bank] recovery error: %v", err)
		return
	}
	if cp == nil && len(entries) == 0 {
		log.Printf("[max_per_bank] no checkpoint — starting fresh")
		return
	}

	if cp != nil {
		var state map[string]map[string]maxPerBankState
		if err := json.Unmarshal(cp.State, &state); err != nil {
			log.Printf("[max_per_bank] unmarshal checkpoint state: %v", err)
			return
		}
		m.state = state
		log.Printf("[max_per_bank] recovered %d clients from checkpoint", len(state))
	} else {
		log.Printf("[max_per_bank] no checkpoint, replaying %d WAL entries from scratch", len(entries))
	}

	for _, entry := range entries {
		var delta maxPerBankDelta
		if err := json.Unmarshal(entry.Delta, &delta); err != nil {
			log.Printf("[max_per_bank] invalid WAL entry: %v", err)
			continue
		}
		m.applyDelta(delta)

	}
	log.Printf("[max_per_bank] recovery done: %d WAL entries replayed", len(entries))
}

func (m *maxPerBank) flush(clientID string) bool {
	banks, ok := m.state[clientID]
	if !ok {
		return true
	}

	keys := make([]string, 0, len(banks))
	for bankID := range banks {
		keys = append(keys, bankID)
	}
	sort.Strings(keys)

	chunkCountByPartition := make(map[int]int)
	partitioned := make(map[int][]maxPerBankResult)
	for _, bankID := range keys {
		state := banks[bankID]
		result, ok := finalize(state)
		if !ok {
			continue
		}

		partition := worker.PartitionForKey(result.BankID, m.outputPartitions)

		partitioned[partition] = append(
			partitioned[partition],
			result,
		)

		if len(partitioned[partition]) >= chunkSize {
			if err := m.sendPartition(clientID, partitioned[partition], partition, chunkCountByPartition[partition]); err != nil {
				log.Printf("[max_per_bank] send partition=%d: %v", partition, err)
				return false
			}

			partitioned[partition] = nil
			chunkCountByPartition[partition]++
		}
	}

	for partition, results := range partitioned {
		if len(results) == 0 {
			continue
		}

		if err := m.sendPartition(clientID, results, partition, chunkCountByPartition[partition]); err != nil {
			log.Printf("[max_per_bank] send partition=%d: %v", partition, err)
			return false
		}
	}

	delete(m.state, clientID)
	m.sendEOF(clientID)
	return true
}

func (m *maxPerBank) sendPartition(clientID string, results []maxPerBankResult, partition int, chunkCount int) error {
	routingKey := worker.RoutingKey(
		m.outputKeyPrefix,
		partition,
	)

	raw, err := json.Marshal(results)
	if err != nil {
		return err
	}

	out := protocol.Batch{
		Type:     protocol.BatchTypeData,
		ClientID: clientID,
		DataType: "max_per_bank",
		Records:  raw,
		BatchID:  id.Aggregator("max_per_bank", clientID, partition, chunkCount, config.MustEnvInt("INSTANCE_ID")),
	}

	data, err := json.Marshal(out)
	if err != nil {
		return err
	}

	return m.outputMW.SendWithKey(
		middleware.Message{
			Body: string(data),
		},
		routingKey,
	)
}

func (m *maxPerBank) sendEOF(clientID string) {
	eofBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: "max_per_bank",
		BatchID:  id.AggregatorEOF("max_per_bank", config.MustEnvInt("INSTANCE_ID"), clientID),
	}

	eofData, err := json.Marshal(eofBatch)
	if err != nil {
		log.Printf("[max_per_bank] marshal EOF: %v", err)
		return
	}

	for i := 0; i < m.outputPartitions; i++ {
		routingKey := worker.RoutingKey(
			m.outputKeyPrefix,
			i,
		)

		if err := m.outputMW.SendWithKey(
			middleware.Message{
				Body: string(eofData),
			},
			routingKey,
		); err != nil {
			log.Printf("[max_per_bank] send EOF partition=%d: %v", i, err)
		}
	}
}

func accumulate(state maxPerBankState, tx protocol.Transaction) maxPerBankState {
	if !state.HasValue || tx.AmountPaid > state.MaxAmountUSD {
		return maxPerBankState{
			BankID:        tx.FromBank,
			BankName:      tx.FromBank,
			SourceAccount: tx.FromAccount,
			MaxAmountUSD:  tx.AmountPaid,
			HasValue:      true,
		}
	}
	return state
}

func finalize(state maxPerBankState) (maxPerBankResult, bool) {
	if !state.HasValue {
		return maxPerBankResult{}, false
	}
	return maxPerBankResult{
		BankID:        state.BankID,
		BankName:      state.BankName,
		SourceAccount: state.SourceAccount,
		MaxAmountUSD:  state.MaxAmountUSD,
	}, true
}
