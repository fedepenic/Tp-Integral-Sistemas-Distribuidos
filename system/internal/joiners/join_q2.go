package joiners

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/aggregators"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

const (
	accountsDataType   = "accounts"
	maxPerBankDataType = "max_per_bank"
	defaultClientID    = "default"
)

type JoinQ2State struct {
	AccountsByBank   map[string]protocol.Account
	PendingMaxByBank map[string][]aggregators.MaxPerBankResult
}

type JoinQ2Logic struct{}

func NewJoinQ2(
	inputMW, outputMW, controlPub, controlSub middleware.Middleware,
	accountsUpstream, maxPerBankUpstream int,
) *worker.JoinerWorker[protocol.Account, aggregators.MaxPerBankResult, JoinQ2State, aggregators.MaxPerBankResult] {
	cfg := worker.JoinerConfig[protocol.Account, aggregators.MaxPerBankResult]{
		Name:                   "join_q2",
		Input:                  inputMW,
		Output:                 outputMW,
		ControlPub:             controlPub,
		ControlSub:             controlSub,
		ExtractLeft:            extractJoinQ2Accounts,
		ExtractRight:           extractJoinQ2MaxPerBank,
		LeftUpstreamInstances:  accountsUpstream,
		RightUpstreamInstances: maxPerBankUpstream,
		OutputDataType:         maxPerBankDataType,
	}

	return worker.NewJoinerWorker[
		protocol.Account,
		aggregators.MaxPerBankResult,
		JoinQ2State,
		aggregators.MaxPerBankResult,
	](
		cfg,
		JoinQ2Logic{},
	)
}

func (JoinQ2Logic) Zero() JoinQ2State {
	return JoinQ2State{
		AccountsByBank:   make(map[string]protocol.Account),
		PendingMaxByBank: make(map[string][]aggregators.MaxPerBankResult),
	}
}

func (JoinQ2Logic) ProcessLeft(
	state JoinQ2State,
	account protocol.Account,
) (JoinQ2State, []aggregators.MaxPerBankResult) {
	state.AccountsByBank[account.BankID] = account

	queued := state.PendingMaxByBank[account.BankID]
	if len(queued) == 0 {
		return state, nil
	}

	out := make([]aggregators.MaxPerBankResult, 0, len(queued))
	for _, res := range queued {
		out = append(out, aggregators.MaxPerBankResult{
			BankID:        res.BankID,
			BankName:      account.BankName,
			SourceAccount: res.SourceAccount,
			MaxAmountUSD:  res.MaxAmountUSD,
		})
	}
	delete(state.PendingMaxByBank, account.BankID)
	return state, out
}

func (JoinQ2Logic) ProcessRight(
	state JoinQ2State,
	res aggregators.MaxPerBankResult,
) (JoinQ2State, []aggregators.MaxPerBankResult) {
	account, ok := state.AccountsByBank[res.BankID]
	if !ok {
		state.PendingMaxByBank[res.BankID] = append(state.PendingMaxByBank[res.BankID], res)
		return state, nil
	}

	return state, []aggregators.MaxPerBankResult{{
		BankID:        res.BankID,
		BankName:      account.BankName,
		SourceAccount: res.SourceAccount,
		MaxAmountUSD:  res.MaxAmountUSD,
	}}
}

func (JoinQ2Logic) Flush(state JoinQ2State) []aggregators.MaxPerBankResult {
	unmatched := 0
	for _, results := range state.PendingMaxByBank {
		unmatched += len(results)
	}
	if unmatched > 0 {
		log.Printf("[join_q2] %d unmatched max_per_bank results at EOF (no account found)", unmatched)
	}
	return nil
}

func extractJoinQ2Accounts(batch protocol.Batch) ([]protocol.Account, bool) {
	if batch.DataType == accountsDataType || batch.Type == protocol.BatchTypeAccounts {
		return batch.Accounts, true
	}
	return nil, false
}

func extractJoinQ2MaxPerBank(batch protocol.Batch) ([]aggregators.MaxPerBankResult, bool) {
	if batch.DataType != maxPerBankDataType {
		if batch.Type == protocol.BatchTypeEOF && batch.DataType != accountsDataType {
			return nil, true
		}
		return nil, false
	}
	if batch.Type == protocol.BatchTypeEOF || len(batch.Records) == 0 {
		return nil, true
	}

	var results []aggregators.MaxPerBankResult
	if err := json.Unmarshal(batch.Records, &results); err != nil {
		log.Printf("[join_q2] malformed max_per_bank records: %v", err)
		return nil, true
	}
	return results, true
}
