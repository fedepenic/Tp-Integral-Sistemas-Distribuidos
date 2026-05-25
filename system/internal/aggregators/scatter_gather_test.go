package aggregators

import "testing"

func TestScatterGatherLogicThreshold(t *testing.T) {
	logic := ScatterGatherLogic{}
	state := logic.Zero()
	item := ScatterGatherItem{
		FromBank:      "B1",
		FromAccount:   "A1",
		MiddleBank:    "MB",
		MiddleAccount: "M1",
		ToBank:        "TB",
		ToAccount:     "T1",
	}

	for i := 0; i < scatterThreshold-1; i++ {
		state = logic.Accumulate(state, ScatterGatherItem{
			FromBank:      item.FromBank,
			FromAccount:   item.FromAccount,
			MiddleBank:    item.MiddleBank,
			MiddleAccount: "M" + string(rune('A'+i)),
			ToBank:        item.ToBank,
			ToAccount:     item.ToAccount,
		})
	}

	key := logic.Key(item)
	res := logic.Finalize(key, state)
	if len(res) != 0 {
		t.Fatalf("expected 0 results below threshold, got %d", len(res))
	}

	state = logic.Accumulate(state, ScatterGatherItem{
		FromBank:      item.FromBank,
		FromAccount:   item.FromAccount,
		MiddleBank:    item.MiddleBank,
		MiddleAccount: "MZ",
		ToBank:        item.ToBank,
		ToAccount:     item.ToAccount,
	})

	res = logic.Finalize(key, state)
	if len(res) != 1 {
		t.Fatalf("expected 1 result at threshold, got %d", len(res))
	}
	if res[0].TargetCount != scatterThreshold {
		t.Fatalf("expected target count %d, got %d", scatterThreshold, res[0].TargetCount)
	}
}
