package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

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
		return protocol.Batch{}, false
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

	partitioned := make(map[int][]maxPerBankResult)
	for _, state := range banks {
		result, ok := finalize(state)
		if !ok {
			continue
		}
		p := worker.PartitionForKey(result.BankID, m.outputPartitions)
		partitioned[p] = append(partitioned[p], result)
	}

	for partition, results := range partitioned {
		routingKey := worker.RoutingKey(m.outputKeyPrefix, partition)
		raw, err := json.Marshal(results)
		if err != nil {
			log.Printf("[max_per_bank] marshal results partition=%d: %v", partition, err)
			continue
		}
		out := protocol.Batch{
			Type:     protocol.BatchTypeData,
			ClientID: clientID,
			DataType: "max_per_bank",
			Records:  raw,
		}
		data, err := json.Marshal(out)
		if err != nil {
			log.Printf("[max_per_bank] marshal batch partition=%d: %v", partition, err)
			continue
		}
		if err := m.outputMW.SendWithKey(middleware.Message{Body: string(data)}, routingKey); err != nil {
			log.Printf("[max_per_bank] send results partition=%d: %v", partition, err)
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
