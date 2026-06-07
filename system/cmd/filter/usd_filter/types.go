package main

const targetCurrency = "US Dollar"

type filterInput struct {
	PaymentCurrency string  `json:"payment_currency"`
	Timestamp       string  `json:"timestamp"`
	PaymentFormat   string  `json:"payment_format"`
	FromBank        string  `json:"from_bank"`
	FromAccount     string  `json:"from_account"`
	ToBank          string  `json:"to_bank"`
	ToAccount       string  `json:"to_account"`
	AmountPaid      float64 `json:"amount_paid"`
}

type filterOutput struct {
	Timestamp     string  `json:"timestamp"`
	PaymentFormat string  `json:"payment_format"`
	FromBank      string  `json:"from_bank"`
	FromAccount   string  `json:"from_account"`
	ToBank        string  `json:"to_bank"`
	ToAccount     string  `json:"to_account"`
	AmountPaid    float64 `json:"amount_paid"`
}
