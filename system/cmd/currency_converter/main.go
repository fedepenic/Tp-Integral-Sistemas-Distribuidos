package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

const frankfurterBaseURL = "https://api.frankfurter.dev/v2"

var currencyNameToCode = map[string]string{
	"Australian Dollar": "AUD",
	"Brazil Real":       "BRL",
	"Canadian Dollar":   "CAD",
	"Euro":              "EUR",
	"Mexican Peso":      "MXN",
	"Ruble":             "RUB",
	"Rupee":             "INR",
	"Saudi Riyal":       "SAR",
	"Shekel":            "ILS",
	"Swiss Franc":       "CHF",
	"UK Pound":          "GBP",
	"US Dollar":         "USD",
	"Yen":               "JPY",
	"Yuan":              "CNY",
}

// staticRates is a fallback for currencies not supported by the Frankfurter API (e.g. Bitcoin).
// Bitcoin rate as of 2026-05-20: 1 BTC = 77687.42 USD.
var staticRates = map[string]float64{
	"Bitcoin": 77687.42,
}

type frankfurterRate struct {
	Rate float64 `json:"rate"`
}

type converter struct {
	cache      map[string]float64
	httpClient *http.Client
}

func newConverter() *converter {
	return &converter{
		cache:      make(map[string]float64),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *converter) rateToUSD(currency string) (float64, error) {
	if currency == "US Dollar" {
		return 1.0, nil
	}
	if rate, ok := staticRates[currency]; ok {
		return rate, nil
	}
	code, ok := currencyNameToCode[currency]
	if !ok {
		return 0, fmt.Errorf("unknown currency: %s", currency)
	}
	if rate, ok := c.cache[code]; ok {
		return rate, nil
	}

	url := fmt.Sprintf("%s/rates?base=%s&quotes=USD", frankfurterBaseURL, code)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return 0, fmt.Errorf("fetch rate for %s: %w", currency, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read frankfurter response: %w", err)
	}

	var rates []frankfurterRate
	if err := json.Unmarshal(body, &rates); err != nil {
		return 0, fmt.Errorf("parse frankfurter response: %w", err)
	}
	if len(rates) == 0 {
		return 0, fmt.Errorf("empty response for currency %s", currency)
	}

	c.cache[code] = rates[0].Rate
	return rates[0].Rate, nil
}

func (c *converter) convertBatch(batch protocol.Batch) protocol.Batch {
	out := protocol.Batch{
		Type:     batch.Type,
		ClientID: batch.ClientID,
	}
	for _, txn := range batch.Transactions {
		rate, err := c.rateToUSD(txn.PaymentCurrency)
		if err != nil {
			log.Printf("conversion error for currency %q: %v — skipping transaction", txn.PaymentCurrency, err)
			continue
		}
		txn.AmountPaid = txn.AmountPaid * rate
		txn.PaymentCurrency = "US Dollar"
		out.Transactions = append(out.Transactions, txn)
	}
	return out
}

// node holds the shared state needed to coordinate between the data consumer
// goroutine and the EOF receiver goroutine.
type node struct {
	conv          *converter
	producer      middleware.Middleware
	eofBroadcast  middleware.Middleware
	mu            sync.Mutex
	cond          *sync.Cond
	globalPending int            // messages dequeued but not yet deserialized (clientID unknown)
	clientPending map[string]int // messages currently being processed per client
}

func newNode(conv *converter, producer, eofBroadcast middleware.Middleware) *node {
	n := &node{
		conv:          conv,
		producer:      producer,
		eofBroadcast:  eofBroadcast,
		clientPending: make(map[string]int),
	}
	n.cond = sync.NewCond(&n.mu)
	return n
}

// handleData is the callback for messages arriving on the data input queue.
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

	var outBatch protocol.Batch
	if batch.Type == protocol.BatchTypeTransactions {
		outBatch = n.conv.convertBatch(batch)
	} else {
		outBatch = batch
	}

	data, err := json.Marshal(outBatch)
	if err != nil {
		log.Printf("marshal output batch: %v", err)
		n.mu.Lock()
		n.clientPending[batch.ClientID]--
		n.cond.Broadcast()
		n.mu.Unlock()
		nack()
		return
	}

	if err := n.producer.Send(middleware.Message{Body: string(data)}); err != nil {
		log.Printf("send to output queue: %v", err)
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
// It blocks until all in-flight data messages for the same client are done.
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
		log.Printf("send EOF to output queue: %v", err)
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
	inputQueue := envOrDefault("INPUT_QUEUE", "wireach_txn")
	outputQueue := envOrDefault("OUTPUT_QUEUE", "converted_usd")
	host := envOrDefault("RABBITMQ_HOST", "rabbitmq")
	portStr := envOrDefault("RABBITMQ_PORT", "5672")
	instanceIDStr := envOrDefault("INSTANCE_ID", "1")
	instanceTotalStr := envOrDefault("INSTANCE_TOTAL", "1")
	eofExchange := envOrDefault("EOF_EXCHANGE", "currency_converter_eof")

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

	allKeys := make([]string, instanceTotal)
	for i := 0; i < instanceTotal; i++ {
		allKeys[i] = fmt.Sprintf("currency_converter_%d", i+1)
	}
	ownKey := fmt.Sprintf("currency_converter_%d", instanceID)

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
	eofBroadcast, err := middleware.CreateExchangeMiddleware(eofExchange, allKeys, connSettings)
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

	n := newNode(newConverter(), producer, eofBroadcast)

	log.Printf("currency converter %d/%d started: %s -> %s", instanceID, instanceTotal, inputQueue, outputQueue)

	go func() {
		if err := eofReceiver.StartConsuming(n.handleEOF); err != nil {
			log.Fatalf("consuming from EOF exchange: %v", err)
		}
	}()

	if err := consumer.StartConsuming(n.handleData); err != nil {
		log.Fatalf("consuming from %s: %v", inputQueue, err)
	}
}
