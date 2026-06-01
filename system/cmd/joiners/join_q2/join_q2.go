package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

const (
	accountsDataType   = "accounts"
	maxPerBankDataType = "max_per_bank"
)

func newJoinQ2(
	inputMW, outputMW, controlPub, controlSub middleware.Middleware,
	accountsUpstream, maxPerBankUpstream int,
) *worker.JoinerWorker[protocol.Account, maxPerBankResult, joinQ2State, maxPerBankResult] {
	cfg := worker.JoinerConfig[protocol.Account, maxPerBankResult]{
		Name:                   "join_q2",
		Input:                  inputMW,
		Output:                 outputMW,
		ControlPub:             controlPub,
		ControlSub:             controlSub,
		ExtractLeft:            extractAccounts,
		ExtractRight:           extractMaxPerBank,
		LeftUpstreamInstances:  accountsUpstream,
		RightUpstreamInstances: maxPerBankUpstream,
		OutputDataType:         maxPerBankDataType,
	}
	return worker.NewJoinerWorker[
		protocol.Account,
		maxPerBankResult,
		joinQ2State,
		maxPerBankResult,
	](cfg, joinQ2Logic{})
}

type joinQ2Logic struct{}

func (joinQ2Logic) Zero() joinQ2State {
	return joinQ2State{
		accountsByBank:   make(map[string]protocol.Account),
		pendingMaxByBank: make(map[string][]maxPerBankResult),
	}
}

func (joinQ2Logic) ProcessLeft(state joinQ2State, account protocol.Account) (joinQ2State, []maxPerBankResult) {
	state.accountsByBank[account.BankID] = account

	queued := state.pendingMaxByBank[account.BankID]
	if len(queued) == 0 {
		return state, nil
	}

	out := make([]maxPerBankResult, 0, len(queued))
	for _, res := range queued {
		out = append(out, maxPerBankResult{
			BankID:        res.BankID,
			BankName:      account.BankName,
			SourceAccount: res.SourceAccount,
			MaxAmountUSD:  res.MaxAmountUSD,
		})
	}
	delete(state.pendingMaxByBank, account.BankID)
	return state, out
}

func (joinQ2Logic) ProcessRight(state joinQ2State, res maxPerBankResult) (joinQ2State, []maxPerBankResult) {
	account, ok := state.accountsByBank[res.BankID]
	if !ok {
		state.pendingMaxByBank[res.BankID] = append(state.pendingMaxByBank[res.BankID], res)
		return state, nil
	}
	return state, []maxPerBankResult{{
		BankID:        res.BankID,
		BankName:      account.BankName,
		SourceAccount: res.SourceAccount,
		MaxAmountUSD:  res.MaxAmountUSD,
	}}
}

func (joinQ2Logic) Flush(state joinQ2State) []maxPerBankResult {
	unmatched := 0
	for _, results := range state.pendingMaxByBank {
		unmatched += len(results)
	}
	if unmatched > 0 {
		log.Printf("[join_q2] %d unmatched max_per_bank results at EOF (no account found)", unmatched)
	}
	return nil
}

func extractAccounts(batch protocol.Batch) ([]protocol.Account, bool) {
	if batch.DataType == accountsDataType || batch.Type == protocol.BatchTypeAccounts {
		return batch.Accounts, true
	}
	return nil, false
}

func extractMaxPerBank(batch protocol.Batch) ([]maxPerBankResult, bool) {
	if batch.DataType != maxPerBankDataType {
		if batch.Type == protocol.BatchTypeEOF && batch.DataType != accountsDataType {
			return nil, true
		}
		return nil, false
	}
	if batch.Type == protocol.BatchTypeEOF || len(batch.Records) == 0 {
		return nil, true
	}
	var results []maxPerBankResult
	if err := json.Unmarshal(batch.Records, &results); err != nil {
		log.Printf("[join_q2] malformed max_per_bank records: %v", err)
		return nil, true
	}
	return results, true
}
