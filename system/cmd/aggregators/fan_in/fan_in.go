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
	log.Printf("[fan_in] batch client=%s txns=%d", batch.ClientID, len(batch.Transactions))

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
				toBank: tx.ToBank,
				toAcct: tx.ToAccount,
				refs:   make(map[accountRef]struct{}),
			}
			byTo[toKey] = entry
		}
		entry.refs[accountRef{bank: tx.FromBank, account: tx.FromAccount}] = struct{}{}
	}
	return protocol.Batch{}, false
}

func (f *fanIn) flush(clientID string) {
	byTo, ok := f.state[clientID]
	if !ok {
		return
	}
	delete(f.state, clientID)
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
		for ref := range entry.refs {
			total++
			res := fanInResult{
				MiddleBank:    ref.bank,
				MiddleAccount: ref.account,
				ToBank:        entry.toBank,
				ToAccount:     entry.toAcct,
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
		}
	}

	f.sendEOF(clientID)
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
