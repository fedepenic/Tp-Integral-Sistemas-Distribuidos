package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func newProcess() node.ProcessFunc {
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		log.Printf("[cleaner] client=%s type=%s txns=%d accounts=%d",
			batch.ClientID, batch.Type, len(batch.Transactions), len(batch.Accounts))

		cleaned := protocol.Batch{Type: batch.Type, ClientID: batch.ClientID}
		switch batch.Type {
		case protocol.BatchTypeTransactions:
			cleaned.Transactions = cleanTransactions(batch.Transactions)
		case protocol.BatchTypeAccounts:
			cleaned.Accounts = cleanAccounts(batch.Accounts)
		}

		log.Printf("[cleaner] client=%s type=%s in=%d out=%d",
			batch.ClientID, batch.Type,
			len(batch.Transactions)+len(batch.Accounts),
			len(cleaned.Transactions)+len(cleaned.Accounts))

		if cleaned.Type != protocol.BatchTypeTransactions {
			return protocol.Batch{}, false
		}
		return cleaned, true
	}
}

// cleanTransactions drops incomplete or malformed transactions and strips the
// IsLaundering label, which is not used by any downstream query.
func cleanTransactions(txns []protocol.Transaction) []protocol.Transaction {
	out := make([]protocol.Transaction, 0, len(txns))
	for _, t := range txns {
		if t.Timestamp == "" {
			continue
		}
		if t.FromAccount == "" || t.ToAccount == "" {
			continue
		}
		if t.PaymentCurrency == "" || t.ReceivingCurrency == "" {
			continue
		}
		if t.AmountPaid <= 0 || t.AmountReceived <= 0 {
			continue
		}
		if t.PaymentFormat == "" {
			continue
		}
		t.IsLaundering = 0
		out = append(out, t)
	}
	return out
}

// cleanAccounts drops account records that are missing identifying fields.
func cleanAccounts(accounts []protocol.Account) []protocol.Account {
	out := make([]protocol.Account, 0, len(accounts))
	for _, a := range accounts {
		if a.BankName == "" || a.BankID == "" || a.AccountNumber == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}
