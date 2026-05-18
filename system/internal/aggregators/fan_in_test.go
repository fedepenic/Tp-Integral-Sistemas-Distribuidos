package aggregators

import (
	"testing"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func TestFanInLogicThreshold(t *testing.T) {
	logic := FanInLogic{}
	state := logic.Zero()
	key := AccountRef{Bank: "B2", Account: "C1"}

	for i := 0; i < scatterThreshold; i++ {
		state = logic.Accumulate(state, protocol.Transaction{
			FromBank:    "FB",
			FromAccount: "F" + string(rune('0'+i)),
			ToBank:      key.Bank,
			ToAccount:   key.Account,
		})
	}

	res := logic.Finalize(key, state)
	if len(res) != 0 {
		t.Fatalf("expected 0 results at threshold, got %d", len(res))
	}

	state = logic.Accumulate(state, protocol.Transaction{
		FromBank:    "FB",
		FromAccount: "F6",
		ToBank:      key.Bank,
		ToAccount:   key.Account,
	})

	res = logic.Finalize(key, state)
	if len(res) != scatterThreshold+1 {
		t.Fatalf("expected %d results, got %d", scatterThreshold+1, len(res))
	}
}
