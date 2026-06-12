package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// usdToCurrencyRates stores daily "1 USD = X currency" rates for the analysis
// window, hardcoded with the exact same values used by the notebook
// (scripts/money-laundering-analysis.ipynb). Converting an amount expressed in
// one of these currencies to USD means dividing by the rate.
var usdToCurrencyRates = map[string]map[string]float64{
	"2022-09-01": {
		"Australian Dollar": 1.4644,
		"Brazil Real":       5.1805,
		"Canadian Dollar":   1.314,
		"Swiss Franc":       0.97999,
		"Yuan":              6.9,
		"Euro":              1.0002,
		"UK Pound":          0.86272,
		"Shekel":            3.3535,
		"Rupee":             79.543,
		"Yen":               139.34,
		"Mexican Peso":      20.189,
		"Ruble":             60.367,
		"Saudi Riyal":       3.75,
		"US Dollar":         1.0,
	},
	"2022-09-02": {
		"Australian Dollar": 1.4691,
		"Brazil Real":       5.2035,
		"Canadian Dollar":   1.3141,
		"Swiss Franc":       0.98175,
		"Yuan":              6.9035,
		"Euro":              1.0011,
		"UK Pound":          0.86468,
		"Shekel":            3.3755,
		"Rupee":             79.719,
		"Yen":               140.11,
		"Mexican Peso":      20.085,
		"Ruble":             60.427,
		"Saudi Riyal":       3.75,
		"US Dollar":         1.0,
	},
	"2022-09-03": {
		"Australian Dollar": 1.4691,
		"Brazil Real":       5.2056,
		"Canadian Dollar":   1.3138,
		"Swiss Franc":       0.98207,
		"Yuan":              6.9046,
		"Euro":              1.0013,
		"UK Pound":          0.86478,
		"Shekel":            3.3791,
		"Rupee":             79.75,
		"Yen":               140.17,
		"Mexican Peso":      20.081,
		"Ruble":             60.471,
		"Saudi Riyal":       3.75,
		"US Dollar":         1.0,
	},
	"2022-09-04": {
		"Australian Dollar": 1.4695,
		"Brazil Real":       5.2082,
		"Canadian Dollar":   1.3139,
		"Swiss Franc":       0.98219,
		"Yuan":              6.9047,
		"Euro":              1.0013,
		"UK Pound":          0.8649,
		"Shekel":            3.3815,
		"Rupee":             79.754,
		"Yen":               140.22,
		"Mexican Peso":      20.084,
		"Ruble":             60.461,
		"Saudi Riyal":       3.75,
		"US Dollar":         1.0,
	},
	"2022-09-05": {
		"Australian Dollar": 1.4722,
		"Brazil Real":       5.1786,
		"Canadian Dollar":   1.3142,
		"Swiss Franc":       0.98273,
		"Yuan":              6.9216,
		"Euro":              1.0068,
		"UK Pound":          0.86813,
		"Shekel":            3.4006,
		"Rupee":             79.816,
		"Yen":               140.49,
		"Mexican Peso":      20.018,
		"Ruble":             60.737,
		"Saudi Riyal":       3.75,
		"US Dollar":         1.0,
	},
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

// currencyToISO maps the currency names used in the dataset to ISO 4217 codes
// understood by the Frankfurter API.
var currencyToISO = map[string]string{
	"Australian Dollar": "AUD",
	"Brazil Real":       "BRL",
	"Canadian Dollar":   "CAD",
	"Swiss Franc":       "CHF",
	"Yuan":              "CNY",
	"Euro":              "EUR",
	"UK Pound":          "GBP",
	"Shekel":            "ILS",
	"Rupee":             "INR",
	"Yen":               "JPY",
	"Mexican Peso":      "MXN",
	"Ruble":             "RUB",
	"Saudi Riyal":       "SAR",
	"US Dollar":         "USD",
}

type converter struct {
	useHardcoded bool
	mu           sync.Mutex
	rateCache    map[string]map[string]float64 // date -> (ISO code -> USD-per-unit rate)
}

func newConverter(useHardcoded bool) *converter {
	return &converter{
		useHardcoded: useHardcoded,
		rateCache:    make(map[string]map[string]float64),
	}
}

// txnDate extracts the date portion of a transaction timestamp
// (e.g. "2022/09/07 13:59") and returns it in YYYY-MM-DD format.
func txnDate(timestamp string) string {
	date := strings.SplitN(timestamp, " ", 2)[0]
	return strings.ReplaceAll(date, "/", "-")
}

func (c *converter) rateToUSD(currency, date string) (float64, error) {
	if c.useHardcoded {
		return c.rateToUSDHardcoded(currency, date)
	}
	return c.rateToUSDFromAPI(currency, date)
}

func (c *converter) rateToUSDHardcoded(currency, date string) (float64, error) {
	if currency == "Bitcoin" {
		if rate, ok := bitcoinRates[date]; ok {
			return rate, nil
		}
		return 0, fmt.Errorf("no Bitcoin rate for date %s", date)
	}
	rates, ok := usdToCurrencyRates[date]
	if !ok {
		return 0, fmt.Errorf("no rates for date %s", date)
	}
	// Rates are "1 USD = X currency", so to convert an amount in this
	// currency to USD we divide by the rate (multiply by its inverse).
	usdToCurrency, ok := rates[currency]
	if !ok {
		return 0, fmt.Errorf("unknown currency: %s", currency)
	}
	return 1.0 / usdToCurrency, nil
}

type frankfurterResp struct {
	Rates map[string]float64 `json:"rates"`
}

func fetchForexRates(date string) (map[string]float64, error) {
	url := fmt.Sprintf("https://api.frankfurter.app/%s?base=USD", date)
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("fetch forex rates: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read forex response: %w", err)
	}
	var result frankfurterResp
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse forex response: %w", err)
	}
	return result.Rates, nil
}

func (c *converter) rateToUSDFromAPI(currency, date string) (float64, error) {
	// Bitcoin is always looked up from the hardcoded map regardless of mode.
	if currency == "Bitcoin" {
		return c.rateToUSDHardcoded(currency, date)
	}
	if currency == "US Dollar" {
		return 1.0, nil
	}
	iso, ok := currencyToISO[currency]
	if !ok {
		return 0, fmt.Errorf("unknown currency: %s", currency)
	}
	c.mu.Lock()
	rates, cached := c.rateCache[date]
	c.mu.Unlock()
	if !cached {
		var err error
		rates, err = fetchForexRates(date)
		if err != nil {
			return 0, err
		}
		c.mu.Lock()
		c.rateCache[date] = rates
		c.mu.Unlock()
	}
	usdToCurrency, ok := rates[iso]
	if !ok {
		return 0, fmt.Errorf("no rate for %s (%s) on %s", currency, iso, date)
	}
	return 1.0 / usdToCurrency, nil
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

func newProcess(useHardcoded bool) node.ProcessFunc {
	conv := newConverter(useHardcoded)
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeTransactions {
			return conv.convertBatch(batch), true
		}
		return protocol.Batch{}, false
	}
}
