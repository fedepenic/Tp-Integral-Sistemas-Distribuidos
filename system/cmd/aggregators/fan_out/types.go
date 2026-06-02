package main

type fanOutEntry struct {
	fromBank string
	fromAcct string
	distinct map[string]accountRef // refKey → accountRef
}

type fanOutResult struct {
	FromBank      string `json:"from_bank"`
	FromAccount   string `json:"from_account"`
	MiddleBank    string `json:"middle_bank"`
	MiddleAccount string `json:"middle_account"`
}

type accountRef struct {
	bank    string
	account string
}

func refKey(r accountRef) string {
	return r.bank + "|" + r.account
}
