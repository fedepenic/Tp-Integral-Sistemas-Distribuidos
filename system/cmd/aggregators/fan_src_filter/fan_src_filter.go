package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

const minDistinctDests = 5

type fanSrcFilter struct {
	// clientID → fromKey → srcEntry
	state         map[string]map[string]*srcEntry
	outFOMW       middleware.Middleware
	outFIMW       middleware.Middleware
	foKeyPrefix   string
	foPartitions  int
	fiKeyPrefix   string
	fiPartitions  int
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
	}
}

func init() {
	_ = config.EnvOrDefault // silence unused import if needed
}

func (f *fanSrcFilter) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.Type == protocol.BatchTypeEOF {
		f.flush(batch.ClientID)
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
	return protocol.Batch{}, false
}

func (f *fanSrcFilter) flush(clientID string) {
	byFrom, ok := f.state[clientID]
	if !ok {
		return
	}
	delete(f.state, clientID)

	foPart := make(map[int][]protocol.Transaction)
	fiPart := make(map[int][]protocol.Transaction)

	for _, entry := range byFrom {
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

	qualifying := 0
	for _, entry := range byFrom {
		if len(entry.distinctTo) > minDistinctDests {
			qualifying++
		}
	}
	log.Printf("[fan_src_filter] flush client=%s total_sources=%d qualifying=%d", clientID, len(byFrom), qualifying)

	sendPartitioned(f.outFOMW, clientID, foPart, f.foKeyPrefix)
	sendPartitioned(f.outFIMW, clientID, fiPart, f.fiKeyPrefix)

	sendEOF(f.outFOMW, clientID, f.foKeyPrefix, f.foPartitions)
	sendEOF(f.outFIMW, clientID, f.fiKeyPrefix, f.fiPartitions)
}

func sendPartitioned(mw middleware.Middleware, clientID string, partitioned map[int][]protocol.Transaction, keyPrefix string) {
	for p, txns := range partitioned {
		key := worker.RoutingKey(keyPrefix, p)
		out := protocol.Batch{
			Type:         protocol.BatchTypeTransactions,
			ClientID:     clientID,
			Transactions: txns,
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
	eof := protocol.Batch{Type: protocol.BatchTypeEOF, ClientID: clientID}
	data, err := json.Marshal(eof)
	if err != nil {
		log.Printf("[fan_src_filter] marshal EOF: %v", err)
		return
	}
	for i := 0; i < partitions; i++ {
		key := worker.RoutingKey(keyPrefix, i)
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[fan_src_filter] send EOF key=%s: %v", key, err)
		}
	}
}
