package main

import (
	"encoding/json"
	"log"
	"sort"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/id"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

const chunkSize = 10_000

func newProcess(outputMW middleware.Middleware, outputKeyPrefix string, outputPartitions int) node.ProcessFunc {
	var mu sync.Mutex
	states := make(map[string]sgState)
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeEOF {
			mu.Lock()
			delete(states, batch.ClientID)
			mu.Unlock()
			for i := 0; i < outputPartitions; i++ {
				routingKey := worker.RoutingKey(outputKeyPrefix, i)
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
				if err := outputMW.SendWithKey(middleware.Message{Body: string(data)}, routingKey); err != nil {
					log.Printf("[joiner_sg] send EOF partition=%d: %v", i, err)
				}
			}
			return batch, true
		}

		mu.Lock()
		defer mu.Unlock()

		state, ok := states[batch.ClientID]
		if !ok {
			state = newSGState()
		}

		var items []protocol.ScatterGatherItem

		switch batch.DataType {
		case "fanout_result":
			var results []fanOutResult
			if err := json.Unmarshal(batch.Records, &results); err != nil {
				log.Printf("[joiner_sg] malformed fanout_result: %v", err)
				states[batch.ClientID] = state
				return protocol.Batch{}, false
			}
			log.Printf("[joiner_sg] fanout_result client=%s results=%d", batch.ClientID, len(results))
			for _, fo := range results {
				key := middleKey(fo.MiddleBank, fo.MiddleAccount)
				state.fanOutByMid[key] = append(state.fanOutByMid[key], fo)
				for _, fi := range state.fanInByMid[key] {
					items = append(items, makeScatterGatherItem(fo, fi))
				}
			}

		case "fanin_result":
			var results []fanInResult
			if err := json.Unmarshal(batch.Records, &results); err != nil {
				log.Printf("[joiner_sg] malformed fanin_result: %v", err)
				states[batch.ClientID] = state
				return protocol.Batch{}, false
			}
			log.Printf("[joiner_sg] fanin_result client=%s results=%d", batch.ClientID, len(results))
			for _, fi := range results {
				key := middleKey(fi.MiddleBank, fi.MiddleAccount)
				state.fanInByMid[key] = append(state.fanInByMid[key], fi)
				for _, fo := range state.fanOutByMid[key] {
					items = append(items, makeScatterGatherItem(fo, fi))
				}
			}
		}

		states[batch.ClientID] = state

		log.Printf("[joiner_sg] produced items=%d client=%s", len(items), batch.ClientID)
		sendChunks(outputMW, batch.ClientID, items, outputKeyPrefix, outputPartitions)
		return protocol.Batch{}, false
	}
}

func sendChunks(outputMW middleware.Middleware, clientID string, items []protocol.ScatterGatherItem, keyPrefix string, partitions int) {
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
		for len(partItems) > 0 {
			end := chunkSize
			if end > len(partItems) {
				end = len(partItems)
			}
			out := protocol.Batch{
				Type:               protocol.BatchTypeScatterGather,
				ClientID:           clientID,
				ScatterGatherItems: partItems[:end],
				BatchID: id.HashBatchJoinSG(
					"joiner_sg",
					clientID,
					partition,
					partItems[:end],
				),
			}
			partItems = partItems[end:]
			data, err := json.Marshal(out)
			if err != nil {
				log.Printf("[joiner_sg] marshal chunk partition=%d: %v", partition, err)
				continue
			}
			if err := outputMW.SendWithKey(middleware.Message{Body: string(data)}, routingKey); err != nil {
				log.Printf("[joiner_sg] send chunk partition=%d: %v", partition, err)
			}
		}
	}
}
