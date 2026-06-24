package main

type avgState struct {
	Sum   float64 `json:"sum"`
	Count int     `json:"count"`
}

type avgPerFormatResult struct {
	PaymentFormat string  `json:"payment_format"`
	AvgAmount     float64 `json:"avg_amount"`
}

type avgDelta struct {
	ClientID string                `json:"client_id"`
	Formats  map[string]avgState   `json:"formats"`
}
