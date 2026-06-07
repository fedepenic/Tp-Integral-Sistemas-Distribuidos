package main

const amountThreshold = 1.0

type filterInput struct {
	AmountPaid float64 `json:"amount_paid"`
}

type filterOutput struct {
	AmountPaid float64 `json:"amount_paid"`
}
