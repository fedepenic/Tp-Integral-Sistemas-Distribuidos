package main

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

type avgPerFormatResult struct {
	PaymentFormat string  `json:"payment_format"`
	AvgAmount     float64 `json:"avg_amount"`
}

type pendingTxn struct {
	PaymentFormat string  `json:"payment_format"`
	AmountPaid    float64 `json:"amount_paid"`
	FromBank      string  `json:"from_bank"`
	FromAccount   string  `json:"from_account"`
}

type joinQ3State struct {
	ThresholdsByFormat map[string]float64    `json:"thresholds_by_format"`
	PendingTxns        map[string][]pendingTxn `json:"pending_txns"`
}

type joinQ3Delta struct {
	ClientID  string                        `json:"client_id"`
	Avgs      []avgPerFormatResult          `json:"avgs,omitempty"`
	Txns      []pendingTxn                  `json:"txns,omitempty"`
	Resolved  []protocol.Transaction        `json:"resolved,omitempty"`
}
