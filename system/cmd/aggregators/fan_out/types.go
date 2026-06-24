package main

type accountRef struct {
	Bank    string `json:"bank"`
	Account string `json:"account"`
}

type fanOutEntry struct {
	FromBank string       `json:"from_bank"`
	FromAcct string       `json:"from_acct"`
	Refs     []accountRef `json:"refs"`
}

type fanOutResult struct {
	FromBank      string `json:"from_bank"`
	FromAccount   string `json:"from_account"`
	MiddleBank    string `json:"middle_bank"`
	MiddleAccount string `json:"middle_account"`
}

type fanOutDelta struct {
	ClientID string               `json:"client_id"`
	Entries  map[string]fanOutEntry `json:"entries"`
}
