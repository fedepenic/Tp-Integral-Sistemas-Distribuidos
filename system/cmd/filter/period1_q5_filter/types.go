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
}

type filterOutput struct {
	Timestamp       string  `json:"timestamp"`
	AmountPaid      float64 `json:"amount_paid"`
	PaymentCurrency string  `json:"payment_currency"`
}
