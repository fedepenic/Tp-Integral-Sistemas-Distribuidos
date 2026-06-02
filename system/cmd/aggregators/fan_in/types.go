package main

type accountRef struct {
	bank    string
	account string
}

func refKey(r accountRef) string {
	return r.bank + "|" + r.account
}

type fanInEntry struct {
	toBank   string
	toAcct   string
	distinct map[string]accountRef // refKey → accountRef
}

type fanInResult struct {
	MiddleBank    string `json:"middle_bank"`
	MiddleAccount string `json:"middle_account"`
	ToBank        string `json:"to_bank"`
	ToAccount     string `json:"to_account"`
}
