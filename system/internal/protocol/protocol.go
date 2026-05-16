package protocol

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
)

type BatchType string

const (
	BatchTypeTransactions BatchType = "transactions"
	BatchTypeAccounts     BatchType = "accounts"
	BatchTypeEOF          BatchType = "eof"
	BatchTypeACK          BatchType = "ack"
)

type Transaction struct {
	Timestamp         string  `json:"timestamp"`
	FromBank          string  `json:"from_bank"`
	FromAccount       string  `json:"from_account"`
	ToBank            string  `json:"to_bank"`
	ToAccount         string  `json:"to_account"`
	AmountReceived    float64 `json:"amount_received"`
	ReceivingCurrency string  `json:"receiving_currency"`
	AmountPaid        float64 `json:"amount_paid"`
	PaymentCurrency   string  `json:"payment_currency"`
	PaymentFormat     string  `json:"payment_format"`
	IsLaundering      int     `json:"is_laundering"`
}

type Account struct {
	BankName      string `json:"bank_name"`
	BankID        string `json:"bank_id"`
	AccountNumber string `json:"account_number"`
	EntityID      string `json:"entity_id"`
	EntityName    string `json:"entity_name"`
}

type Batch struct {
	Type         BatchType     `json:"type"`
	ClientID     string        `json:"client_id,omitempty"`
	Transactions []Transaction `json:"transactions,omitempty"`
	Accounts     []Account     `json:"accounts,omitempty"`
}

func Send(conn net.Conn, batch Batch) error {
	data, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	length := uint32(len(data))
	if err := binary.Write(conn, binary.BigEndian, length); err != nil {
		return err
	}
	_, err = conn.Write(data)
	return err
}

func Receive(conn net.Conn) (Batch, error) {
	var length uint32
	if err := binary.Read(conn, binary.BigEndian, &length); err != nil {
		return Batch{}, err
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return Batch{}, err
	}
	var batch Batch
	if err := json.Unmarshal(data, &batch); err != nil {
		return Batch{}, err
	}
	return batch, nil
}
