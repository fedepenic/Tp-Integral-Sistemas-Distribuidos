package main

import (
	"encoding/json"
	"fmt"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func writeQ4Rows(w *queryWriter, batch protocol.Batch) error {
	if len(batch.Records) == 0 {
		return nil
	}
	var results []scatterGatherResult
	if err := json.Unmarshal(batch.Records, &results); err != nil {
		return fmt.Errorf("unmarshal scatter_gather_result: %w", err)
	}
	if !w.headerWritten {
		w.csv.Write([]string{"Bank", "Account"})
		w.headerWritten = true
	}
	if w.seenAccounts == nil {
		w.seenAccounts = make(map[string]bool)
	}
	for _, res := range results {
		fromKey := res.FromBank + "|" + res.FromAccount
		if !w.seenAccounts[fromKey] {
			w.seenAccounts[fromKey] = true
			w.csv.Write([]string{res.FromBank, res.FromAccount})
		}
		toKey := res.ToBank + "|" + res.ToAccount
		if !w.seenAccounts[toKey] {
			w.seenAccounts[toKey] = true
			w.csv.Write([]string{res.ToBank, res.ToAccount})
		}
	}
	return w.csv.Error()
}
