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
// batches from its dedicated input queue, stamps each with the query identifier,
// and forwards them to the shared report queue consumed by the client.
//
// Each query has exactly one sink instance — no EOF coordination between
// siblings is required.
type sink struct {
	producer middleware.Middleware
	queryID  string
}

func (s *sink) handle(msg middleware.Message, ack func(), nack func()) {
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

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	inputQueue  := envOrDefault("INPUT_QUEUE", "q1_results")
	outputQueue := envOrDefault("OUTPUT_QUEUE", "reports")
	queryID     := envOrDefault("QUERY_ID", "q1")
	host        := envOrDefault("RABBITMQ_HOST", "rabbitmq")
	portStr     := envOrDefault("RABBITMQ_PORT", "5672")

	port, err := strconv.Atoi(portStr)
	if err != nil {
		log.Fatalf("invalid RABBITMQ_PORT %q: %v", portStr, err)
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

	s := &sink{producer: producer, queryID: queryID}

	log.Printf("sink %s started: %s -> %s", queryID, inputQueue, outputQueue)

	if err := consumer.StartConsuming(s.handle); err != nil {
		log.Fatalf("consuming from %s: %v", inputQueue, err)
	}
}
