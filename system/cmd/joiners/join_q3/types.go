package main

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

type avgPerFormatResult struct {
	PaymentFormat string  `json:"payment_format"`
	AvgAmount     float64 `json:"avg_amount"`
}

type joinQ3State struct {
	thresholdsByFormat map[string]float64
	pendingTxns        map[string][]protocol.Transaction
}
