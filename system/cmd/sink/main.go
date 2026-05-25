package main

import (
	"encoding/json"
	"log"
	"os"
	"strconv"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// sink is the terminal node for a single query pipeline. It reads result
// batches from its dedicated input queue, stamps each with the query identifier,
// and forwards them to the shared report queue consumed by the client.
//
// Data and EOFs arrive on separate channels: data on the input queue,
// EOFs on a dedicated exchange. The sink counts EOFs per client and only
// forwards a single EOF to the report queue once all upstream instances have
// signalled completion (count == upstreamTotal).
type sink struct {
	producer      middleware.Middleware
	queryID       string
	upstreamTotal int
	mu            sync.Mutex
	eofCount      map[string]int
}

func (s *sink) handleData(msg middleware.Message, ack func(), nack func()) {
	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("unmarshal batch: %v — discarding", err)
		ack()
		return
	}

	batch.QueryID = s.queryID

	data, err := json.Marshal(batch)
	if err != nil {
		log.Printf("marshal batch: %v", err)
		nack()
		return
	}

	if err := s.producer.Send(middleware.Message{Body: string(data)}); err != nil {
		log.Printf("send to report queue: %v", err)
		nack()
		return
	}

	ack()
}

func (s *sink) handleEOF(msg middleware.Message, ack func(), nack func()) {
	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("unmarshal EOF: %v — discarding", err)
		ack()
		return
	}

	s.mu.Lock()
	s.eofCount[batch.ClientID]++
	count := s.eofCount[batch.ClientID]
	s.mu.Unlock()

	log.Printf("sink %s: EOF %d/%d for client %s", s.queryID, count, s.upstreamTotal, batch.ClientID)

	if count < s.upstreamTotal {
		ack()
		return
	}

	s.mu.Lock()
	delete(s.eofCount, batch.ClientID)
	s.mu.Unlock()

	log.Printf("sink %s: all EOFs received for client %s — forwarding", s.queryID, batch.ClientID)

	batch.QueryID = s.queryID
	data, err := json.Marshal(batch)
	if err != nil {
		log.Printf("marshal EOF: %v", err)
		nack()
		return
	}

	if err := s.producer.Send(middleware.Message{Body: string(data)}); err != nil {
		log.Printf("send EOF to report queue: %v", err)
		nack()
		return
	}

	ack()
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	inputQueue  := envOrDefault("INPUT_QUEUE", "q1_results")
	eofExchange := envOrDefault("EOF_INPUT_EXCHANGE", "eof_q1_data")
	outputQueue := envOrDefault("OUTPUT_QUEUE", "reports")
	queryID     := envOrDefault("QUERY_ID", "q1")
	host        := envOrDefault("RABBITMQ_HOST", "rabbitmq")
	portStr     := envOrDefault("RABBITMQ_PORT", "5672")
	upstreamStr := envOrDefault("UPSTREAM_TOTAL", "1")

	port, err := strconv.Atoi(portStr)
	if err != nil {
		log.Fatalf("invalid RABBITMQ_PORT %q: %v", portStr, err)
	}

	upstreamTotal, err := strconv.Atoi(upstreamStr)
	if err != nil {
		log.Fatalf("invalid UPSTREAM_TOTAL %q: %v", upstreamStr, err)
	}

	connSettings := middleware.ConnSettings{Hostname: host, Port: port}

	consumer, err := middleware.CreateQueueMiddleware(inputQueue, connSettings)
	if err != nil {
		log.Fatalf("connect to input queue %q: %v", inputQueue, err)
	}
	defer consumer.Close()

	// eofConsumer subscribes to the dedicated EOF exchange. Each upstream instance
	// publishes one EOF here; handleEOF counts up to upstreamTotal before forwarding.
	eofConsumer, err := middleware.CreateExchangeMiddleware(eofExchange, []string{""}, connSettings)
	if err != nil {
		log.Fatalf("connect to EOF exchange %q: %v", eofExchange, err)
	}
	defer eofConsumer.Close()

	producer, err := middleware.CreateQueueMiddleware(outputQueue, connSettings)
	if err != nil {
		log.Fatalf("connect to output queue %q: %v", outputQueue, err)
	}
	defer producer.Close()

	s := &sink{
		producer:      producer,
		queryID:       queryID,
		upstreamTotal: upstreamTotal,
		eofCount:      make(map[string]int),
	}

	log.Printf("sink %s started: %s -> %s (upstream=%d)", queryID, inputQueue, outputQueue, upstreamTotal)

	go func() {
		if err := eofConsumer.StartConsuming(s.handleEOF); err != nil {
			log.Fatalf("consuming from EOF exchange %q: %v", eofExchange, err)
		}
	}()

	if err := consumer.StartConsuming(s.handleData); err != nil {
		log.Fatalf("consuming from %s: %v", inputQueue, err)
	}
}
