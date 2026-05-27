package main

import (
	"encoding/json"
	"log"
	"os"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// sink is the terminal node for a single query pipeline. It reads result
// batches AND EOFs from the same dedicated input queue, stamps each with the
// query identifier, and forwards them to the shared report queue consumed by
// the client.
//
// Reading data and EOFs from the same queue preserves their FIFO order, so the
// EOF is always written to the report queue AFTER all the data that preceded
// it. The sink counts EOFs per client and only forwards a single EOF to the
// report queue once all upstream instances have signalled completion
// (count == upstreamTotal).
type sink struct {
	producer      middleware.Middleware
	queryID       string
	upstreamTotal int
	eofCount      map[string]int
}

func (s *sink) handle(msg middleware.Message, ack func(), nack func()) {
	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("sink %s: unmarshal: %v — discarding", s.queryID, err)
		ack()
		return
	}

	clientID := batch.ClientID
	if clientID == "" {
		clientID = "default"
	}

	if batch.Type == protocol.BatchTypeEOF {
		s.eofCount[clientID]++
		count := s.eofCount[clientID]
		log.Printf("sink %s: EOF %d/%d for client %s", s.queryID, count, s.upstreamTotal, clientID)
		if count < s.upstreamTotal {
			ack()
			return
		}
		delete(s.eofCount, clientID)
	}

	batch.QueryID = s.queryID

	data, err := json.Marshal(batch)
	if err != nil {
		log.Printf("sink %s: marshal: %v", s.queryID, err)
		nack()
		return
	}
	if err := s.producer.Send(middleware.Message{Body: string(data)}); err != nil {
		log.Printf("sink %s: send: %v", s.queryID, err)
		nack()
		return
	}

	if batch.Type != protocol.BatchTypeEOF {
		log.Printf("sink %s: forwarded batch client=%s txns=%d", s.queryID, batch.ClientID, len(batch.Records))
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
	inputQueue := envOrDefault("INPUT_QUEUE", "q1_results")
	outputQueue := envOrDefault("OUTPUT_QUEUE", "reports")
	queryID := envOrDefault("QUERY_ID", "q1")
	host := envOrDefault("RABBITMQ_HOST", "rabbitmq")
	portStr := envOrDefault("RABBITMQ_PORT", "5672")
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

	if err := consumer.StartConsuming(s.handle); err != nil {
		log.Fatalf("consuming from %s: %v", inputQueue, err)
	}
}
