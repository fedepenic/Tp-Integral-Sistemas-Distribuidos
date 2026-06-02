package main

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

type fanOutResult struct {
	FromBank      string `json:"from_bank"`
	FromAccount   string `json:"from_account"`
	MiddleBank    string `json:"middle_bank"`
	MiddleAccount string `json:"middle_account"`
}

type fanInResult struct {
	MiddleBank    string `json:"middle_bank"`
	MiddleAccount string `json:"middle_account"`
	ToBank        string `json:"to_bank"`
	ToAccount     string `json:"to_account"`
}

type sgState struct {
	fanOutByMid map[string][]fanOutResult // middleKey → results
	fanInByMid  map[string][]fanInResult
}

func newSGState() sgState {
	return sgState{
		fanOutByMid: make(map[string][]fanOutResult),
		fanInByMid:  make(map[string][]fanInResult),
	}
}

func middleKey(bank, account string) string {
	return bank + "|" + account
}

func makeScatterGatherItem(fo fanOutResult, fi fanInResult) protocol.ScatterGatherItem {
	return protocol.ScatterGatherItem{
		FromBank:      fo.FromBank,
		FromAccount:   fo.FromAccount,
		MiddleBank:    fo.MiddleBank,
		MiddleAccount: fo.MiddleAccount,
		ToBank:        fi.ToBank,
		ToAccount:     fi.ToAccount,
	}
}
