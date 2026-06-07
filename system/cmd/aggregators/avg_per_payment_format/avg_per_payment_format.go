package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

type avgPerPaymentFormat struct {
	// clientID -> paymentFormat -> accumulated state
	state            map[string]map[string]avgState
	outputMW         middleware.Middleware
	outputKeyPrefix  string
	outputPartitions int
}

func newAvgPerPaymentFormat(outputMW middleware.Middleware) *avgPerPaymentFormat {
	return &avgPerPaymentFormat{
		state:            make(map[string]map[string]avgState),
		outputMW:         outputMW,
		outputKeyPrefix:  config.EnvOrDefault("OUTPUT_KEY_PREFIX", "joinerformat"),
		outputPartitions: config.MustEnvInt("OUTPUT_PARTITIONS"),
	}
}

func (m *avgPerPaymentFormat) process(batch protocol.Batch) (protocol.Batch, bool) {
	if batch.Type == protocol.BatchTypeEOF {
		m.flush(batch.ClientID)
		return batch, true
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
	return protocol.Batch{}, false
}

func (m *avgPerPaymentFormat) flush(clientID string) {
	formats, ok := m.state[clientID]
	if !ok {
		return
	}
	delete(m.state, clientID)

	partitioned := make(map[int][]avgPerFormatResult)
	for format, s := range formats {
		if s.count == 0 {
			continue
		}
		p := worker.PartitionForKey(format, m.outputPartitions)
		partitioned[p] = append(partitioned[p], avgPerFormatResult{
			PaymentFormat: format,
			AvgAmount:     s.sum / float64(s.count),
		})
	}

	for partition, results := range partitioned {
		routingKey := worker.RoutingKey(m.outputKeyPrefix, partition)
		raw, err := json.Marshal(results)
		if err != nil {
			log.Printf("[avg_per_payment_format] marshal results partition=%d: %v", partition, err)
			continue
		}
		out := protocol.Batch{
			Type:     protocol.BatchTypeData,
			ClientID: clientID,
			DataType: "avg_per_format",
			Records:  raw,
		}
		data, err := json.Marshal(out)
		if err != nil {
			log.Printf("[avg_per_payment_format] marshal batch partition=%d: %v", partition, err)
			continue
		}
		if err := m.outputMW.SendWithKey(middleware.Message{Body: string(data)}, routingKey); err != nil {
			log.Printf("[avg_per_payment_format] send results partition=%d: %v", partition, err)
		}
	}

	eofBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: "avg_per_format",
	}
	eofData, err := json.Marshal(eofBatch)
	if err != nil {
		log.Printf("[avg_per_payment_format] marshal EOF: %v", err)
		return
	}
	for i := 0; i < m.outputPartitions; i++ {
		routingKey := worker.RoutingKey(m.outputKeyPrefix, i)
		if err := m.outputMW.SendWithKey(middleware.Message{Body: string(eofData)}, routingKey); err != nil {
			log.Printf("[avg_per_payment_format] send EOF partition=%d: %v", i, err)
		}
	}
}
