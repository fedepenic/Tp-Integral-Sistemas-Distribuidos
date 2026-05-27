package worker

type ControlMessage struct {
	Type     string `json:"type"`
	ClientID string `json:"client_id"`
	SenderID int    `json:"sender_id"`
	Seq      int    `json:"seq"`
}

const (
	ControlTypeEOF  = "control_eof"
	defaultClientID = "default"
)
