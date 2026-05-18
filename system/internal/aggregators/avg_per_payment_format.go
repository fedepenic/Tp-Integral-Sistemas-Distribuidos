package aggregators

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

type AvgPerPaymentFormatState struct {
	Sum   float64
	Count int
}

type AvgPerPaymentFormatResult struct {
	PaymentFormat string  `json:"payment_format"`
	AvgAmount     float64 `json:"avg_amount"`
}

type AvgPerPaymentFormatLogic struct{}

func (AvgPerPaymentFormatLogic) Key(tx protocol.Transaction) string {
	return tx.PaymentFormat
}

func (AvgPerPaymentFormatLogic) Zero() AvgPerPaymentFormatState {
	return AvgPerPaymentFormatState{}
}

func (AvgPerPaymentFormatLogic) Accumulate(state AvgPerPaymentFormatState, tx protocol.Transaction) AvgPerPaymentFormatState {
	state.Sum += tx.AmountPaid
	state.Count++
	return state
}

func (AvgPerPaymentFormatLogic) Finalize(key string, state AvgPerPaymentFormatState) []AvgPerPaymentFormatResult {
	if state.Count == 0 {
		return nil
	}
	avg := state.Sum / float64(state.Count)
	return []AvgPerPaymentFormatResult{{
		PaymentFormat: key,
		AvgAmount:     avg,
	}}
}
