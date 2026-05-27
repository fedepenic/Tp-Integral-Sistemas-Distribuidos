package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
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

// bitcoinRates stores daily BTC→USD rates for the analysis window.
// Source: investing.com (same values used by the notebook).
var bitcoinRates = map[string]float64{
	"2022-09-01": 19793.1,
	"2022-09-02": 199999.0,
	"2022-09-03": 19831.4,
	"2022-09-04": 19952.7,
	"2022-09-05": 20126.1,
}

type frankfurterRate struct {
	Rate float64 `json:"rate"`
}

type rateKey struct {
	code string
	date string
}

type converter struct {
	cache      map[rateKey]float64
	httpClient *http.Client
}

func newConverter() *converter {
	return &converter{
		cache:      make(map[rateKey]float64),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// txnDate extracts the date portion of a transaction timestamp
// (e.g. "2022/09/07 13:59") and returns it in YYYY-MM-DD format.
func txnDate(timestamp string) string {
	date := strings.SplitN(timestamp, " ", 2)[0]
	return strings.ReplaceAll(date, "/", "-")
}

func (c *converter) rateToUSD(currency, date string) (float64, error) {
	if currency == "US Dollar" {
		return 1.0, nil
	}
	if currency == "Bitcoin" {
		if rate, ok := bitcoinRates[date]; ok {
			return rate, nil
		}
		return 0, fmt.Errorf("no Bitcoin rate for date %s", date)
	}
	code, ok := currencyNameToCode[currency]
	if !ok {
		return 0, fmt.Errorf("unknown currency: %s", currency)
	}
	key := rateKey{code: code, date: date}
	if rate, ok := c.cache[key]; ok {
		return rate, nil
	}

	url := fmt.Sprintf("%s/rates?base=%s&quotes=USD&date=%s", frankfurterBaseURL, code, date)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return 0, fmt.Errorf("fetch rate for %s on %s: %w", currency, date, err)
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
		return 0, fmt.Errorf("empty response for currency %s on %s", currency, date)
	}

	c.cache[key] = rates[0].Rate
	return rates[0].Rate, nil
}

func (c *converter) convertBatch(batch protocol.Batch) protocol.Batch {
	out := protocol.Batch{Type: batch.Type, ClientID: batch.ClientID}
	for _, txn := range batch.Transactions {
		rate, err := c.rateToUSD(txn.PaymentCurrency, txnDate(txn.Timestamp))
		if err != nil {
			log.Printf("[currency_converter] currency=%q date=%s: %v — skipping", txn.PaymentCurrency, txn.Timestamp, err)
			continue
		}
		txn.AmountPaid *= rate
		txn.PaymentCurrency = "US Dollar"
		out.Transactions = append(out.Transactions, txn)
	}
	return out
}

func main() {
	svc := node.New("currency_converter")
	conn := svc.Conn()

	inputQueue  := config.EnvOrDefault("INPUT_QUEUE", "wireach_txn")
	outputQueue := config.EnvOrDefault("OUTPUT_QUEUE", "converted_usd")

	inputMW, err := middleware.CreateQueueMiddleware(inputQueue, conn)
	if err != nil {
		log.Fatalf("[currency_converter] connect to input queue: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.CreateQueueMiddleware(outputQueue, conn)
	if err != nil {
		log.Fatalf("[currency_converter] connect to output queue: %v", err)
	}
	defer outputMW.Close()

	conv := newConverter()

	svc.Run(inputMW, outputMW, func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeTransactions {
			return conv.convertBatch(batch), true
		}
		return batch, true
	})
}
