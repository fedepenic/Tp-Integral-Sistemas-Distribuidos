package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// node holds shared state between the data consumer goroutine and the EOF
// receiver goroutine, following the same coordination pattern used by the
// currency converter.
type node struct {
	producer     middleware.Middleware
	eofBroadcast middleware.Middleware
	mu           sync.Mutex
	cond         *sync.Cond
	// globalPending counts messages dequeued but not yet deserialized (clientID unknown).
	globalPending int
	// clientPending counts messages currently being processed per client.
	clientPending map[string]int
}

func newNode(producer, eofBroadcast middleware.Middleware) *node {
	n := &node{
		producer:      producer,
		eofBroadcast:  eofBroadcast,
		clientPending: make(map[string]int),
	}
	n.cond = sync.NewCond(&n.mu)
	return n
}

// cleanTransactions drops incomplete or malformed transactions and strips the
// IsLaundering label, which is not used by any downstream query.
//
// Drop criteria:
//   - Empty Timestamp (date-based queries require it)
//   - Empty FromAccount or ToAccount (account graph queries require both endpoints)
//   - Empty PaymentCurrency or ReceivingCurrency (currency conversion requires them)
//   - AmountPaid <= 0 or AmountReceived <= 0 (zero-amount records carry no signal)
//   - Empty PaymentFormat (queries Q3 and Q5 partition by payment format)
func cleanTransactions(txns []protocol.Transaction) []protocol.Transaction {
	out := make([]protocol.Transaction, 0, len(txns))
	for _, t := range txns {
		if t.Timestamp == "" {
			continue
		}
		if t.FromAccount == "" || t.ToAccount == "" {
			continue
		}
		if t.PaymentCurrency == "" || t.ReceivingCurrency == "" {
			continue
		}
		if t.AmountPaid <= 0 || t.AmountReceived <= 0 {
			continue
		}
		if t.PaymentFormat == "" {
			continue
		}
		t.IsLaundering = 0
		out = append(out, t)
	}
	return out
}

