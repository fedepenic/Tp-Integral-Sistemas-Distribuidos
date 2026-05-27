package main

import (
	"log"
	"strings"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// cleanTransactions drops incomplete or malformed transactions and strips the
// IsLaundering label, which is not used by any downstream query.
//
// Drop criteria:
//   - Empty Timestamp (date-based queries require it)
//   - Empty FromAccount or ToAccount (account graph queries require both endpoints)
//   - Empty PaymentCurrency or ReceivingCurrency (currency conversion requires them)
//   - AmountPaid <= 0 or AmountReceived <= 0 (zero-amount records carry no signal)
//   - Empty PaymentFormat (queries Q3 and Q5 partition by payment format)
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
//
// Drop criteria:
//   - Empty BankName, BankID, or AccountNumber (needed to resolve bank/account references)
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

func main() {
	svc := node.New("cleaner")
	conn := svc.Conn()

	inputQueue     := config.EnvOrDefault("INPUT_QUEUE", "raw_transactions")
	outputExchange := config.EnvOrDefault("OUTPUT_EXCHANGE", "transactions_clean")
	outputKeys     := strings.Split(config.EnvOrDefault("OUTPUT_KEYS", "txn_for_usd,txn_for_q5"), ",")

	inputMW, err := middleware.CreateQueueMiddleware(inputQueue, conn)
	if err != nil {
		log.Fatalf("[cleaner] connect to input queue: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.CreateExchangeMiddleware(outputExchange, outputKeys, conn)
	if err != nil {
		log.Fatalf("[cleaner] connect to output exchange: %v", err)
	}
	defer outputMW.Close()

	svc.Run(inputMW, outputMW, func(batch protocol.Batch) (protocol.Batch, bool) {
		log.Printf("[cleaner] client=%s type=%s txns=%d accounts=%d", batch.ClientID, batch.Type, len(batch.Transactions), len(batch.Accounts))

		cleaned := protocol.Batch{Type: batch.Type, ClientID: batch.ClientID}
		switch batch.Type {
		case protocol.BatchTypeTransactions:
			cleaned.Transactions = cleanTransactions(batch.Transactions)
		case protocol.BatchTypeAccounts:
			cleaned.Accounts = cleanAccounts(batch.Accounts)
		}

		log.Printf("[cleaner] client=%s type=%s in=%d out=%d", batch.ClientID, batch.Type, len(batch.Transactions)+len(batch.Accounts), len(cleaned.Transactions)+len(cleaned.Accounts))

		// Account batches are not forwarded: no downstream consumer needs them.
		if cleaned.Type != protocol.BatchTypeTransactions {
			return protocol.Batch{}, false
		}
		return cleaned, true
	})
}
