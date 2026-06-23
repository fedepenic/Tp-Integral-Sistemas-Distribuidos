package main

import (
	"encoding/json"
	"log"
	"sort"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/dedup"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/id"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

const chunkSize = 1000

type fanOut struct {
	// clientID → fromKey → entry
	state            map[string]map[string]*fanOutEntry
	outputMW         middleware.Middleware
	outputKeyPrefix  string
	outputPartitions int
	deduper          *dedup.BatchDeduplicator
}

func newFanOut(outputMW middleware.Middleware) *fanOut {
	return &fanOut{
		state:            make(map[string]map[string]*fanOutEntry),
		outputMW:         outputMW,
		outputKeyPrefix:  config.EnvOrDefault("OUTPUT_KEY_PREFIX", "joinersg"),
		outputPartitions: config.MustEnvInt("OUTPUT_PARTITIONS"),
		deduper:          dedup.New(),
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
	if batch.Type != protocol.BatchTypeTransactions {
		return protocol.Batch{}, false
	}
	log.Printf("[fan_out] batch client=%s txns=%d", batch.ClientID, len(batch.Transactions))

	byFrom, ok := f.state[batch.ClientID]
	if !ok {
		byFrom = make(map[string]*fanOutEntry)
		f.state[batch.ClientID] = byFrom
	}
	for _, tx := range batch.Transactions {
		fromKey := tx.FromBank + "|" + tx.FromAccount
		entry, ok := byFrom[fromKey]
		if !ok {
			entry = &fanOutEntry{
				fromBank: tx.FromBank,
				fromAcct: tx.FromAccount,
			}
			byFrom[fromKey] = entry
		}
		entry.refs = append(entry.refs, accountRef{bank: tx.ToBank, account: tx.ToAccount})
	}

	if batch.BatchID != "" {
		f.deduper.Mark(batch.BatchID)
	}
	return protocol.Batch{}, false
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
		for _, to := range entry.refs {
			res := fanOutResult{
				FromBank:      entry.fromBank,
				FromAccount:   entry.fromAcct,
				MiddleBank:    to.bank,
				MiddleAccount: to.account,
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
