package joiners

type fanInResult struct {
	MiddleBank    string `json:"middle_bank"`
	MiddleAccount string `json:"middle_account"`
	ToBank        string `json:"to_bank"`
	ToAccount     string `json:"to_account"`
}
