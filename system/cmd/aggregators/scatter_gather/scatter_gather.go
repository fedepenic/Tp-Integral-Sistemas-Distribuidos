package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

type scatterGather struct {
	// clientID → sgKey → entry
	state    map[string]map[string]*sgEntry
	outputMW middleware.Middleware
}

func newScatterGather(outputMW middleware.Middleware) *scatterGather {
	return &scatterGather{
		state:    make(map[string]map[string]*sgEntry),
		outputMW: outputMW,
	}
}

func (sg *scatterGather) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.Type == protocol.BatchTypeEOF {
		sg.flush(batch.ClientID)
		return batch, true
	}
	if batch.Type != protocol.BatchTypeScatterGather {
		return protocol.Batch{}, false
	}
	log.Printf("[scatter_gather] batch client=%s items=%d", batch.ClientID, len(batch.ScatterGatherItems))

	byKey, ok := sg.state[batch.ClientID]
	if !ok {
		byKey = make(map[string]*sgEntry)
		sg.state[batch.ClientID] = byKey
	}
	for _, item := range batch.ScatterGatherItems {
		sgKey := item.FromBank + "|" + item.FromAccount + "|" + item.ToBank + "|" + item.ToAccount
		entry, ok := byKey[sgKey]
		if !ok {
			entry = &sgEntry{
				fromBank:    item.FromBank,
				fromAccount: item.FromAccount,
				toBank:      item.ToBank,
				toAccount:   item.ToAccount,
			}
			byKey[sgKey] = entry
		}
		entry.count++
	}
	return protocol.Batch{}, false
}

func (sg *scatterGather) flush(clientID string) {
	byKey, ok := sg.state[clientID]
	if !ok {
		return
	}
	delete(sg.state, clientID)

	passed, total := 0, len(byKey)
	for _, entry := range byKey {
		count := entry.count
		if count <= scatterThreshold {
			continue
		}
		// Skip self-loops (source == destination): the scatter-gather pattern
		// counts paths X -> Y -> Z where X and Z are distinct accounts.
		if entry.fromBank == entry.toBank && entry.fromAccount == entry.toAccount {
			continue
		}
		passed++
		res := scatterGatherResult{
			FromBank:    entry.fromBank,
			FromAccount: entry.fromAccount,
			ToBank:      entry.toBank,
			ToAccount:   entry.toAccount,
			TargetCount: count,
		}
		out := protocol.Batch{
			Type:     protocol.BatchTypeData,
			ClientID: clientID,
			DataType: "scatter_gather_result",
			Records:  mustMarshal([]scatterGatherResult{res}),
		}
		data, err := json.Marshal(out)
		if err != nil {
			log.Printf("[scatter_gather] marshal result: %v", err)
			continue
		}
		if err := sg.outputMW.Send(middleware.Message{Body: string(data)}); err != nil {
			log.Printf("[scatter_gather] send result: %v", err)
		}
	}

	log.Printf("[scatter_gather] flush client=%s total_pairs=%d passed_threshold=%d", clientID, total, passed)

	eofBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: "scatter_gather_result",
	}
	eofData, err := json.Marshal(eofBatch)
	if err != nil {
		log.Printf("[scatter_gather] marshal EOF: %v", err)
		return
	}
	if err := sg.outputMW.Send(middleware.Message{Body: string(eofData)}); err != nil {
		log.Printf("[scatter_gather] send EOF: %v", err)
	}
}

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// ProcessFunc wrapper so node.ProcessFunc signature is satisfied.
func newProcess(sg *scatterGather) node.ProcessFunc {
	return sg.process
}
