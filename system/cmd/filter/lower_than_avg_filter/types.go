package main

type filterInput struct {
	AmountPaid    float64 `json:"amount_paid"`
	AvgForFormat  float64 `json:"avg_for_format"`
	FromBank      string  `json:"from_bank"`
	FromAccount   string  `json:"from_account"`
	PaymentFormat string  `json:"payment_format"`
}

type filterOutput struct {
	FromBank      string  `json:"from_bank"`
	FromAccount   string  `json:"from_account"`
	PaymentFormat string  `json:"payment_format"`
	AmountPaid    float64 `json:"amount_paid"`
}
