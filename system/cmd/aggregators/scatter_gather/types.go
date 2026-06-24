package main

const scatterThreshold = 5

type sgEntry struct {
	FromBank    string `json:"from_bank"`
	FromAccount string `json:"from_account"`
	ToBank      string `json:"to_bank"`
	ToAccount   string `json:"to_account"`
	Count       int    `json:"count"`
}

type scatterGatherResult struct {
	FromBank    string `json:"from_bank"`
	FromAccount string `json:"from_account"`
	ToBank      string `json:"to_bank"`
	ToAccount   string `json:"to_account"`
	TargetCount int    `json:"target_count"`
}

type sgDelta struct {
	ClientID string              `json:"client_id"`
	Entries  map[string]*sgEntry `json:"entries"`
}
