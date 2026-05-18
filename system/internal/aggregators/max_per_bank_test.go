package aggregators

import (
	"testing"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func TestMaxPerBankLogicAccumulate(t *testing.T) {
	logic := MaxPerBankLogic{}
	state := logic.Zero()

	tx1 := protocol.Transaction{FromBank: "BankA", FromAccount: "A-1", AmountPaid: 10}
	tx2 := protocol.Transaction{FromBank: "BankA", FromAccount: "A-2", AmountPaid: 25}
	tx3 := protocol.Transaction{FromBank: "BankA", FromAccount: "A-3", AmountPaid: 5}

	state = logic.Accumulate(state, tx1)
	state = logic.Accumulate(state, tx2)
	state = logic.Accumulate(state, tx3)

	res := logic.Finalize("BankA", state)
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if res[0].SourceAccount != "A-2" || res[0].MaxAmountUSD != 25 {
		t.Fatalf("unexpected result: %+v", res[0])
	}
}
