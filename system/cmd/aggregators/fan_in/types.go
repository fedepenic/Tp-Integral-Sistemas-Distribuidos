package main

type accountRef struct {
	bank    string
	account string
}

type fanInEntry struct {
	toBank string
	toAcct string
	refs   map[accountRef]struct{}
}

type fanInResult struct {
	MiddleBank    string `json:"middle_bank"`
	MiddleAccount string `json:"middle_account"`
	ToBank        string `json:"to_bank"`
	ToAccount     string `json:"to_account"`
}
