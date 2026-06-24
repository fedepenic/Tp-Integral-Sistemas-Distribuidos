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
	FanOutByMid map[string][]fanOutResult `json:"fan_out_by_mid"`
	FanInByMid  map[string][]fanInResult  `json:"fan_in_by_mid"`
}

func newSGState() sgState {
	return sgState{
		FanOutByMid: make(map[string][]fanOutResult),
		FanInByMid:  make(map[string][]fanInResult),
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

type sgDelta struct {
	ClientID  string                `json:"client_id"`
	FanOut    []fanOutResult        `json:"fan_out,omitempty"`
	FanIn     []fanInResult         `json:"fan_in,omitempty"`
}
