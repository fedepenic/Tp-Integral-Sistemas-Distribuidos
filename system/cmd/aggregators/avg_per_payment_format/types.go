package main

type avgState struct {
	sum   float64
	count int
}

type avgPerFormatResult struct {
	PaymentFormat string  `json:"payment_format"`
	AvgAmount     float64 `json:"avg_amount"`
}
