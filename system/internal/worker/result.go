package worker

type ResultBatch[O any] struct {
	Type     string `json:"type"`
	ClientID string `json:"client_id"`
	Records  []O    `json:"records,omitempty"`
}

type ControlMessage struct {
	Type     string `json:"type"`
	ClientID string `json:"client_id"`
	SenderID int    `json:"sender_id"`
	Seq      int    `json:"seq"`
}

const (
	ResultTypeData  = "result"
	ResultTypeEOF   = "eof"
	ControlTypeEOF  = "control_eof"
	defaultClientID = "default"
)
