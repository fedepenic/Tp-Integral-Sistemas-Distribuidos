package aggregators

import (
	"testing"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func TestFanInLogicThreshold(t *testing.T) {
	logic := FanInLogic{}
	state := logic.Zero()
	key := AccountRef{Bank: "B1", Account: "A1"}

	records := 6
	for i := 0; i < records; i++ {
		state = logic.Accumulate(state, protocol.Transaction{
			ToBank:      key.Bank,
			ToAccount:   key.Account,
			FromBank:    "FB",
			FromAccount: "F" + string(rune('0'+i)),
		})
	}

	res := logic.Finalize(key, state)
	if len(res) != records {
		t.Fatalf("expected %d results, got %d", records, len(res))
	}
}
