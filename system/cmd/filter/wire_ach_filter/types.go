package main

const (
	formatWire = "Wire"
	formatACH  = "ACH"
)

type filterInput struct {
	PaymentFormat string  `json:"payment_format"`
	FromBank      string  `json:"from_bank"`
	FromAccount   string  `json:"from_account"`
	ToBank        string  `json:"to_bank"`
	ToAccount     string  `json:"to_account"`
	AmountPaid    float64 `json:"amount_paid"`
}

type filterOutput struct {
	FromBank    string  `json:"from_bank"`
	FromAccount string  `json:"from_account"`
	ToBank      string  `json:"to_bank"`
	ToAccount   string  `json:"to_account"`
	AmountPaid  float64 `json:"amount_paid"`
}
