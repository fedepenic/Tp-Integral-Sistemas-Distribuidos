package main

import (
	"encoding/json"
	"log"
	"sort"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/id"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
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
}

func newMaxPerBank(outputMW middleware.Middleware) *maxPerBank {
	return &maxPerBank{
		state:            make(map[string]map[string]maxPerBankState),
		outputMW:         outputMW,
		outputKeyPrefix:  config.EnvOrDefault("OUTPUT_KEY_PREFIX", "joinq2"),
		outputPartitions: config.MustEnvInt("OUTPUT_PARTITIONS"),
	}
}

func (m *maxPerBank) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.Type == protocol.BatchTypeEOF {
		m.flush(batch.ClientID)
		return batch, true
	}
	banks, ok := m.state[batch.ClientID]
	if !ok {
		banks = make(map[string]maxPerBankState)
		m.state[batch.ClientID] = banks
	}
	for _, t := range batch.Transactions {
		banks[t.FromBank] = accumulate(banks[t.FromBank], t)
	}
	return protocol.Batch{}, false
}

func (m *maxPerBank) flush(clientID string) {
	banks, ok := m.state[clientID]
	if !ok {
		return
	}
	delete(m.state, clientID)

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
		}
	}

	m.sendEOF(clientID)
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
