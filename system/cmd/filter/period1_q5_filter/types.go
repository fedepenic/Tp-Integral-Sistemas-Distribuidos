package main

const (
	dateLayout  = "2006-01-02"
	periodStart = "2022-09-01"
	periodEnd   = "2022-09-05"
)

type filterInput struct {
	Timestamp       string  `json:"timestamp"`
	AmountPaid      float64 `json:"amount_paid"`
	PaymentCurrency string  `json:"payment_currency"`
	PaymentFormat   string  `json:"payment_format"`
	FromBank        string  `json:"from_bank"`
	FromAccount     string  `json:"from_account"`
	ToBank          string  `json:"to_bank"`
	ToAccount       string  `json:"to_account"`
}

type filterOutput struct {
	Timestamp       string  `json:"timestamp"`
	AmountPaid      float64 `json:"amount_paid"`
	PaymentCurrency string  `json:"payment_currency"`
	PaymentFormat   string  `json:"payment_format"`
	FromBank        string  `json:"from_bank"`
	FromAccount     string  `json:"from_account"`
	ToBank          string  `json:"to_bank"`
	ToAccount       string  `json:"to_account"`
}
