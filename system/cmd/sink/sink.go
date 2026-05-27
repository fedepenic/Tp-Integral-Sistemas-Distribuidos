package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// newProcess returns the sink's business logic: stamp QueryID on every batch
// and forward it downstream. EOF counting is handled by node.Node.
func newProcess(queryID string) node.ProcessFunc {
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		batch.QueryID = queryID
		if batch.Type == protocol.BatchTypeEOF {
			log.Printf("[sink_%s] EOF forwarding for client=%s", queryID, batch.ClientID)
		} else {
			log.Printf("[sink_%s] batch client=%s txns=%d", queryID, batch.ClientID, len(batch.Transactions))
		}
		return batch, true
	}
}
