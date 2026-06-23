package main

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

type srcEntry struct {
	FromBank     string                `json:"from_bank"`
	FromAcct     string                `json:"from_acct"`
	DistinctTo   map[string]bool       `json:"distinct_to"`
	Transactions []protocol.Transaction `json:"transactions"`
}

type srcDelta struct {
	ClientID string              `json:"client_id"`
	Entries  map[string]*srcEntry `json:"entries"`
}
