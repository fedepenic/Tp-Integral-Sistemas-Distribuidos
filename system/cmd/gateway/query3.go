package main

import (
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func writeQ3Rows(w *queryWriter, batch protocol.Batch) error {
	if len(batch.Transactions) == 0 {
		return nil
	}
	if !w.headerWritten {
		writeStagingHeaders(w, batch.QueryID)
	}
	for i, t := range batch.Transactions {
		w.csv.Write(rowWithMetadata(batch.BatchID, i, []string{
			t.FromBank,
			t.FromAccount,
			t.PaymentFormat,
			strconv.FormatFloat(t.AmountPaid, 'f', -1, 64),
		}))
	}
	return w.csv.Error()
}
