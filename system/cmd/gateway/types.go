package main

type maxPerBankResult struct {
	BankID        string  `json:"bank_id"`
	BankName      string  `json:"bank_name"`
	SourceAccount string  `json:"source_account"`
	MaxAmountUSD  float64 `json:"max_amount_usd"`
}

type scatterGatherResult struct {
	FromBank    string `json:"from_bank"`
	FromAccount string `json:"from_account"`
	ToBank      string `json:"to_bank"`
	ToAccount   string `json:"to_account"`
	TargetCount int    `json:"target_count"`
}
