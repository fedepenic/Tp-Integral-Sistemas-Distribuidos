package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

const disconnectedMessage = "Client has been disconnected"

type queryWriter struct {
	file          *os.File
	csv           *csv.Writer
	finalPath     string
	stagingPath   string
	tmpPath       string
	headerWritten bool
	closed        bool
}

// reporter consumes from the reports queue and writes results to CSV files,
// one file per (client, query) pair at {outputDir}/client_{clientID}/{queryID}_results.csv.
//
// It is also updated by abandonment timers, so accesses are serialized with mu.
type reporter struct {
	mu                  sync.Mutex
	outputDir           string
	writers             map[string]*queryWriter // key: clientID + "/" + queryID
	disconnectedClients map[string]bool
	eofs                *eofStore
}

func newReporter(outputDir string, eofs *eofStore) *reporter {
	return &reporter{
		outputDir:           outputDir,
		writers:             make(map[string]*queryWriter),
		disconnectedClients: make(map[string]bool),
		eofs:                eofs,
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
	stagingPath := filepath.Join(dir, ".query_"+queryID+".staging.csv")

	if err := repairStagingTail(stagingPath); err != nil {
		return nil, fmt.Errorf("repair staging file %s: %w", stagingPath, err)
	}

	stagingInfo, statErr := os.Stat(stagingPath)
	headerWritten := statErr == nil && stagingInfo.Size() > 0
	f, err := os.OpenFile(stagingPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open staging file %s: %w", stagingPath, err)
	}

	w := &queryWriter{
		file:          f,
		csv:           csv.NewWriter(f),
		finalPath:     path,
		stagingPath:   stagingPath,
		tmpPath:       path + ".tmp",
		headerWritten: headerWritten,
	}
	r.writers[key] = w
	return w, nil
}

func (r *reporter) closeWriter(clientID, queryID string) error {
	key := clientID + "/" + queryID
	w, ok := r.writers[key]
	if !ok {
		// No data arrived — still create an empty file with headers so the
		// comparison script finds a file even when the query has no results.
		w, err := r.writerFor(clientID, queryID)
		if err != nil {
			return fmt.Errorf("create empty file client %s query %s: %w", clientID, queryID, err)
		}
		defer delete(r.writers, key)
		if !w.headerWritten {
			writeStagingHeaders(w, queryID)
		}
		if err := closeAndPublish(w, queryID); err != nil {
			return fmt.Errorf("close empty file for client %s query %s: %w", clientID, queryID, err)
		}
		return nil
	}
	defer delete(r.writers, key)
	if err := closeAndPublish(w, queryID); err != nil {
		return fmt.Errorf("close file for client %s query %s: %w", clientID, queryID, err)
	}
	return nil
}

func (r *reporter) publishWriter(clientID, queryID string, w *queryWriter) error {
	key := clientID + "/" + queryID
	if err := closeStaging(w); err != nil {
		return err
	}
	if _, err := os.Stat(w.finalPath); err == nil {
		if err := publishCompactedCSV(w.stagingPath, w.tmpPath, w.finalPath, queryID); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat final output file %s: %w", w.finalPath, err)
	}
	delete(r.writers, key)
	return nil
}

func (r *reporter) markClientDisconnected(clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.disconnectedClients[clientID] = true
	r.closeClientWriters(clientID)
	if err := r.writeDisconnectedFiles(clientID); err != nil {
		log.Printf("reporter: write disconnected files for client %s: %v", clientID, err)
	}
}

func (r *reporter) closeClientWriters(clientID string) {
	for key, w := range r.writers {
		if filepath.Dir(key) != clientID {
			continue
		}
		if err := closeStaging(w); err != nil {
			log.Printf("reporter: close file for disconnected client %s: %v", clientID, err)
		}
		delete(r.writers, key)
	}
}

func (r *reporter) writeDisconnectedFiles(clientID string) error {
	dir := filepath.Join(r.outputDir, "client_"+clientID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove output dir %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output dir %s: %w", dir, err)
	}

	for _, queryID := range []string{"1", "2", "3", "4", "5"} {
		path := filepath.Join(dir, "query_"+queryID+".csv")
		tmpPath := path + ".tmp"
		f, err := os.Create(tmpPath)
		if err != nil {
			return fmt.Errorf("create disconnected query file %s: %w", tmpPath, err)
		}
		w := csv.NewWriter(f)
		w.Write([]string{disconnectedMessage})
		w.Flush()
		if closeErr := f.Close(); closeErr != nil {
			return fmt.Errorf("close disconnected query file %s: %w", path, closeErr)
		}
		if err := w.Error(); err != nil {
			return fmt.Errorf("write disconnected query file %s: %w", path, err)
		}
		if err := os.Rename(tmpPath, path); err != nil {
			return fmt.Errorf("replace disconnected query file %s: %w", path, err)
		}
	}

	return nil
}

func writeStagingHeaders(w *queryWriter, queryID string) {
	headers := []string{"batch_id", "row_number"}
	switch queryID {
	case "1":
		headers = append(headers, "From Bank", "Account", "To Bank", "Account.1", "Amount Paid")
	case "2":
		headers = append(headers, "From Bank", "Account", "Bank Name", "Amount Paid")
	case "3":
		headers = append(headers, "From Bank", "Account", "Payment Format", "Amount Paid")
	case "4":
		headers = append(headers, "Bank", "Account")
	case "5":
		headers = append(headers, "quantity")
	}
	w.csv.Write(headers)
	w.headerWritten = true
}

func (r *reporter) handle(msg middleware.Message, ack func(), nack func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("reporter: unmarshal batch: %v — discarding", err)
		ack()
		return
	}

	if r.disconnectedClients[batch.ClientID] {
		log.Printf("reporter: discarding batch for disconnected client %s query %s", batch.ClientID, batch.QueryID)
		ack()
		return
	}

	if batch.Type == protocol.BatchTypeEOF {
		eofID := reportEOFID(batch)
		closed, err := r.eofs.withUnseen(eofID, func() error {
			return r.closeWriter(batch.ClientID, batch.QueryID)
		})
		if err != nil {
			log.Printf("reporter: close/persist EOF %s: %v", eofID, err)
			nack()
			return
		}
		if !closed {
			log.Printf("reporter: duplicate EOF %s (client=%s query=%s)", eofID, batch.ClientID, batch.QueryID)
			ack()
			return
		}
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
		if err := r.writeCount(w, batch); err != nil {
			log.Printf("reporter: write count for client %s query %s: %v", batch.ClientID, batch.QueryID, err)
			nack()
			return
		}
	} else if batch.QueryID == "4" {
		log.Printf("reporter: q4 batch client=%s records=%d", batch.ClientID, len(batch.Records))
		if err := writeQ4Rows(w, batch); err != nil {
			log.Printf("reporter: write rows for client %s query %s: %v", batch.ClientID, batch.QueryID, err)
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

	if err := r.publishWriter(batch.ClientID, batch.QueryID, w); err != nil {
		log.Printf("reporter: publish file for client %s query %s: %v", batch.ClientID, batch.QueryID, err)
		nack()
		return
	}
	ack()
}

func reportEOFID(batch protocol.Batch) string {
	if batch.BatchID != "" {
		return "report:" + batch.ClientID + ":query:" + batch.QueryID + ":" + batch.BatchID
	}
	return "report:" + batch.ClientID + ":query:" + batch.QueryID + ":eof"
}

func closeStaging(w *queryWriter) error {
	if w.closed {
		return nil
	}
	w.csv.Flush()
	if err := w.csv.Error(); err != nil {
		_ = w.file.Close()
		w.closed = true
		return fmt.Errorf("flush staging file %s: %w", w.stagingPath, err)
	}
	if err := w.file.Close(); err != nil {
		w.closed = true
		return fmt.Errorf("close staging file %s: %w", w.stagingPath, err)
	}
	w.closed = true
	return nil
}

func repairStagingTail(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size == 0 {
		return nil
	}

	offset, err := lastCompleteLineOffset(file, size)
	if err != nil {
		return err
	}
	if offset == size {
		return nil
	}
	if err := file.Truncate(offset); err != nil {
		return err
	}
	return file.Sync()
}

func lastCompleteLineOffset(file *os.File, size int64) (int64, error) {
	const chunkSize int64 = 4096

	buffer := make([]byte, chunkSize)
	for end := size; end > 0; {
		start := end - chunkSize
		if start < 0 {
			start = 0
		}
		n, err := file.ReadAt(buffer[:end-start], start)
		if err != nil && err != io.EOF {
			return 0, err
		}
		if idx := bytes.LastIndexByte(buffer[:n], '\n'); idx >= 0 {
			return start + int64(idx) + 1, nil
		}
		end = start
	}
	return 0, nil
}

func closeAndPublish(w *queryWriter, queryID string) error {
	if err := closeStaging(w); err != nil {
		return err
	}
	return publishCompactedCSV(w.stagingPath, w.tmpPath, w.finalPath, queryID)
}

func (r *reporter) writeCount(w *queryWriter, batch protocol.Batch) error {
	if !w.headerWritten {
		writeStagingHeaders(w, "5")
	}
	w.csv.Write(rowWithMetadata(batch.BatchID, 0, []string{strconv.FormatInt(batch.Count, 10)}))
	return w.csv.Error()
}

func (r *reporter) writeRows(w *queryWriter, batch protocol.Batch) error {
	if len(batch.Transactions) > 0 {
		if !w.headerWritten {
			writeStagingHeaders(w, batch.QueryID)
		}
		for i, t := range batch.Transactions {
			w.csv.Write(rowWithMetadata(batch.BatchID, i, []string{
				t.FromBank,
				t.FromAccount,
				t.ToBank,
				t.ToAccount,
				strconv.FormatFloat(t.AmountPaid, 'f', -1, 64),
			}))
		}
	}

	if batch.DataType == "max_per_bank" && len(batch.Records) > 0 {
		var results []maxPerBankResult
		if err := json.Unmarshal(batch.Records, &results); err != nil {
			return fmt.Errorf("unmarshal max_per_bank records: %w", err)
		}
		if !w.headerWritten {
			writeStagingHeaders(w, batch.QueryID)
		}
		for i, res := range results {
			w.csv.Write(rowWithMetadata(batch.BatchID, i, []string{
				res.BankID,
				res.SourceAccount,
				res.BankName,
				strconv.FormatFloat(res.MaxAmountUSD, 'f', -1, 64),
			}))
		}
	}

	return w.csv.Error()
}

func rowWithMetadata(batchID string, rowNumber int, row []string) []string {
	out := make([]string, 0, len(row)+2)
	out = append(out, batchID, strconv.Itoa(rowNumber))
	out = append(out, row...)
	return out
}
