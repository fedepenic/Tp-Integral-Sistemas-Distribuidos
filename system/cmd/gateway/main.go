package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	port         := envOrDefault("GATEWAY_PORT", "8080")
	host         := envOrDefault("RABBITMQ_HOST", "rabbitmq")
	portStr      := envOrDefault("RABBITMQ_PORT", "5672")
	outputQueue  := envOrDefault("OUTPUT_QUEUE", "raw_transactions")
	reportsQueue := envOrDefault("REPORTS_QUEUE", "reports")
	outputDir    := envOrDefault("OUTPUT_DIR", "/output")

	rabbitPort, err := strconv.Atoi(portStr)
	if err != nil {
		log.Fatalf("invalid RABBITMQ_PORT %q: %v", portStr, err)
	}

	connSettings := middleware.ConnSettings{Hostname: host, Port: rabbitPort}

	producer, err := middleware.CreateQueueMiddleware(outputQueue, connSettings)
	if err != nil {
		log.Fatalf("connect to output queue %q: %v", outputQueue, err)
	}
	defer producer.Close()

	reportsConsumer, err := middleware.CreateQueueMiddleware(reportsQueue, connSettings)
	if err != nil {
		log.Fatalf("connect to reports queue %q: %v", reportsQueue, err)
	}
	defer reportsConsumer.Close()

	go func() {
		r := newReporter(outputDir)
		if err := reportsConsumer.StartConsuming(r.handle); err != nil {
			log.Fatalf("consuming from reports queue: %v", err)
		}
	}()

	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("listen on port %s: %v", port, err)
	}
	defer ln.Close()
	log.Printf("gateway listening on :%s", port)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		log.Printf("client connected: %s", conn.RemoteAddr())
		go handleClient(conn, producer)
	}
}

func handleClient(conn net.Conn, producer middleware.Middleware) {
	defer conn.Close()

	clientID := "unknown"
	totalAccounts := 0
	totalTransactions := 0

	for {
		batch, err := protocol.Receive(conn)
		if err != nil {
			if err == io.EOF {
				log.Printf("client %s disconnected unexpectedly", clientID)
			} else {
				log.Printf("client %s receive error: %v", clientID, err)
			}
			return
		}

		if batch.ClientID != "" {
			clientID = batch.ClientID
		}

		switch batch.Type {
		case protocol.BatchTypeAccounts:
			totalAccounts += len(batch.Accounts)
			log.Printf("[client %s] accounts batch of %d (total: %d)", clientID, len(batch.Accounts), totalAccounts)
			if err := publish(producer, batch); err != nil {
				log.Printf("[client %s] publish accounts: %v", clientID, err)
			}

		case protocol.BatchTypeTransactions:
			totalTransactions += len(batch.Transactions)
			log.Printf("[client %s] transactions batch of %d (total: %d)", clientID, len(batch.Transactions), totalTransactions)
			if err := publish(producer, batch); err != nil {
				log.Printf("[client %s] publish transactions: %v", clientID, err)
			}

		case protocol.BatchTypeEOF:
			log.Printf("[client %s] finished — accounts=%d transactions=%d", clientID, totalAccounts, totalTransactions)
			if err := publish(producer, batch); err != nil {
				log.Printf("[client %s] publish EOF: %v", clientID, err)
			}
			if err := protocol.Send(conn, protocol.Batch{Type: protocol.BatchTypeACK}); err != nil {
				log.Printf("client %s send ack: %v", clientID, err)
			}
			return
		}
	}
}

func publish(producer middleware.Middleware, batch protocol.Batch) error {
	data, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	return producer.Send(middleware.Message{Body: string(data)})
}
