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
	avgPerFormatDataType = "avg_per_format"
	q3CandidatesDataType = "q3_candidates"
)

type JoinQ3State struct {
	ThresholdsByFormat map[string]float64
	PendingTxns        map[string][]protocol.Transaction
}

type JoinQ3Logic struct{}

func NewJoinQ3(
	inputMW, outputMW, controlPub, controlSub middleware.Middleware,
	avgUpstream, txnUpstream int,
) *worker.JoinerWorker[aggregators.AvgPerPaymentFormatResult, protocol.Transaction, JoinQ3State, protocol.Transaction] {
	cfg := worker.JoinerConfig[aggregators.AvgPerPaymentFormatResult, protocol.Transaction]{
		Name:                   "join_q3",
		Input:                  inputMW,
		Output:                 outputMW,
		ControlPub:             controlPub,
		ControlSub:             controlSub,
		ExtractLeft:            extractJoinQ3Averages,
		ExtractRight:           extractJoinQ3Transactions,
		LeftUpstreamInstances:  avgUpstream,
		RightUpstreamInstances: txnUpstream,
		OutputDataType:         q3CandidatesDataType,
	}

	return worker.NewJoinerWorker[
		aggregators.AvgPerPaymentFormatResult,
		protocol.Transaction,
		JoinQ3State,
		protocol.Transaction,
	](
		cfg,
		JoinQ3Logic{},
	)
}

func (JoinQ3Logic) Zero() JoinQ3State {
	return JoinQ3State{
		ThresholdsByFormat: make(map[string]float64),
		PendingTxns:        make(map[string][]protocol.Transaction),
	}
}

func (JoinQ3Logic) ProcessLeft(state JoinQ3State, avg aggregators.AvgPerPaymentFormatResult) (JoinQ3State, []protocol.Transaction) {
	threshold := avg.AvgAmount / 100.0
	state.ThresholdsByFormat[avg.PaymentFormat] = threshold

	queued := state.PendingTxns[avg.PaymentFormat]
	if len(queued) == 0 {
		return state, nil
	}

	out := make([]protocol.Transaction, 0, len(queued))
	for _, tx := range queued {
		if filtered, ok := belowJoinQ3Threshold(tx, threshold); ok {
			filtered.AvgForFormat = avg.AvgAmount
			out = append(out, filtered)
		}
	}

	delete(state.PendingTxns, avg.PaymentFormat)
	return state, out
}

func (JoinQ3Logic) ProcessRight(state JoinQ3State, tx protocol.Transaction) (JoinQ3State, []protocol.Transaction) {
	threshold, ok := state.ThresholdsByFormat[tx.PaymentFormat]
	if !ok {
		state.PendingTxns[tx.PaymentFormat] = append(state.PendingTxns[tx.PaymentFormat], tx)
		return state, nil
	}

	if filtered, ok := belowJoinQ3Threshold(tx, threshold); ok {
		filtered.AvgForFormat = threshold * 100.0
		return state, []protocol.Transaction{filtered}
	}

	return state, nil
}

func (JoinQ3Logic) Flush(state JoinQ3State) []protocol.Transaction {
	pending := 0
	for _, txns := range state.PendingTxns {
		pending += len(txns)
	}
	if pending > 0 {
		log.Printf("[join_q3] %d period2 transactions discarded at EOF (no average for payment_format)", pending)
	}
	return nil
}

func belowJoinQ3Threshold(tx protocol.Transaction, threshold float64) (protocol.Transaction, bool) {
	if tx.AmountPaid >= threshold {
		return protocol.Transaction{}, false
	}
	return tx, true
}

func extractJoinQ3Averages(batch protocol.Batch) ([]aggregators.AvgPerPaymentFormatResult, bool) {
	if batch.DataType != avgPerFormatDataType {
		return nil, false
	}
	if batch.Type == protocol.BatchTypeEOF || len(batch.Records) == 0 {
		return nil, true
	}

	var results []aggregators.AvgPerPaymentFormatResult
	if err := json.Unmarshal(batch.Records, &results); err != nil {
		log.Printf("[join_q3] malformed avg_per_format records: %v", err)
		return nil, true
	}
	return results, true
}

func extractJoinQ3Transactions(batch protocol.Batch) ([]protocol.Transaction, bool) {
	if batch.Type == protocol.BatchTypeEOF {
		if batch.DataType == "" || batch.DataType == q3CandidatesDataType {
			return nil, true
		}
		return nil, false
	}
	if batch.Type != protocol.BatchTypeTransactions {
		return nil, false
	}
	return batch.Transactions, true
}
