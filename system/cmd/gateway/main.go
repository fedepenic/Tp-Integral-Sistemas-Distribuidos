package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/health"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

const defaultClientDisconnectTimeout time.Duration = 60 * time.Second

type clientStatus struct {
	timer     *time.Timer
	completed bool
	abandoned bool
}

type clientTracker struct {
	mu                      sync.Mutex
	clients                 map[string]*clientStatus
	reporter                *reporter
	clientDisconnectTimeout time.Duration
}

func newClientTracker(reporter *reporter, clientDisconnectTimeout time.Duration) *clientTracker {
	return &clientTracker{
		clients:                 make(map[string]*clientStatus),
		reporter:                reporter,
		clientDisconnectTimeout: clientDisconnectTimeout,
	}
}

func (t *clientTracker) markActive(clientID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	status := t.statusFor(clientID)
	if status.abandoned {
		return false
	}
	if status.timer != nil {
		status.timer.Stop()
		status.timer = nil
		log.Printf("client %s reconnected before abandonment timeout", clientID)
	}
	return true
}

func (t *clientTracker) markCompleted(clientID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	status := t.statusFor(clientID)
	status.completed = true
	if status.timer != nil {
		status.timer.Stop()
		status.timer = nil
	}
}

func (t *clientTracker) markDisconnected(clientID string) {
	if clientID == "" || clientID == "unknown" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	status := t.statusFor(clientID)
	if status.completed || status.abandoned {
		return
	}
	if status.timer != nil {
		status.timer.Stop()
	}
	status.timer = time.AfterFunc(t.clientDisconnectTimeout, func() {
		t.markAbandoned(clientID)
	})
	log.Printf("client %s disconnected before EOF; waiting %s before marking as abandoned", clientID, t.clientDisconnectTimeout)
}

func (t *clientTracker) markAbandoned(clientID string) {
	t.mu.Lock()
	status := t.statusFor(clientID)
	if status.completed || status.abandoned {
		t.mu.Unlock()
		return
	}
	status.abandoned = true
	status.timer = nil
	t.mu.Unlock()

	log.Printf("client %s abandoned after %s without reconnecting", clientID, t.clientDisconnectTimeout)
	t.reporter.markClientDisconnected(clientID)
}

func (t *clientTracker) statusFor(clientID string) *clientStatus {
	status, ok := t.clients[clientID]
	if !ok {
		status = &clientStatus{}
		t.clients[clientID] = status
	}
	return status
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDurationOrDefault(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("invalid %s %q, using default %s", key, v, def)
	}
	return def
}

func main() {
	health.StartIfEnabled()

	port := envOrDefault("GATEWAY_PORT", "8080")
	host := envOrDefault("RABBITMQ_HOST", "rabbitmq")
	portStr := envOrDefault("RABBITMQ_PORT", "5672")
	outputQueue := envOrDefault("OUTPUT_QUEUE", "raw_transactions")
	reportsQueue := envOrDefault("REPORTS_QUEUE", "reports")
	outputDir := envOrDefault("OUTPUT_DIR", "/output")
	eofStorePath := envOrDefault("GATEWAY_EOF_STORE_FILE", outputDir+"/gateway_eofs.log")
	clientDisconnectTimeout := envDurationOrDefault("CLIENT_DISCONNECT_TIMEOUT", defaultClientDisconnectTimeout)

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

	r := newReporter(outputDir)
	tracker := newClientTracker(r, clientDisconnectTimeout)
	eofs, err := newEOFStore(eofStorePath)
	if err != nil {
		log.Fatalf("load EOF store: %v", err)
	}

	go func() {
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
		go handleClient(conn, producer, tracker, eofs)
	}
}

func handleClient(conn net.Conn, producer middleware.Middleware, tracker *clientTracker, eofs *eofStore) {
	defer conn.Close()

	clientID := "unknown"
	totalAccounts := 0
	totalTransactions := 0
	completed := false

	defer func() {
		if !completed {
			tracker.markDisconnected(clientID)
		}
	}()

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
			if !tracker.markActive(clientID) {
				log.Printf("client %s attempted to reconnect after being abandoned; closing connection", clientID)
				return
			}
		}

		switch batch.Type {
		case protocol.BatchTypeAccounts:
			totalAccounts += len(batch.Accounts)
			log.Printf("[client %s] accounts batch of %d (total: %d)", clientID, len(batch.Accounts), totalAccounts)
			if err := publish(producer, batch); err != nil {
				log.Printf("[client %s] publish accounts: %v", clientID, err)
				return
			}
			if err := sendACK(conn, batch); err != nil {
				log.Printf("client %s send accounts ack: %v", clientID, err)
				return
			}

		case protocol.BatchTypeTransactions:
			totalTransactions += len(batch.Transactions)
			log.Printf("[client %s] transactions batch of %d (total: %d)", clientID, len(batch.Transactions), totalTransactions)
			if err := publish(producer, batch); err != nil {
				log.Printf("[client %s] publish transactions: %v", clientID, err)
				return
			}
			if err := sendACK(conn, batch); err != nil {
				log.Printf("client %s send transactions ack: %v", clientID, err)
				return
			}

		case protocol.BatchTypeEOF:
			log.Printf("[client %s] finished — accounts=%d transactions=%d", clientID, totalAccounts, totalTransactions)
			eofID := batch.BatchID
			if eofID == "" {
				eofID = "client:" + clientID + ":eof"
			}
			published, err := eofs.withUnseen(eofID, func() error {
				return publish(producer, batch)
			})
			if err != nil {
				log.Printf("[client %s] publish/persist EOF: %v", clientID, err)
				return
			}
			if !published {
				log.Printf("[client %s] duplicate EOF %s — acking without republishing", clientID, eofID)
			}
			tracker.markCompleted(clientID)
			completed = true
			if err := sendACK(conn, batch); err != nil {
				log.Printf("client %s send EOF ack: %v", clientID, err)
			}
			return
		}
	}
}

func sendACK(conn net.Conn, batch protocol.Batch) error {
	return protocol.Send(conn, protocol.Batch{
		Type:    protocol.BatchTypeACK,
		BatchID: batch.BatchID,
	})
}

func publish(producer middleware.Middleware, batch protocol.Batch) error {
	data, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	return producer.Send(middleware.Message{Body: string(data)})
}
