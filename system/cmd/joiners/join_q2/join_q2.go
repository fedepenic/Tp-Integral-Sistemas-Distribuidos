package main

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/dedup"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func newProcess() node.ProcessFunc {
	var mu sync.Mutex
	states := make(map[string]joinQ2State)
	deduper := dedup.New()

	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
			if deduper.Seen(batch.BatchID) {
				return protocol.Batch{}, false
			}
		}

		if batch.Type == protocol.BatchTypeEOF {
			mu.Lock()
			state, ok := states[batch.ClientID]
			if ok {
				unmatched := 0
				for _, pending := range state.pendingMaxByBank {
					unmatched += len(pending)
				}
				if unmatched > 0 {
					log.Printf("[join_q2] %d unmatched max_per_bank results at EOF for client=%s", unmatched, batch.ClientID)
				}
				delete(states, batch.ClientID)
			}
			mu.Unlock()
			return protocol.Batch{}, false
		}

		mu.Lock()
		defer mu.Unlock()

		state, ok := states[batch.ClientID]
		if !ok {
			state = joinQ2State{
				accountsByBank:   make(map[string]protocol.Account),
				pendingMaxByBank: make(map[string][]maxPerBankResult),
			}
		}

		var results []maxPerBankResult

		if batch.DataType == "accounts" || batch.Type == protocol.BatchTypeAccounts {
			for _, account := range batch.Accounts {
				state.accountsByBank[account.BankID] = account
				if pending, exists := state.pendingMaxByBank[account.BankID]; exists {
					for _, res := range pending {
						results = append(results, maxPerBankResult{
							BankID:        res.BankID,
							BankName:      account.BankName,
							SourceAccount: res.SourceAccount,
							MaxAmountUSD:  res.MaxAmountUSD,
						})
					}
					delete(state.pendingMaxByBank, account.BankID)
				}
			}
		}

		if batch.DataType == "max_per_bank" && len(batch.Records) > 0 {
			var maxResults []maxPerBankResult
			if err := json.Unmarshal(batch.Records, &maxResults); err != nil {
				log.Printf("[join_q2] malformed max_per_bank records: %v", err)
				states[batch.ClientID] = state
				return protocol.Batch{}, false
			}
			for _, res := range maxResults {
				if account, exists := state.accountsByBank[res.BankID]; exists {
					results = append(results, maxPerBankResult{
						BankID:        res.BankID,
						BankName:      account.BankName,
						SourceAccount: res.SourceAccount,
						MaxAmountUSD:  res.MaxAmountUSD,
					})
				} else {
					state.pendingMaxByBank[res.BankID] = append(state.pendingMaxByBank[res.BankID], res)
				}
			}
		}

		states[batch.ClientID] = state
		deduper.Mark(batch.BatchID)

		if len(results) == 0 {
			return protocol.Batch{}, false
		}

		records, err := json.Marshal(results)
		if err != nil {
			log.Printf("[join_q2] marshal results: %v", err)
			return protocol.Batch{}, false
		}
		return protocol.Batch{
			Type:     protocol.BatchTypeData,
			ClientID: batch.ClientID,
			DataType: "max_per_bank",
			BatchID:  batch.BatchID,
			Records:  json.RawMessage(records),
		}, true
	}
}
