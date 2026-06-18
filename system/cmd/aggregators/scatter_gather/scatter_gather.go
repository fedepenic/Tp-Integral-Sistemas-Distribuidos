package main

import (
	"encoding/json"
	"log"
	"sort"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/dedup"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/id"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

const chunkSize = 10_000

type scatterGather struct {
	// clientID → sgKey → entry
	state    map[string]map[string]*sgEntry
	outputMW middleware.Middleware
	dedup    *dedup.BatchDeduplicator
}

func newScatterGather(outputMW middleware.Middleware) *scatterGather {
	return &scatterGather{
		state:    make(map[string]map[string]*sgEntry),
		outputMW: outputMW,
		dedup:    dedup.New(),
	}
}

func (sg *scatterGather) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
		if sg.dedup.Seen(batch.BatchID) {
			return protocol.Batch{}, false
		}
	}
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
	sg.dedup.Mark(batch.BatchID)
	return protocol.Batch{}, false
}

func (sg *scatterGather) flush(clientID string) {
	byKey, ok := sg.state[clientID]
	if !ok {
		return
	}
	delete(sg.state, clientID)

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	chunkCount := 0

	passed, total := 0, len(byKey)
	chunk := make([]scatterGatherResult, 0, chunkSize)

	for _, key := range keys {
		entry := byKey[key]

		count := entry.count
		if count <= scatterThreshold {
			continue
		}

		// Skip self-loops (source == destination)
		if entry.fromBank == entry.toBank &&
			entry.fromAccount == entry.toAccount {
			continue
		}

		passed++

		chunk = append(chunk, scatterGatherResult{
			FromBank:    entry.fromBank,
			FromAccount: entry.fromAccount,
			ToBank:      entry.toBank,
			ToAccount:   entry.toAccount,
			TargetCount: count,
		})

		if len(chunk) >= chunkSize {
			if err := sg.sendChunk(
				clientID,
				chunk,
				chunkCount,
			); err != nil {
				log.Printf("[scatter_gather] send chunk=%d: %v", chunkCount, err)
			}

			chunk = make([]scatterGatherResult, 0, chunkSize)
			chunkCount++
		}
	}

	if len(chunk) > 0 {
		if err := sg.sendChunk(
			clientID,
			chunk,
			chunkCount,
		); err != nil {
			log.Printf("[scatter_gather] send chunk=%d: %v", chunkCount, err)
		}
	}

	log.Printf(
		"[scatter_gather] flush client=%s total_pairs=%d passed_threshold=%d",
		clientID,
		total,
		passed,
	)

	sg.sendEOF(clientID)
}

func (sg *scatterGather) sendChunk(
	clientID string,
	results []scatterGatherResult,
	chunkCount int,
) error {
	instance := config.MustEnvInt("INSTANCE_ID")
	out := protocol.Batch{
		Type:     protocol.BatchTypeData,
		ClientID: clientID,
		DataType: "scatter_gather_result",
		Records:  mustMarshal(results),
		BatchID:  id.Aggregator("scatter_gather", clientID, 0, chunkCount, instance),
	}

	data, err := json.Marshal(out)
	if err != nil {
		return err
	}

	return sg.outputMW.Send(
		middleware.Message{
			Body: string(data),
		},
	)
}

func (sg *scatterGather) sendEOF(clientID string) {
	instance := config.MustEnvInt("INSTANCE_ID")

	eofBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: "scatter_gather_result",
		BatchID: id.AggregatorEOF(
			"scatter_gather", instance, clientID),
	}

	eofData, err := json.Marshal(eofBatch)
	if err != nil {
		log.Printf("[scatter_gather] marshal EOF: %v", err)
		return
	}

	if err := sg.outputMW.Send(
		middleware.Message{
			Body: string(eofData),
		},
	); err != nil {
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
