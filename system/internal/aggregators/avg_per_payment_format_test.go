package aggregators

import (
	"testing"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func TestAvgPerPaymentFormatLogic(t *testing.T) {
	logic := AvgPerPaymentFormatLogic{}
	state := logic.Zero()

	state = logic.Accumulate(state, protocol.Transaction{PaymentFormat: "Wire", AmountPaid: 10})
	state = logic.Accumulate(state, protocol.Transaction{PaymentFormat: "Wire", AmountPaid: 30})

	res := logic.Finalize("Wire", state)
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if res[0].AvgAmount != 20 {
		t.Fatalf("expected avg 20, got %v", res[0].AvgAmount)
	}
}
