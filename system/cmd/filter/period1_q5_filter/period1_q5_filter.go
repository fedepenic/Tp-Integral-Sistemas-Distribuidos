package main

import (
	"log"
	"strings"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

const dateLayout = "2006-01-02"

func newProcess() node.ProcessFunc {
	start, _ := time.Parse(dateLayout, "2022-09-01")
	end, _ := time.Parse(dateLayout, "2022-09-05")
	end = end.Add(24 * time.Hour)

	var seen, passed, parseErrs int

	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.Type != protocol.BatchTypeTransactions {
			return protocol.Batch{}, false
		}
		out := make([]protocol.Transaction, 0, len(batch.Transactions))
		for _, t := range batch.Transactions {
			seen++
			date := strings.ReplaceAll(t.Timestamp[:10], "/", "-")
			ts, err := time.Parse(dateLayout, date)
			if err != nil {
				parseErrs++
				if parseErrs <= 3 {
					log.Printf("[period1_q5_filter] parse error txn=%d timestamp=%q: %v", seen, t.Timestamp, err)
				}
				continue
			}
			ok := !ts.Before(start) && ts.Before(end)
			if ok {
				passed++
				if passed <= 3 || passed%5000 == 0 {
					log.Printf("[period1_q5_filter] pass #%d date=%s (seen=%d)", passed, date, seen)
				}
				out = append(out, t)
			} else if seen <= 5 {
				log.Printf("[period1_q5_filter] skip txn=%d date=%s (out of window)", seen, date)
			}
		}
		if len(out) == 0 {
			return protocol.Batch{}, false
		}
		return protocol.Batch{Type: batch.Type, ClientID: batch.ClientID, Transactions: out}, true
	}
}
