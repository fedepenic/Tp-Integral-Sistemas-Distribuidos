package main

import (
	"encoding/json"
	"log"
	"os"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// counter accumulates the number of transactions per client and, once all
// upstream EOFs for a client have arrived, emits a single BatchTypeCount
// batch followed by an EOF batch downstream.
//
// It is a single-instance node that handles multiple clients concurrently
// by tracking counts and EOF receipts in per-client maps.
//
// Variables de entorno:
//   INPUT_QUEUE        -- cola de entrada (q5_filtered)
//   OUTPUT_QUEUE       -- cola de salida  (q5_count)
//   UPSTREAM_INSTANCES -- EOFs esperados por cliente (N_USD_LOWER_THAN_ONE)
//   RABBITMQ_HOST, RABBITMQ_PORT

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	inputQueue    := envOrDefault("INPUT_QUEUE", "q5_filtered")
	outputQueue   := envOrDefault("OUTPUT_QUEUE", "q5_count")
	host          := envOrDefault("RABBITMQ_HOST", "rabbitmq")
	portStr       := envOrDefault("RABBITMQ_PORT", "5672")
	upstreamStr   := envOrDefault("UPSTREAM_INSTANCES", "1")

	port, err := strconv.Atoi(portStr)
	if err != nil {
		log.Fatalf("invalid RABBITMQ_PORT %q: %v", portStr, err)
	}
	upstreamCount, err := strconv.Atoi(upstreamStr)
	if err != nil {
		log.Fatalf("invalid UPSTREAM_INSTANCES %q: %v", upstreamStr, err)
	}

	conn := middleware.ConnSettings{Hostname: host, Port: port}

	consumer, err := middleware.CreateQueueMiddleware(inputQueue, conn)
	if err != nil {
		log.Fatalf("connect to input queue %q: %v", inputQueue, err)
	}
	defer consumer.Close()

	producer, err := middleware.CreateQueueMiddleware(outputQueue, conn)
	if err != nil {
		log.Fatalf("connect to output queue %q: %v", outputQueue, err)
	}
	defer producer.Close()

	log.Printf("[counter] started: %s -> %s (upstream=%d)", inputQueue, outputQueue, upstreamCount)

	eofCounts := make(map[string]int)
	txnCounts := make(map[string]int64)

	send := func(batch protocol.Batch) bool {
		data, err := json.Marshal(batch)
		if err != nil {
			log.Printf("[counter] marshal error: %v", err)
			return false
		}
		if err := producer.Send(middleware.Message{Body: string(data)}); err != nil {
			log.Printf("[counter] send error: %v", err)
			return false
		}
		return true
	}

	if err := consumer.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
		var batch protocol.Batch
		if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
			log.Printf("[counter] malformed message — discarding: %v", err)
			ack()
			return
		}

		if batch.Type == protocol.BatchTypeEOF {
			eofCounts[batch.ClientID]++
			count := eofCounts[batch.ClientID]
			log.Printf("[counter] EOF client=%s (%d/%d)", batch.ClientID, count, upstreamCount)

			if count < upstreamCount {
				ack()
				return
			}

			total := txnCounts[batch.ClientID]
			log.Printf("[counter] client=%s total=%d — emitting count batch", batch.ClientID, total)

			if !send(protocol.Batch{Type: protocol.BatchTypeCount, ClientID: batch.ClientID, Count: total}) {
				nack()
				return
			}
			if !send(protocol.Batch{Type: protocol.BatchTypeEOF, ClientID: batch.ClientID}) {
				nack()
				return
			}

			delete(eofCounts, batch.ClientID)
			delete(txnCounts, batch.ClientID)
			ack()
			return
		}

		if batch.Type == protocol.BatchTypeTransactions {
			txnCounts[batch.ClientID] += int64(len(batch.Transactions))
		}
		ack()
	}); err != nil {
		log.Fatalf("[counter] consumer error: %v", err)
	}
}
