package main

const scatterThreshold = 5

type sgEntry struct {
	fromBank    string
	fromAccount string
	toBank      string
	toAccount   string
	count       int
}

type scatterGatherResult struct {
	FromBank    string `json:"from_bank"`
	FromAccount string `json:"from_account"`
	ToBank      string `json:"to_bank"`
	ToAccount   string `json:"to_account"`
	TargetCount int    `json:"target_count"`
}
