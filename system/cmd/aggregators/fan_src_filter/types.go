package main

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

// srcEntry buffers all transactions from one source account and tracks
// how many distinct destination accounts it has sent to.
type srcEntry struct {
	fromBank     string
	fromAcct     string
	distinctTo   map[string]struct{}
	transactions []protocol.Transaction
}
