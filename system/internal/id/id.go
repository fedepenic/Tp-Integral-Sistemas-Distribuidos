package id

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func New() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func Child(parent string, partition int) string {
	return fmt.Sprintf("%s:%d",
		parent,
		partition,
	)
}

func Aggregator(nodeName string, clientID string, partition int, chunkCount int, instance int) string {
	return fmt.Sprintf("%s:%s:%d:%d:%d",
		nodeName,
		clientID,
		partition,
		chunkCount,
		instance,
	)
}

func AggregatorEOF(nodeName string, instance int, clientID string) string {
	return fmt.Sprintf("%s:%d:%s:%s",
		nodeName,
		instance,
		clientID,
		"eof",
	)
}

func HashBatchJoinSG(nodeName string, clientID string, partition int, items []protocol.ScatterGatherItem) string {
	payload := struct {
		Node      string                       `json:"node"`
		ClientID  string                       `json:"client_id"`
		Partition int                          `json:"partition"`
		Items     []protocol.ScatterGatherItem `json:"items"`
	}{
		Node:      nodeName,
		ClientID:  clientID,
		Partition: partition,
		Items:     items,
	}

	data, _ := json.Marshal(payload)

	hash := sha256.Sum256(data)

	return fmt.Sprintf(
		"%s-%s-%d-%s",
		nodeName,
		clientID,
		partition,
		hex.EncodeToString(hash[:8]),
	)
}
