package main

const (
	dateLayout  = "2006-01-02"
	periodStart = "2022-09-01"
	periodEnd   = "2022-09-05"
)

type filterInput struct {
	Timestamp     string  `json:"timestamp"`
	PaymentFormat string  `json:"payment_format"`
	AmountPaid    float64 `json:"amount_paid"`
	FromBank      string  `json:"from_bank"`
	FromAccount   string  `json:"from_account"`
	ToBank        string  `json:"to_bank"`
	ToAccount     string  `json:"to_account"`
}

// q3Output carries the fields needed by avg_per_payment_format.
type q3Output struct {
	PaymentFormat string  `json:"payment_format"`
	AmountPaid    float64 `json:"amount_paid"`
}

// q4Output carries the fields needed by fan_out and fan_in.
type q4Output struct {
	FromBank    string `json:"from_bank"`
	FromAccount string `json:"from_account"`
	ToBank      string `json:"to_bank"`
	ToAccount   string `json:"to_account"`
}
