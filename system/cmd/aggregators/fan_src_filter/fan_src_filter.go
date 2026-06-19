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

const minDistinctDests = 5

type fanSrcFilter struct {
	// clientID → fromKey → srcEntry
	state        map[string]map[string]*srcEntry
	outFOMW      middleware.Middleware
	outFIMW      middleware.Middleware
	foKeyPrefix  string
	foPartitions int
	fiKeyPrefix  string
	fiPartitions int
	dedup        *dedup.BatchDeduplicator
}

func newFanSrcFilter(
	outFOMW, outFIMW middleware.Middleware,
	foKeyPrefix string, foPartitions int,
	fiKeyPrefix string, fiPartitions int,
) *fanSrcFilter {
	return &fanSrcFilter{
		state:        make(map[string]map[string]*srcEntry),
		outFOMW:      outFOMW,
		outFIMW:      outFIMW,
		foKeyPrefix:  foKeyPrefix,
		foPartitions: foPartitions,
		fiKeyPrefix:  fiKeyPrefix,
		fiPartitions: fiPartitions,
		dedup:        dedup.New(),
	}
}

func (f *fanSrcFilter) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
		if f.dedup.Seen(batch.BatchID) {
			log.Printf("[fan_src_filter] duplicate batch: %s", batch.BatchID)
			return protocol.Batch{}, false
		}
	}

	if batch.Type == protocol.BatchTypeEOF {
		f.flush(batch.ClientID, batch.BatchID)
		return batch, true
	}
	if batch.Type != protocol.BatchTypeTransactions {
		return protocol.Batch{}, false
	}
	log.Printf("[fan_src_filter] batch client=%s txns=%d", batch.ClientID, len(batch.Transactions))

	byFrom, ok := f.state[batch.ClientID]
	if !ok {
		byFrom = make(map[string]*srcEntry)
		f.state[batch.ClientID] = byFrom
	}
	for _, tx := range batch.Transactions {
		fromKey := tx.FromBank + "|" + tx.FromAccount
		entry, ok := byFrom[fromKey]
		if !ok {
			entry = &srcEntry{
				fromBank:   tx.FromBank,
				fromAcct:   tx.FromAccount,
				distinctTo: make(map[string]struct{}),
			}
			byFrom[fromKey] = entry
		}
		entry.distinctTo[tx.ToBank+"|"+tx.ToAccount] = struct{}{}
		entry.transactions = append(entry.transactions, tx)
	}

	if batch.BatchID != "" {
		f.dedup.Mark(batch.BatchID)
	}
	return protocol.Batch{}, false
}

func (f *fanSrcFilter) flush(clientID string, parentBatchID string) {
	byFrom, ok := f.state[clientID]
	if !ok {
		return
	}
	delete(f.state, clientID)

	foPart := make(map[int][]protocol.Transaction)
	fiPart := make(map[int][]protocol.Transaction)

	fromKeys := make([]string, 0, len(byFrom))
	for k := range byFrom {
		fromKeys = append(fromKeys, k)
	}
	sort.Strings(fromKeys)

	for _, key := range fromKeys {
		entry := byFrom[key]
		if len(entry.distinctTo) <= minDistinctDests {
			continue
		}
		for _, tx := range entry.transactions {
			foP := worker.PartitionForKey(tx.FromBank+"|"+tx.FromAccount, f.foPartitions)
			foPart[foP] = append(foPart[foP], tx)
			fiP := worker.PartitionForKey(tx.ToBank+"|"+tx.ToAccount, f.fiPartitions)
			fiPart[fiP] = append(fiPart[fiP], tx)
		}
	}

	log.Printf("[fan_src_filter] flush client=%s total_sources=%d parent_batch=%s",
		clientID, len(byFrom), parentBatchID)

	sendPartitioned(f.outFOMW, clientID, foPart, f.foKeyPrefix)
	sendPartitioned(f.outFIMW, clientID, fiPart, f.fiKeyPrefix)

	sendEOF(f.outFOMW, clientID, f.foKeyPrefix, f.foPartitions)
	sendEOF(f.outFIMW, clientID, f.fiKeyPrefix, f.fiPartitions)
}

func sendPartitioned(mw middleware.Middleware, clientID string, partitioned map[int][]protocol.Transaction, keyPrefix string) {
	parts := make([]int, 0, len(partitioned))
	instance := config.MustEnvInt("INSTANCE_ID")
	for p := range partitioned {
		parts = append(parts, p)
	}
	sort.Ints(parts)

	for _, p := range parts {
		txns := partitioned[p]
		key := worker.RoutingKey(keyPrefix, p)
		out := protocol.Batch{
			Type:         protocol.BatchTypeTransactions,
			ClientID:     clientID,
			Transactions: txns,
			BatchID:      id.Aggregator("fan_src_filter", clientID, p, 0, instance),
		}
		data, err := json.Marshal(out)
		if err != nil {
			log.Printf("[fan_src_filter] marshal batch key=%s: %v", key, err)
			continue
		}
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[fan_src_filter] send batch key=%s: %v", key, err)
		}
	}
}

func sendEOF(mw middleware.Middleware, clientID, keyPrefix string, partitions int) {
	instance := config.MustEnvInt("INSTANCE_ID")
	for i := 0; i < partitions; i++ {
		key := worker.RoutingKey(keyPrefix, i)
		eof := protocol.Batch{
			Type:     protocol.BatchTypeEOF,
			ClientID: clientID,
			BatchID:  id.AggregatorEOF("fan_src_filter", instance, clientID),
		}
		data, err := json.Marshal(eof)
		if err != nil {
			log.Printf("[fan_src_filter] marshal EOF key=%s: %v", key, err)
			continue
		}
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[fan_src_filter] send EOF key=%s: %v", key, err)
		}
	}
}
