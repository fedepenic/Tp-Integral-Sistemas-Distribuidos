package aggregators

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

const scatterThreshold = 5

type ScatterGatherItem = protocol.ScatterGatherItem

type ScatterGatherState struct {
	Middles map[string]struct{}
}

type ScatterGatherResult struct {
	FromBank    string `json:"from_bank"`
	FromAccount string `json:"from_account"`
	ToBank      string `json:"to_bank"`
	ToAccount   string `json:"to_account"`
	TargetCount int    `json:"target_count"`
}

type ScatterGatherLogic struct{}

func (ScatterGatherLogic) Key(item ScatterGatherItem) string {
	return scatterGatherKey(item.FromBank, item.FromAccount, item.ToBank, item.ToAccount)
}

func (ScatterGatherLogic) Zero() ScatterGatherState {
	return ScatterGatherState{Middles: map[string]struct{}{}}
}

func (ScatterGatherLogic) Accumulate(state ScatterGatherState, item ScatterGatherItem) ScatterGatherState {
	if state.Middles == nil {
		state.Middles = map[string]struct{}{}
	}
	middleKey := scatterGatherMiddleKey(item.MiddleBank, item.MiddleAccount)
	state.Middles[middleKey] = struct{}{}
	return state
}

func (ScatterGatherLogic) Finalize(key string, state ScatterGatherState) []ScatterGatherResult {
	count := len(state.Middles)
	if count < scatterThreshold {
		return nil
	}
	fromBank, fromAccount, toBank, toAccount := parseScatterGatherKey(key)
	return []ScatterGatherResult{{
		FromBank:    fromBank,
		FromAccount: fromAccount,
		ToBank:      toBank,
		ToAccount:   toAccount,
		TargetCount: count,
	}}
}

func scatterGatherKey(fromBank, fromAccount, toBank, toAccount string) string {
	return fromBank + "|" + fromAccount + "|" + toBank + "|" + toAccount
}

func scatterGatherMiddleKey(middleBank, middleAccount string) string {
	return middleBank + "|" + middleAccount
}

func parseScatterGatherKey(key string) (string, string, string, string) {
	parts := splitScatterGatherKey(key)
	if len(parts) != 4 {
		return "", "", "", ""
	}
	return parts[0], parts[1], parts[2], parts[3]
}

func splitScatterGatherKey(key string) []string {
	parts := make([]string, 0, 4)
	start := 0
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			parts = append(parts, key[start:i])
			start = i + 1
			if len(parts) == 3 {
				break
			}
		}
	}
	parts = append(parts, key[start:])
	return parts
}
