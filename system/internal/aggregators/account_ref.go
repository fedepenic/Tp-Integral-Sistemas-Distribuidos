package aggregators

type AccountRef struct {
	Bank    string
	Account string
}

func accountKey(ref AccountRef) string {
	return ref.Bank + "|" + ref.Account
}
