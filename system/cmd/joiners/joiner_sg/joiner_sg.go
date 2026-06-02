package main

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func newProcess() node.ProcessFunc {
	var mu sync.Mutex
	states := make(map[string]sgState)

	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeEOF {
			mu.Lock()
			delete(states, batch.ClientID)
			mu.Unlock()
			return protocol.Batch{}, false
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
			for _, fi := range results {
				key := middleKey(fi.MiddleBank, fi.MiddleAccount)
				state.fanInByMid[key] = append(state.fanInByMid[key], fi)
				for _, fo := range state.fanOutByMid[key] {
					items = append(items, makeScatterGatherItem(fo, fi))
				}
			}
		}

		states[batch.ClientID] = state

		if len(items) == 0 {
			return protocol.Batch{}, false
		}

		return protocol.Batch{
			Type:               protocol.BatchTypeScatterGather,
			ClientID:           batch.ClientID,
			ScatterGatherItems: items,
		}, true
	}
}
