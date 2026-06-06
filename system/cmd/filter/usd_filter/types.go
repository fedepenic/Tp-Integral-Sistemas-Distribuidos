package main

const targetCurrency = "US Dollar"

type filterInput struct {
	PaymentCurrency string  `json:"payment_currency"`
	FromBank        string  `json:"from_bank"`
	FromAccount     string  `json:"from_account"`
	AmountPaid      float64 `json:"amount_paid"`
}

type filterOutput struct {
	FromBank    string  `json:"from_bank"`
	FromAccount string  `json:"from_account"`
	AmountPaid  float64 `json:"amount_paid"`
}
