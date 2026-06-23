package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func publishCompactedCSV(stagingPath, tmpPath, finalPath, queryID string) error {
	in, err := os.Open(stagingPath)
	if err != nil {
		return fmt.Errorf("open staging file %s: %w", stagingPath, err)
	}
	defer in.Close()

	out, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create compacted temp file %s: %w", tmpPath, err)
	}

	reader := csv.NewReader(in)
	writer := csv.NewWriter(out)

	if _, err := reader.Read(); err != nil {
		_ = out.Close()
		return fmt.Errorf("read staging header %s: %w", stagingPath, err)
	}
	writeFinalHeaders(writer, queryID)

	seenRows := make(map[string]struct{})
	seenQ4Accounts := make(map[string]struct{})
	for {
		record, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			_ = out.Close()
			return fmt.Errorf("read staging record %s: %w", stagingPath, err)
		}
		if len(record) < 2 {
			continue
		}
		if isDuplicateStagingRow(seenRows, record[0], record[1]) {
			continue
		}
		row := record[2:]
		if queryID == "4" {
			if len(row) < 2 {
				continue
			}
			accountKey := row[0] + "|" + row[1]
			if _, ok := seenQ4Accounts[accountKey]; ok {
				continue
			}
			seenQ4Accounts[accountKey] = struct{}{}
		}
		if err := writer.Write(row); err != nil {
			_ = out.Close()
			return fmt.Errorf("write compacted record %s: %w", tmpPath, err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		_ = out.Close()
		return fmt.Errorf("flush compacted temp file %s: %w", tmpPath, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("sync compacted temp file %s: %w", tmpPath, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close compacted temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("replace compacted file %s: %w", finalPath, err)
	}

	dir, err := os.Open(filepath.Dir(finalPath))
	if err != nil {
		return fmt.Errorf("open compacted file dir: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync compacted file dir: %w", err)
	}
	return nil
}

func writeFinalHeaders(w *csv.Writer, queryID string) {
	switch queryID {
	case "1":
		w.Write([]string{"From Bank", "Account", "To Bank", "Account.1", "Amount Paid"})
	case "2":
		w.Write([]string{"From Bank", "Account", "Bank Name", "Amount Paid"})
	case "3":
		w.Write([]string{"From Bank", "Account", "Payment Format", "Amount Paid"})
	case "4":
		w.Write([]string{"Bank", "Account"})
	case "5":
		w.Write([]string{"quantity"})
	}
}

func isDuplicateStagingRow(seenRows map[string]struct{}, batchID, rowNumber string) bool {
	if batchID == "" {
		return false
	}
	key := batchID + "|" + rowNumber
	if _, ok := seenRows[key]; ok {
		return true
	}
	seenRows[key] = struct{}{}
	return false
}
