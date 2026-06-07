package main

type maxPerBankState struct {
	bankID        string
	bankName      string
	sourceAccount string
	maxAmountUSD  float64
	hasValue      bool
}

// maxPerBankResult is the record sent downstream to join_q2.
type maxPerBankResult struct {
	BankID        string  `json:"bank_id"`
	BankName      string  `json:"bank_name"`
	SourceAccount string  `json:"source_account"`
	MaxAmountUSD  float64 `json:"max_amount_usd"`
}
