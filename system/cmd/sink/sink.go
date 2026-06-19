package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/dedup"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func newProcess(queryID string) node.ProcessFunc {
	deduper := dedup.New()
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.BatchID != "" && batch.Type != protocol.BatchTypeEOF {
			if deduper.CheckAndMark(batch.BatchID) {
				log.Printf("[sink_%s] duplicate batch: %s", queryID, batch.BatchID)
				return protocol.Batch{}, false
			}
		}
		batch.QueryID = queryID
		if batch.Type == protocol.BatchTypeEOF {
			log.Printf("[sink_%s] EOF forwarding for client=%s", queryID, batch.ClientID)
		} else {
			log.Printf("[sink_%s] batch client=%s txns=%d", queryID, batch.ClientID, len(batch.Transactions))
		}
		return batch, true
	}
}
