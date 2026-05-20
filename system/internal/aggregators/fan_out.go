package aggregators

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

type FanOutState struct {
	Distinct map[string]AccountRef
}

type FanOutResult struct {
	FromBank      string `json:"from_bank"`
	FromAccount   string `json:"from_account"`
	MiddleBank    string `json:"middle_bank"`
	MiddleAccount string `json:"middle_account"`
}

type FanOutLogic struct{}

func (FanOutLogic) Key(tx protocol.Transaction) AccountRef {
	return AccountRef{Bank: tx.FromBank, Account: tx.FromAccount}
}

func (FanOutLogic) Zero() FanOutState {
	return FanOutState{Distinct: map[string]AccountRef{}}
}

func (FanOutLogic) Accumulate(state FanOutState, tx protocol.Transaction) FanOutState {
	if state.Distinct == nil {
		state.Distinct = map[string]AccountRef{}
	}
	ref := AccountRef{Bank: tx.ToBank, Account: tx.ToAccount}
	state.Distinct[accountKey(ref)] = ref
	return state
}

func (FanOutLogic) Finalize(key AccountRef, state FanOutState) []FanOutResult {
	results := make([]FanOutResult, 0, len(state.Distinct))
	for _, ref := range state.Distinct {
		results = append(results, FanOutResult{
			FromBank:      key.Bank,
			FromAccount:   key.Account,
			MiddleBank:    ref.Bank,
			MiddleAccount: ref.Account,
		})
	}
	return results
}
