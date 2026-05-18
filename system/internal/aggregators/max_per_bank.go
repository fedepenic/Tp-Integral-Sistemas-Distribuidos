package aggregators

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

type MaxPerBankState struct {
	BankName      string
	SourceAccount string
	MaxAmountUSD  float64
	HasValue      bool
}

type MaxPerBankResult struct {
	BankName      string  `json:"bank_name"`
	SourceAccount string  `json:"source_account"`
	MaxAmountUSD  float64 `json:"max_amount_usd"`
}

type MaxPerBankLogic struct{}

func (MaxPerBankLogic) Key(tx protocol.Transaction) string {
	return tx.FromBank
}

func (MaxPerBankLogic) Zero() MaxPerBankState {
	return MaxPerBankState{}
}

func (MaxPerBankLogic) Accumulate(state MaxPerBankState, tx protocol.Transaction) MaxPerBankState {
	if !state.HasValue || tx.AmountPaid > state.MaxAmountUSD {
		return MaxPerBankState{
			BankName:      tx.FromBank,
			SourceAccount: tx.FromAccount,
			MaxAmountUSD:  tx.AmountPaid,
			HasValue:      true,
		}
	}
	return state
}

func (MaxPerBankLogic) Finalize(_ string, state MaxPerBankState) []MaxPerBankResult {
	if !state.HasValue {
		return nil
	}
	return []MaxPerBankResult{{
		BankName:      state.BankName,
		SourceAccount: state.SourceAccount,
		MaxAmountUSD:  state.MaxAmountUSD,
	}}
}
