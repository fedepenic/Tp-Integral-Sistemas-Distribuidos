package main

import (
	"encoding/json"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// newProcess returns the counter's business logic: accumulate transaction
// counts per client, then on EOF emit a BatchTypeCount batch followed by
// the EOF. node.Node handles upstream EOF counting before calling this.
func newProcess(outputMW middleware.Middleware) node.ProcessFunc {
	txnCounts := make(map[string]int64)

	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type == protocol.BatchTypeTransactions {
			txnCounts[batch.ClientID] += int64(len(batch.Transactions))
			return protocol.Batch{}, false
		}

		if batch.Type == protocol.BatchTypeEOF {
			total := txnCounts[batch.ClientID]
			delete(txnCounts, batch.ClientID)
			log.Printf("[counter] client=%s total=%d — emitting count batch", batch.ClientID, total)

			// Send the count batch directly; the node will send the EOF via its normal path.
			sendBatch(outputMW, protocol.Batch{
				Type:     protocol.BatchTypeCount,
				ClientID: batch.ClientID,
				Count:    total,
			})

			return protocol.Batch{Type: protocol.BatchTypeEOF, ClientID: batch.ClientID}, true
		}

		return protocol.Batch{}, false
	}
}

func sendBatch(mw middleware.Middleware, batch protocol.Batch) {
	data, err := json.Marshal(batch)
	if err != nil {
		log.Printf("[counter] marshal batch: %v", err)
		return
	}
	if err := mw.Send(middleware.Message{Body: string(data)}); err != nil {
		log.Printf("[counter] send batch: %v", err)
	}
}