// cleanAccounts drops account records that are missing identifying fields.
//
// Drop criteria:
//   - Empty BankName, BankID, or AccountNumber (needed to resolve bank/account references)
func cleanAccounts(accounts []protocol.Account) []protocol.Account {
	out := make([]protocol.Account, 0, len(accounts))
	for _, a := range accounts {
		if a.BankName == "" || a.BankID == "" || a.AccountNumber == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func cleanBatch(batch protocol.Batch) protocol.Batch {
	out := protocol.Batch{
		Type:     batch.Type,
		ClientID: batch.ClientID,
	}
	switch batch.Type {
	case protocol.BatchTypeTransactions:
		out.Transactions = cleanTransactions(batch.Transactions)
	case protocol.BatchTypeAccounts:
		out.Accounts = cleanAccounts(batch.Accounts)
	}
	return out
}

func (n *node) handleData(msg middleware.Message, ack func(), nack func()) {
	// Increment globalPending before deserialization so the EOF handler cannot
	// slip through while the clientID of this message is still unknown.
	n.mu.Lock()
	n.globalPending++
	n.mu.Unlock()

	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("unmarshal batch: %v — discarding", err)
		n.mu.Lock()
		n.globalPending--
		n.cond.Broadcast()
		n.mu.Unlock()
		ack()
		return
	}

	if batch.Type == protocol.BatchTypeEOF {
		n.mu.Lock()
		n.globalPending--
		n.cond.Broadcast()
		n.mu.Unlock()
		if err := n.eofBroadcast.Send(msg); err != nil {
			log.Printf("broadcast EOF: %v", err)
			nack()
			return
		}
		ack()
		return
	}

	// Move the message from globalPending to clientPending atomically so it is
	// never invisible to both counters at the same time.
	n.mu.Lock()
	n.globalPending--
	n.clientPending[batch.ClientID]++
	n.mu.Unlock()

	cleaned := cleanBatch(batch)

	data, err := json.Marshal(cleaned)
	if err != nil {
		log.Printf("marshal cleaned batch: %v", err)
		n.mu.Lock()
		n.clientPending[batch.ClientID]--
		n.cond.Broadcast()
		n.mu.Unlock()
		nack()
		return
	}

	if err := n.producer.Send(middleware.Message{Body: string(data)}); err != nil {
		log.Printf("send to output exchange: %v", err)
		n.mu.Lock()
		n.clientPending[batch.ClientID]--
		n.cond.Broadcast()
		n.mu.Unlock()
		nack()
		return
	}

	n.mu.Lock()
	n.clientPending[batch.ClientID]--
	n.cond.Broadcast()
	n.mu.Unlock()
	ack()
}

// handleEOF is the callback for EOF signals arriving on the broadcast exchange.
// It blocks until all in-flight data messages for the same client are drained,
// then forwards the EOF to the output exchange so downstream nodes receive it.
func (n *node) handleEOF(msg middleware.Message, ack func(), nack func()) {
	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("unmarshal EOF broadcast: %v — discarding", err)
		ack()
		return
	}

	n.mu.Lock()
	for n.globalPending > 0 || n.clientPending[batch.ClientID] > 0 {
		n.cond.Wait()
	}
	n.mu.Unlock()

	if err := n.producer.Send(msg); err != nil {
		log.Printf("send EOF to output exchange: %v", err)
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
	inputQueue := envOrDefault("INPUT_QUEUE", "raw_transactions")
	outputQueue := envOrDefault("OUTPUT_QUEUE", "q1_results")
	host := envOrDefault("RABBITMQ_HOST", "rabbitmq")
	portStr := envOrDefault("RABBITMQ_PORT", "5672")
	instanceIDStr := envOrDefault("INSTANCE_ID", "1")
	instanceTotalStr := envOrDefault("INSTANCE_TOTAL", "1")
	eofExchange := envOrDefault("EOF_EXCHANGE", "cleaner_eof")

	port, err := strconv.Atoi(portStr)
	if err != nil {
		log.Fatalf("invalid RABBITMQ_PORT %q: %v", portStr, err)
	}
	instanceID, err := strconv.Atoi(instanceIDStr)
	if err != nil {
		log.Fatalf("invalid INSTANCE_ID %q: %v", instanceIDStr, err)
	}
	instanceTotal, err := strconv.Atoi(instanceTotalStr)
	if err != nil {
		log.Fatalf("invalid INSTANCE_TOTAL %q: %v", instanceTotalStr, err)
	}

	connSettings := middleware.ConnSettings{Hostname: host, Port: port}

	allEOFKeys := make([]string, instanceTotal)
	for i := 0; i < instanceTotal; i++ {
		allEOFKeys[i] = fmt.Sprintf("cleaner_%d", i+1)
	}
	ownKey := fmt.Sprintf("cleaner_%d", instanceID)

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

	// eofBroadcast publishes to ALL instance keys so every sibling receives the EOF.
	eofBroadcast, err := middleware.CreateExchangeMiddleware(eofExchange, allEOFKeys, connSettings)
	if err != nil {
		log.Fatalf("connect to EOF broadcast exchange: %v", err)
	}
	defer eofBroadcast.Close()

	// eofReceiver subscribes only to this instance's own routing key.
	eofReceiver, err := middleware.CreateExchangeMiddleware(eofExchange, []string{ownKey}, connSettings)
	if err != nil {
		log.Fatalf("connect to EOF receiver exchange: %v", err)
	}
	defer eofReceiver.Close()

	n := newNode(producer, eofBroadcast)

	log.Printf("cleaner %d/%d started: %s -> %s", instanceID, instanceTotal, inputQueue, outputQueue)

	go func() {
		if err := eofReceiver.StartConsuming(n.handleEOF); err != nil {
			log.Fatalf("consuming from EOF exchange: %v", err)
		}
	}()

	if err := consumer.StartConsuming(n.handleData); err != nil {
		log.Fatalf("consuming from %s: %v", inputQueue, err)
	}
}
