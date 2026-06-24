package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	instanceSuffixPattern = regexp.MustCompile(`:i\d+$`)
	chunkSuffixPattern    = regexp.MustCompile(`_chunk_\d+$`)
	joinerSuffixPattern   = regexp.MustCompile(`:\d+:\d+$`)
	hashBatchIDPattern    = regexp.MustCompile(`^[a-zA-Z0-9_]+-[^-]+-\d+-[0-9a-f]{16}$`)
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
	reader.FieldsPerRecord = -1
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
		if !isValidStagingRecord(record, queryID) {
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

func isValidStagingRecord(record []string, queryID string) bool {
	if len(record) != stagingColumnCount(queryID) {
		return false
	}
	if !isValidBatchID(record[0]) {
		return false
	}
	if _, err := strconv.Atoi(record[1]); err != nil {
		return false
	}
	return true
}

func stagingColumnCount(queryID string) int {
	switch queryID {
	case "1":
		return 7
	case "2", "3":
		return 6
	case "4":
		return 4
	case "5":
		return 3
	default:
		return -1
	}
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

func isValidBatchID(batchID string) bool {
	if batchID == "" {
		return false
	}
	if hashBatchIDPattern.MatchString(batchID) {
		return true
	}
	if isValidBaseBatchID(batchID) {
		return true
	}
	if stripped, ok := stripBatchIDSuffix(batchID); ok {
		return isValidBatchID(stripped)
	}
	return false
}

func stripBatchIDSuffix(batchID string) (string, bool) {
	for _, pattern := range []*regexp.Regexp{
		instanceSuffixPattern,
		chunkSuffixPattern,
		joinerSuffixPattern,
	} {
		loc := pattern.FindStringIndex(batchID)
		if loc != nil && loc[1] == len(batchID) {
			return batchID[:loc[0]], true
		}
	}
	return "", false
}

func isValidBaseBatchID(batchID string) bool {
	parts := strings.Split(batchID, ":")
	switch len(parts) {
	case 3:
		return parts[0] == "client" &&
			parts[1] != "" &&
			parts[2] == "eof"
	case 4:
		if parts[0] == "client" {
			return parts[1] != "" && isDataBatchType(parts[2]) && isInteger(parts[3])
		}
		return parts[0] != "" &&
			isInteger(parts[1]) &&
			parts[2] != "" &&
			parts[3] == "eof"
	case 5:
		if parts[0] == "" || parts[1] == "" {
			return false
		}
		if parts[4] == "eof" {
			return isInteger(parts[2]) && isInteger(parts[3])
		}
		return isInteger(parts[2]) && isInteger(parts[3]) && isInteger(parts[4])
	default:
		return false
	}
}

func isDataBatchType(batchType string) bool {
	switch batchType {
	case "transactions", "accounts":
		return true
	default:
		return false
	}
}

func isInteger(value string) bool {
	if value == "" {
		return false
	}
	_, err := strconv.Atoi(value)
	return err == nil
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
