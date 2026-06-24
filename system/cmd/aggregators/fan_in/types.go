package main

type accountRef struct {
	Bank    string `json:"bank"`
	Account string `json:"account"`
}

type fanInEntry struct {
	ToBank string       `json:"to_bank"`
	ToAcct string       `json:"to_acct"`
	Refs   []accountRef `json:"refs"`
}

type fanInResult struct {
	MiddleBank    string `json:"middle_bank"`
	MiddleAccount string `json:"middle_account"`
	ToBank        string `json:"to_bank"`
	ToAccount     string `json:"to_account"`
}

type fanInDelta struct {
	ClientID string               `json:"client_id"`
	Entries  map[string]fanInEntry `json:"entries"`
}
