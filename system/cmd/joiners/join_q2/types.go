package main

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

type maxPerBankResult struct {
	BankID        string  `json:"bank_id"`
	BankName      string  `json:"bank_name"`
	SourceAccount string  `json:"source_account"`
	MaxAmountUSD  float64 `json:"max_amount_usd"`
}

type joinQ2State struct {
	accountsByBank   map[string]protocol.Account
	pendingMaxByBank map[string][]maxPerBankResult
}
