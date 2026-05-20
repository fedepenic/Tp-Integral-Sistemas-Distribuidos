package aggregators

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

type FanInState struct {
	Distinct map[string]AccountRef
}

type FanInResult struct {
	MiddleBank    string `json:"middle_bank"`
	MiddleAccount string `json:"middle_account"`
	ToBank        string `json:"to_bank"`
	ToAccount     string `json:"to_account"`
}

type FanInLogic struct{}

func (FanInLogic) Key(tx protocol.Transaction) AccountRef {
	return AccountRef{Bank: tx.ToBank, Account: tx.ToAccount}
}

func (FanInLogic) Zero() FanInState {
	return FanInState{Distinct: map[string]AccountRef{}}
}

func (FanInLogic) Accumulate(state FanInState, tx protocol.Transaction) FanInState {
	if state.Distinct == nil {
		state.Distinct = map[string]AccountRef{}
	}
	ref := AccountRef{Bank: tx.FromBank, Account: tx.FromAccount}
	state.Distinct[accountKey(ref)] = ref
	return state
}

func (FanInLogic) Finalize(key AccountRef, state FanInState) []FanInResult {
	results := make([]FanInResult, 0, len(state.Distinct))
	for _, ref := range state.Distinct {
		results = append(results, FanInResult{
			MiddleBank:    ref.Bank,
			MiddleAccount: ref.Account,
			ToBank:        key.Bank,
			ToAccount:     key.Account,
		})
	}
	return results
}
