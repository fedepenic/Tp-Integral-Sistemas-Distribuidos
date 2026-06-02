package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

func newProcess(accountsMW middleware.Middleware, keyPrefix string, partitions int) node.ProcessFunc {
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		switch batch.Type {
		case protocol.BatchTypeTransactions:
			cleaned := cleanTransactions(batch.Transactions)
			if len(cleaned) == 0 {
				return protocol.Batch{}, false
			}
			return protocol.Batch{Type: batch.Type, ClientID: batch.ClientID, Transactions: cleaned}, true

		case protocol.BatchTypeAccounts:
			cleaned := cleanAccounts(batch.Accounts)
			if len(cleaned) > 0 {
				sendAccountBatches(accountsMW, batch.ClientID, cleaned, keyPrefix, partitions)
			}
			return protocol.Batch{}, false

		case protocol.BatchTypeEOF:
			// Send one accounts EOF per join_q2 partition so the joiner knows
			// the left side is done. Return false so Scalable still forwards
			// the transactions EOF on outputMW.
			sendAccountsEOF(accountsMW, batch.ClientID, keyPrefix, partitions)
			return protocol.Batch{}, false
		}
		return protocol.Batch{}, false
	}
}

func sendAccountBatches(mw middleware.Middleware, clientID string, accounts []protocol.Account, keyPrefix string, partitions int) {
	partitioned := make(map[int][]protocol.Account, partitions)
	for _, a := range accounts {
		p := worker.PartitionForKey(a.BankID, partitions)
		partitioned[p] = append(partitioned[p], a)
	}
	for p, batch := range partitioned {
		key := worker.RoutingKey(keyPrefix, p)
		out := protocol.Batch{
			Type:     protocol.BatchTypeAccounts,
			ClientID: clientID,
			DataType: "accounts",
			Accounts: batch,
		}
		data, err := json.Marshal(out)
		if err != nil {
			log.Printf("[cleaner] marshal accounts partition=%d: %v", p, err)
			continue
		}
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[cleaner] send accounts partition=%d: %v", p, err)
		}
	}
}

func sendAccountsEOF(mw middleware.Middleware, clientID string, keyPrefix string, partitions int) {
	eof := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: "accounts",
	}
	data, err := json.Marshal(eof)
	if err != nil {
		log.Printf("[cleaner] marshal accounts EOF: %v", err)
		return
	}
	for p := 0; p < partitions; p++ {
		key := worker.RoutingKey(keyPrefix, p)
		if err := mw.SendWithKey(middleware.Message{Body: string(data)}, key); err != nil {
			log.Printf("[cleaner] send accounts EOF partition=%d: %v", p, err)
		}
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
