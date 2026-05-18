package aggregators

const scatterThreshold = 5

type AccountRef struct {
	Bank    string
	Account string
}

func accountKey(ref AccountRef) string {
	return ref.Bank + "|" + ref.Account
}
