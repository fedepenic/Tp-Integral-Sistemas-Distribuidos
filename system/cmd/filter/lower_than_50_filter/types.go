package main

const amountThreshold = 50

type filterInput struct {
	FromBank    string  `json:"from_bank"`
	FromAccount string  `json:"from_account"`
	ToBank      string  `json:"to_bank"`
	ToAccount   string  `json:"to_account"`
	AmountPaid  float64 `json:"amount_paid"`
}

type filterOutput struct {
	FromBank    string  `json:"from_bank"`
	FromAccount string  `json:"from_account"`
	ToBank      string  `json:"to_bank"`
	ToAccount   string  `json:"to_account"`
	AmountPaid  float64 `json:"amount_paid"`
}
