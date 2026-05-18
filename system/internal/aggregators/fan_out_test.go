package aggregators

import (
	"testing"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func TestFanOutLogicThreshold(t *testing.T) {
	logic := FanOutLogic{}
	state := logic.Zero()
	key := AccountRef{Bank: "B1", Account: "A1"}

	for i := 0; i < scatterThreshold; i++ {
		state = logic.Accumulate(state, protocol.Transaction{
			FromBank:    key.Bank,
			FromAccount: key.Account,
			ToBank:      "TB",
			ToAccount:   "T" + string(rune('0'+i)),
		})
	}

	res := logic.Finalize(key, state)
	if len(res) != 0 {
		t.Fatalf("expected 0 results at threshold, got %d", len(res))
	}

	state = logic.Accumulate(state, protocol.Transaction{
		FromBank:    key.Bank,
		FromAccount: key.Account,
		ToBank:      "TB",
		ToAccount:   "T6",
	})

	res = logic.Finalize(key, state)
	if len(res) != scatterThreshold+1 {
		t.Fatalf("expected %d results, got %d", scatterThreshold+1, len(res))
	}
}
