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
		writeStagingHeaders(w, batch.QueryID)
	}
	rowNumber := 0
	for _, res := range results {
		w.csv.Write(rowWithMetadata(batch.BatchID, rowNumber, []string{res.FromBank, res.FromAccount}))
		rowNumber++
		w.csv.Write(rowWithMetadata(batch.BatchID, rowNumber, []string{res.ToBank, res.ToAccount}))
		rowNumber++
	}
	return w.csv.Error()
}
