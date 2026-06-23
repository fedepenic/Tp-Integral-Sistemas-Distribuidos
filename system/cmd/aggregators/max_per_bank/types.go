package main

type maxPerBankState struct {
	BankID        string  `json:"bank_id"`
	BankName      string  `json:"bank_name"`
	SourceAccount string  `json:"source_account"`
	MaxAmountUSD  float64 `json:"max_amount_usd"`
	HasValue      bool    `json:"has_value"`
}

type maxPerBankResult struct {
	BankID        string  `json:"bank_id"`
	BankName      string  `json:"bank_name"`
	SourceAccount string  `json:"source_account"`
	MaxAmountUSD  float64 `json:"max_amount_usd"`
}

type maxPerBankDelta struct {
	ClientID string                     `json:"client_id"`
	Banks    map[string]maxPerBankState `json:"banks"`
}
