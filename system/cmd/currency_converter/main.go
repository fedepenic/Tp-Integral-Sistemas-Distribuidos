package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
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
var staticRates = map[string]float64{
	"Bitcoin": 78.33,
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

	conv := newConverter()

	log.Printf("currency converter started: %s -> %s", inputQueue, outputQueue)

	if err := consumer.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
		var batch protocol.Batch
		if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
			log.Printf("unmarshal batch: %v — discarding", err)
			ack()
			return
		}

		var outBatch protocol.Batch
		if batch.Type == protocol.BatchTypeTransactions {
			outBatch = conv.convertBatch(batch)
		} else {
			outBatch = batch
		}

		data, err := json.Marshal(outBatch)
		if err != nil {
			log.Printf("marshal output batch: %v", err)
			nack()
			return
		}

		if err := producer.Send(middleware.Message{Body: string(data)}); err != nil {
			log.Printf("send to output queue: %v", err)
			nack()
			return
		}

		ack()
	}); err != nil {
		log.Fatalf("consuming from %s: %v", inputQueue, err)
	}
}
