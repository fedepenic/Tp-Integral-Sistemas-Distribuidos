package main

const (
	dateLayout  = "2006-01-02"
	periodStart = "2022-09-06"
	periodEnd   = "2022-09-15"
)

type filterInput struct {
	Timestamp     string  `json:"timestamp"`
	PaymentFormat string  `json:"payment_format"`
	AmountPaid    float64 `json:"amount_paid"`
	FromBank      string  `json:"from_bank"`
	FromAccount   string  `json:"from_account"`
}

type filterOutput struct {
	PaymentFormat string  `json:"payment_format"`
	AmountPaid    float64 `json:"amount_paid"`
	FromBank      string  `json:"from_bank"`
	FromAccount   string  `json:"from_account"`
}
