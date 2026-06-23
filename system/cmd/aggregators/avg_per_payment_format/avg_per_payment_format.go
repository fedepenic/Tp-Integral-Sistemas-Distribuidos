package main

import (
	"encoding/json"
	"log"
	"sort"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/dedup"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/id"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

const maxRetries = 5

const chunkSize = 1000

type avgPerPaymentFormat struct {
	// clientID -> paymentFormat -> accumulated state
	state            map[string]map[string]avgState
	outputMW         middleware.Middleware
	outputKeyPrefix  string
	outputPartitions int
	dedup            *dedup.BatchDeduplicator
}

func newAvgPerPaymentFormat(outputMW middleware.Middleware) *avgPerPaymentFormat {
	return &avgPerPaymentFormat{
		state:            make(map[string]map[string]avgState),
		outputMW:         outputMW,
		outputKeyPrefix:  config.EnvOrDefault("OUTPUT_KEY_PREFIX", "joinerformat"),
		outputPartitions: config.MustEnvInt("OUTPUT_PARTITIONS"),
		dedup:            dedup.New(),
	}
}

func (m *avgPerPaymentFormat) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
		if m.dedup.Seen(batch.BatchID) {
			log.Printf("[avg_per_payment_format] duplicate batch: %s", batch.BatchID)
			return protocol.Batch{}, false
		}
	}
	if batch.Type == protocol.BatchTypeEOF {
		if m.flush(batch.ClientID) {
			return batch, true
		}
		return protocol.Batch{}, false
	}
	if batch.Type != protocol.BatchTypeTransactions {
		return protocol.Batch{}, false
	}
	formats, ok := m.state[batch.ClientID]
	if !ok {
		formats = make(map[string]avgState)
		m.state[batch.ClientID] = formats
	}
	for _, tx := range batch.Transactions {
		s := formats[tx.PaymentFormat]
		s.sum += tx.AmountPaid
		s.count++
		formats[tx.PaymentFormat] = s
	}
	m.dedup.Mark(batch.BatchID)
	return protocol.Batch{}, false
}

func (m *avgPerPaymentFormat) flush(clientID string) bool {
	formats, ok := m.state[clientID]
	if !ok {
		return true
	}

	keys := make([]string, 0, len(formats))
	for format := range formats {
		keys = append(keys, format)
	}

	sort.Strings(keys)

	chunkCountByPartition := make(map[int]int)
	partitioned := make(map[int][]avgPerFormatResult)
	for _, format := range keys {
		s := formats[format]
		if s.count == 0 {
			continue
		}
		p := worker.PartitionForKey(format, m.outputPartitions)
		partitioned[p] = append(partitioned[p], avgPerFormatResult{
			PaymentFormat: format,
			AvgAmount:     s.sum / float64(s.count),
		})

		if len(partitioned[p]) >= chunkSize {
			if err := m.sendPartition(
				clientID,
				partitioned[p],
				p,
				chunkCountByPartition[p],
			); err != nil {
				log.Printf("[avg_per_payment_format] send partition=%d: %v", p, err)
				return false
			}
			partitioned[p] = nil
			chunkCountByPartition[p]++
		}
	}

	for partition, results := range partitioned {
		if len(results) == 0 {
			continue
		}
		if err := m.sendPartition(
			clientID,
			results,
			partition,
			chunkCountByPartition[partition],
		); err != nil {
			log.Printf("[avg_per_payment_format] send partition=%d: %v", partition, err)
			return false
		}
	}

	delete(m.state, clientID)
	m.sendEOF(clientID)
	return true
}

func (m *avgPerPaymentFormat) sendPartition(
	clientID string,
	results []avgPerFormatResult,
	partition int,
	chunkCount int,
) error {
	routingKey := worker.RoutingKey(
		m.outputKeyPrefix,
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
		DataType: "avg_per_format",
		Records:  raw,
		BatchID:  id.Aggregator("avg_per_format", clientID, partition, chunkCount, instance),
	}

	data, err := json.Marshal(out)
	if err != nil {
		return err
	}

	msg := middleware.Message{Body: string(data)}
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if err := m.outputMW.SendWithKey(msg, routingKey); err != nil {
			lastErr = err
			log.Printf("[avg_per_payment_format] send partition=%d attempt=%d/%d: %v", partition, i+1, maxRetries, err)
			time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
			continue
		}
		return nil
	}
	return lastErr
}

func (m *avgPerPaymentFormat) sendEOF(clientID string) {
	instance := config.MustEnvInt("INSTANCE_ID")
	eofBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: "avg_per_format",
		BatchID:  id.AggregatorEOF("avg_per_format", instance, clientID),
	}

	eofData, err := json.Marshal(eofBatch)
	if err != nil {
		log.Printf("[avg_per_payment_			format] marshal EOF: %v", err)
		return
	}

	for i := 0; i < m.outputPartitions; i++ {
		routingKey := worker.RoutingKey(
			m.outputKeyPrefix,
			i,
		)

		if err := m.outputMW.SendWithKey(
			middleware.Message{Body: string(eofData)},
			routingKey,
		); err != nil {
			log.Printf(
				"[avg_per_payment_format] send EOF partition=%d: %v",
				i,
				err,
			)
		}
	}
}
