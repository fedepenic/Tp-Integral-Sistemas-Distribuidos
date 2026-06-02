package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

type fanIn struct {
	// clientID → toKey → entry
	state            map[string]map[string]*fanInEntry
	outputMW         middleware.Middleware
	outputKeyPrefix  string
	outputPartitions int
}

func newFanIn(outputMW middleware.Middleware) *fanIn {
	return &fanIn{
		state:            make(map[string]map[string]*fanInEntry),
		outputMW:         outputMW,
		outputKeyPrefix:  config.EnvOrDefault("OUTPUT_KEY_PREFIX", "joinersg"),
		outputPartitions: config.MustEnvInt("OUTPUT_PARTITIONS"),
	}
}

func (f *fanIn) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.Type == protocol.BatchTypeEOF {
		f.flush(batch.ClientID)
		return batch, true
	}
	if batch.Type != protocol.BatchTypeTransactions {
		return protocol.Batch{}, false
	}

	byTo, ok := f.state[batch.ClientID]
	if !ok {
		byTo = make(map[string]*fanInEntry)
		f.state[batch.ClientID] = byTo
	}
	for _, tx := range batch.Transactions {
		toKey := tx.ToBank + "|" + tx.ToAccount
		entry, ok := byTo[toKey]
		if !ok {
			entry = &fanInEntry{
				toBank:   tx.ToBank,
				toAcct:   tx.ToAccount,
				distinct: make(map[string]accountRef),
			}
			byTo[toKey] = entry
		}
		from := accountRef{bank: tx.FromBank, account: tx.FromAccount}
		entry.distinct[refKey(from)] = from
	}
	return protocol.Batch{}, false
}

func (f *fanIn) flush(clientID string) {
	byTo, ok := f.state[clientID]
	if !ok {
		return
	}
	delete(f.state, clientID)

	partitioned := make(map[int][]fanInResult)
	for _, entry := range byTo {
		for _, from := range entry.distinct {
			res := fanInResult{
				MiddleBank:    from.bank,
				MiddleAccount: from.account,
				ToBank:        entry.toBank,
				ToAccount:     entry.toAcct,
			}
			p := worker.PartitionForKey(res.MiddleAccount, f.outputPartitions)
			partitioned[p] = append(partitioned[p], res)
		}
	}

	for partition, results := range partitioned {
		routingKey := worker.RoutingKey(f.outputKeyPrefix, partition)
		raw, err := json.Marshal(results)
		if err != nil {
			log.Printf("[fan_in] marshal results partition=%d: %v", partition, err)
			continue
		}
		out := protocol.Batch{
			Type:     protocol.BatchTypeData,
			ClientID: clientID,
			DataType: "fanin_result",
			Records:  raw,
		}
		data, err := json.Marshal(out)
		if err != nil {
			log.Printf("[fan_in] marshal batch partition=%d: %v", partition, err)
			continue
		}
		if err := f.outputMW.SendWithKey(middleware.Message{Body: string(data)}, routingKey); err != nil {
			log.Printf("[fan_in] send results partition=%d: %v", partition, err)
		}
	}

	eofBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: "fanin_result",
	}
	eofData, err := json.Marshal(eofBatch)
	if err != nil {
		log.Printf("[fan_in] marshal EOF: %v", err)
		return
	}
	for i := 0; i < f.outputPartitions; i++ {
		routingKey := worker.RoutingKey(f.outputKeyPrefix, i)
		if err := f.outputMW.SendWithKey(middleware.Message{Body: string(eofData)}, routingKey); err != nil {
			log.Printf("[fan_in] send EOF partition=%d: %v", i, err)
		}
	}
}
