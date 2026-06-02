package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

type queryWriter struct {
	file          *os.File
	csv           *csv.Writer
	headerWritten bool
}

// reporter consumes from the reports queue and writes results to CSV files,
// one file per (client, query) pair at {outputDir}/client_{clientID}/{queryID}_results.csv.
//
// It is driven by a single goroutine (the middleware callback), so no locking
// is needed on the writers map.
type reporter struct {
	outputDir string
	writers   map[string]*queryWriter // key: clientID + "/" + queryID
}

func newReporter(outputDir string) *reporter {
	return &reporter{
		outputDir: outputDir,
		writers:   make(map[string]*queryWriter),
	}
}

func (r *reporter) writerFor(clientID, queryID string) (*queryWriter, error) {
	key := clientID + "/" + queryID
	if w, ok := r.writers[key]; ok {
		return w, nil
	}

	dir := filepath.Join(r.outputDir, "client_"+clientID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir %s: %w", dir, err)
	}

	path := filepath.Join(dir, "query_"+queryID+".csv")
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create output file %s: %w", path, err)
	}

	w := &queryWriter{file: f, csv: csv.NewWriter(f)}
	r.writers[key] = w
	return w, nil
}

func (r *reporter) closeWriter(clientID, queryID string) {
	key := clientID + "/" + queryID
	w, ok := r.writers[key]
	if !ok {
		return
	}
	w.csv.Flush()
	if err := w.file.Close(); err != nil {
		log.Printf("reporter: close file for client %s query %s: %v", clientID, queryID, err)
	}
	delete(r.writers, key)
}

func (r *reporter) handle(msg middleware.Message, ack func(), nack func()) {
	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("reporter: unmarshal batch: %v — discarding", err)
		ack()
		return
	}

	if batch.Type == protocol.BatchTypeEOF {
		r.closeWriter(batch.ClientID, batch.QueryID)
		log.Printf("reporter: query %s complete for client %s", batch.QueryID, batch.ClientID)
		ack()
		return
	}

	w, err := r.writerFor(batch.ClientID, batch.QueryID)
	if err != nil {
		log.Printf("reporter: open writer for client %s query %s: %v", batch.ClientID, batch.QueryID, err)
		nack()
		return
	}

	if batch.Type == protocol.BatchTypeCount {
		log.Printf("reporter: count received client=%s query=%s count=%d", batch.ClientID, batch.QueryID, batch.Count)
		if err := r.writeCount(w, batch.Count); err != nil {
			log.Printf("reporter: write count for client %s query %s: %v", batch.ClientID, batch.QueryID, err)
			nack()
			return
		}
	} else if batch.QueryID == "3" {
		log.Printf("reporter: q3 batch client=%s txns=%d", batch.ClientID, len(batch.Transactions))
		if err := writeQ3Rows(w, batch); err != nil {
			log.Printf("reporter: write rows for client %s query %s: %v", batch.ClientID, batch.QueryID, err)
			nack()
			return
		}
	} else {
		log.Printf("reporter: batch received client=%s query=%s txns=%d", batch.ClientID, batch.QueryID, len(batch.Transactions))
		if err := r.writeRows(w, batch); err != nil {
			log.Printf("reporter: write rows for client %s query %s: %v", batch.ClientID, batch.QueryID, err)
			nack()
			return
		}
	}

	w.csv.Flush()
	ack()
}

func (r *reporter) writeCount(w *queryWriter, count int64) error {
	if !w.headerWritten {
		w.csv.Write([]string{"quantity"})
		w.headerWritten = true
	}
	w.csv.Write([]string{strconv.FormatInt(count, 10)})
	return w.csv.Error()
}

func (r *reporter) writeRows(w *queryWriter, batch protocol.Batch) error {
	if len(batch.Transactions) > 0 {
		if !w.headerWritten {
			w.csv.Write([]string{
				"From Bank",
				"Account",
				"To Bank",
				"Account.1",
				"Amount Paid",
			})
			w.headerWritten = true
		}
		for _, t := range batch.Transactions {
			w.csv.Write([]string{
				t.FromBank,
				t.FromAccount,
				t.ToBank,
				t.ToAccount,
				strconv.FormatFloat(t.AmountPaid, 'f', -1, 64),
			})
		}
	}

	if batch.DataType == "max_per_bank" && len(batch.Records) > 0 {
		var results []maxPerBankResult
		if err := json.Unmarshal(batch.Records, &results); err != nil {
			return fmt.Errorf("unmarshal max_per_bank records: %w", err)
		}
		if !w.headerWritten {
			w.csv.Write([]string{"From Bank", "Account", "Bank Name", "Amount Paid"})
			w.headerWritten = true
		}
		for _, res := range results {
			w.csv.Write([]string{
				res.BankID,
				res.SourceAccount,
				res.BankName,
				strconv.FormatFloat(res.MaxAmountUSD, 'f', -1, 64),
			})
		}
	}

	return w.csv.Error()
}
