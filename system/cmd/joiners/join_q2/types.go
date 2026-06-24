package main

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

type maxPerBankResult struct {
	BankID        string  `json:"bank_id"`
	BankName      string  `json:"bank_name"`
	SourceAccount string  `json:"source_account"`
	MaxAmountUSD  float64 `json:"max_amount_usd"`
}

type joinQ2State struct {
	AccountsByBank   map[string]protocol.AccountRef `json:"accounts_by_bank"`
	PendingMaxByBank map[string][]maxPerBankResult   `json:"pending_max_by_bank"`
}

type joinQ2Delta struct {
	ClientID   string                          `json:"client_id"`
	Accounts   []protocol.AccountRef           `json:"accounts,omitempty"`
	MaxResults []maxPerBankResult              `json:"max_results,omitempty"`
	Resolved   []maxPerBankResult              `json:"resolved,omitempty"`
}
