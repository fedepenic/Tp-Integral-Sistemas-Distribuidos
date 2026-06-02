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
		w.csv.Write([]string{"From Bank", "Account", "Payment Format", "Amount Paid"})
		w.headerWritten = true
	}
	for _, t := range batch.Transactions {
		w.csv.Write([]string{
			t.FromBank,
			t.FromAccount,
			t.PaymentFormat,
			strconv.FormatFloat(t.AmountPaid, 'f', -1, 64),
		})
	}
	return w.csv.Error()
}
