package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/id"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	maxperbankstore "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/persistence/maxperbank"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

const chunkSize = 1000

type maxPerBank struct {
	// clientID -> bankID -> state
	state            map[string]map[string]maxPerBankState
	outputMW         middleware.Middleware
	outputKeyPrefix  string
	outputPartitions int
	store            *maxperbankstore.Store
}

func newMaxPerBank(outputMW middleware.Middleware) *maxPerBank {
	store, err := maxperbankstore.NewFromEnv()
	if err != nil {
		log.Fatalf("[max_per_bank] initialize persistence: %v", err)
	}
	recovered, err := store.Recover()
	if err != nil {
		log.Fatalf("[max_per_bank] recover persisted state: %v", err)
	}

	return &maxPerBank{
		state:            fromPersistentState(recovered),
		outputMW:         outputMW,
		outputKeyPrefix:  config.EnvOrDefault("OUTPUT_KEY_PREFIX", "joinq2"),
		outputPartitions: config.MustEnvInt("OUTPUT_PARTITIONS"),
		store:            store,
	}
}

func (m *maxPerBank) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.Type == protocol.BatchTypeEOF {
		if err := m.flush(batch.ClientID); err != nil {
			log.Fatalf("[max_per_bank] flush client=%s: %v", batch.ClientID, err)
		}
		return batch, true
	}
	banks, ok := m.state[batch.ClientID]
	if !ok {
		banks = make(map[string]maxPerBankState)
		m.state[batch.ClientID] = banks
	}
	updates := make([]maxperbankstore.State, 0)
	for _, t := range batch.Transactions {
		previous := banks[t.FromBank]
		next := accumulate(previous, t)
		banks[t.FromBank] = next
		if next != previous {
			updates = append(updates, toPersistentRecord(next))
		}
	}
	if err := m.store.AppendBatch(batch.ClientID, updates); err != nil {
		log.Fatalf("[max_per_bank] append WAL client=%s batch=%s: %v", batch.ClientID, batch.BatchID, err)
	}
	if err := m.store.MaybeCheckpoint(batch.ClientID, toPersistentClientState(banks)); err != nil {
		log.Fatalf("[max_per_bank] checkpoint client=%s: %v", batch.ClientID, err)
	}
	return protocol.Batch{}, false
}

func (m *maxPerBank) flush(clientID string) error {
	banks, ok := m.state[clientID]
	if !ok {
		if err := m.sendEOF(clientID); err != nil {
			return err
		}
		return m.store.RemoveClient(clientID)
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
				return fmt.Errorf("send partition=%d: %w", partition, err)
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
			return fmt.Errorf("send partition=%d: %w", partition, err)
		}
	}

	if err := m.sendEOF(clientID); err != nil {
		return err
	}
	if err := m.store.RemoveClient(clientID); err != nil {
		return err
	}
	delete(m.state, clientID)
	return nil
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

func (m *maxPerBank) sendEOF(clientID string) error {
	eofBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: "max_per_bank",
		BatchID:  id.AggregatorEOF("max_per_bank", config.MustEnvInt("INSTANCE_ID"), clientID),
	}

	eofData, err := json.Marshal(eofBatch)
	if err != nil {
		return fmt.Errorf("marshal EOF: %w", err)
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
			return fmt.Errorf("send EOF partition=%d: %w", i, err)
		}
	}
	return nil
}

func accumulate(state maxPerBankState, tx protocol.Transaction) maxPerBankState {
	if !state.hasValue || tx.AmountPaid > state.maxAmountUSD {
		return maxPerBankState{
			bankID:        tx.FromBank,
			bankName:      tx.FromBank,
			sourceAccount: tx.FromAccount,
			maxAmountUSD:  tx.AmountPaid,
			hasValue:      true,
		}
	}

	return state
}

func finalize(state maxPerBankState) (maxPerBankResult, bool) {
	if !state.hasValue {
		return maxPerBankResult{}, false
	}
	return maxPerBankResult{
		BankID:        state.bankID,
		BankName:      state.bankName,
		SourceAccount: state.sourceAccount,
		MaxAmountUSD:  state.maxAmountUSD,
	}, true
}

func toPersistentRecord(state maxPerBankState) maxperbankstore.State {
	return maxperbankstore.State{
		BankID:        state.bankID,
		BankName:      state.bankName,
		SourceAccount: state.sourceAccount,
		MaxAmountUSD:  state.maxAmountUSD,
		HasValue:      state.hasValue,
	}
}

func toPersistentClientState(state map[string]maxPerBankState) map[string]maxperbankstore.State {
	out := make(map[string]maxperbankstore.State, len(state))
	for bankID, bankState := range state {
		out[bankID] = toPersistentRecord(bankState)
	}
	return out
}

func fromPersistentState(state map[string]map[string]maxperbankstore.State) map[string]map[string]maxPerBankState {
	out := make(map[string]map[string]maxPerBankState, len(state))
	for clientID, banks := range state {
		out[clientID] = make(map[string]maxPerBankState, len(banks))
		for bankID, bankState := range banks {
			out[clientID][bankID] = maxPerBankState{
				bankID:        bankState.BankID,
				bankName:      bankState.BankName,
				sourceAccount: bankState.SourceAccount,
				maxAmountUSD:  bankState.MaxAmountUSD,
				hasValue:      bankState.HasValue,
			}
		}
	}
	return out
}
