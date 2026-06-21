package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/health"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func main() {
	health.StartIfEnabled()

	gatewayHost := envOrDefault("GATEWAY_HOST", "gateway")
	gatewayPort := envOrDefault("GATEWAY_PORT", "8080")
	inputDir := envOrDefault("INPUT_DIR", "/data")
	clientID := envOrDefault("INSTANCE_ID", "unknown")
	batchSize := envIntOrDefault("BATCH_SIZE", 1000)
	progressPath := envOrDefault("CLIENT_PROGRESS_FILE", filepath.Join(inputDir, fmt.Sprintf(".%s.progress", clientID)))

	addr := fmt.Sprintf("%s:%s", gatewayHost, gatewayPort)
	sender, err := newClientSender(addr, progressPath)
	if err != nil {
		log.Fatalf("loading client progress: %v", err)
	}
	defer sender.close()

	accountsFile := envOrDefault("ACCOUNTS_FILE", "LI-Medium_accounts.csv")
	transactionsFile := envOrDefault("TRANSACTIONS_FILE", "LI-Medium_Trans.csv")

	if err := sendAccounts(sender, inputDir+"/"+accountsFile, batchSize, clientID); err != nil {
		log.Fatalf("sending accounts: %v", err)
	}

	if err := sendTransactions(sender, inputDir+"/"+transactionsFile, batchSize, clientID); err != nil {
		log.Fatalf("sending transactions: %v", err)
	}

	eofID := eofBatchID(clientID)
	if _, err := sender.sendAndWait(protocol.Batch{Type: protocol.BatchTypeEOF, ClientID: clientID, BatchID: eofID}); err != nil {
		log.Fatalf("waiting for EOF ack: %v", err)
	}

	log.Println("all data sent and acknowledged")
}

func dialWithRetry(addr string, maxRetries int, delay time.Duration) net.Conn {
	for i := 1; i <= maxRetries; i++ {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			log.Printf("connected to gateway at %s", addr)
			return conn
		}
		log.Printf("attempt %d/%d: connecting to %s: %v", i, maxRetries, addr, err)
		time.Sleep(delay)
	}
	log.Fatalf("could not connect to gateway at %s after %d attempts", addr, maxRetries)
	return nil
}

type clientSender struct {
	addr       string
	progress   *clientProgress
	conn       net.Conn
	maxRetries int
	retryDelay time.Duration
}

func newClientSender(addr string, progressPath string) (*clientSender, error) {
	progress, err := loadClientProgress(progressPath)
	if err != nil {
		return nil, err
	}

	sender := &clientSender{
		addr:       addr,
		progress:   progress,
		maxRetries: envIntOrDefault("CLIENT_RECONNECT_RETRIES", 30),
		retryDelay: envDurationOrDefault("CLIENT_RECONNECT_DELAY", 2*time.Second),
	}
	sender.reconnect()
	return sender, nil
}

func (s *clientSender) sendAndWait(batch protocol.Batch) (bool, error) {
	if s.progress.isACKed(batch.BatchID) {
		log.Printf("skipping already acknowledged batch %s", batch.BatchID)
		return false, nil
	}

	for {
		if s.conn == nil {
			s.reconnect()
		}
		if err := protocol.Send(s.conn, batch); err != nil {
			log.Printf("send batch %s failed: %v; reconnecting", batch.BatchID, err)
			s.resetConn()
			continue
		}
		if err := waitForACK(s.conn, batch.BatchID); err != nil {
			log.Printf("ack for batch %s failed: %v; reconnecting and resending", batch.BatchID, err)
			s.resetConn()
			continue
		}
		if err := s.progress.markACKed(batch.BatchID); err != nil {
			return false, err
		}
		return true, nil
	}
}

func (s *clientSender) reconnect() {
	s.conn = dialWithRetry(s.addr, s.maxRetries, s.retryDelay)
}

func (s *clientSender) resetConn() {
	if s.conn != nil {
		if err := s.conn.Close(); err != nil {
			log.Printf("closing connection: %v", err)
		}
	}
	s.conn = nil
}

func (s *clientSender) close() {
	s.resetConn()
}

type clientProgress struct {
	path           string
	lastACKedBatch string
}

func loadClientProgress(path string) (*clientProgress, error) {
	progress := &clientProgress{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return progress, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read progress file %s: %w", path, err)
	}

	progress.lastACKedBatch = strings.TrimSpace(string(data))
	if progress.lastACKedBatch != "" {
		log.Printf("resuming after acknowledged batch %s", progress.lastACKedBatch)
	}
	return progress, nil
}

func (p *clientProgress) isACKed(batchID string) bool {
	if p.lastACKedBatch == "" {
		return false
	}

	batch, ok := parseBatchPosition(batchID)
	if !ok {
		return false
	}
	lastACKed, ok := parseBatchPosition(p.lastACKedBatch)
	if !ok {
		return false
	}
	return batch.compare(lastACKed) <= 0
}

func (p *clientProgress) markACKed(batchID string) error {
	if batchID == "" {
		return nil
	}
	if p.isACKed(batchID) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(p.path), 0755); err != nil {
		return fmt.Errorf("create progress dir: %w", err)
	}
	tmpPath := p.path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(batchID+"\n"), 0644); err != nil {
		return fmt.Errorf("write progress file: %w", err)
	}
	if err := os.Rename(tmpPath, p.path); err != nil {
		return fmt.Errorf("replace progress file: %w", err)
	}
	p.lastACKedBatch = batchID
	return nil
}

type batchPosition struct {
	clientID string
	stage    int
	index    int
}

func parseBatchPosition(batchID string) (batchPosition, bool) {
	parts := strings.Split(batchID, ":")
	if len(parts) < 3 || parts[0] != "client" {
		return batchPosition{}, false
	}

	pos := batchPosition{clientID: parts[1]}
	switch {
	case len(parts) == 3 && parts[2] == "eof":
		pos.stage = 2
		return pos, true
	case len(parts) == 4 && parts[2] == string(protocol.BatchTypeAccounts):
		pos.stage = 0
	case len(parts) == 4 && parts[2] == string(protocol.BatchTypeTransactions):
		pos.stage = 1
	default:
		return batchPosition{}, false
	}

	index, err := strconv.Atoi(parts[3])
	if err != nil {
		return batchPosition{}, false
	}
	pos.index = index
	return pos, true
}

func (p batchPosition) compare(other batchPosition) int {
	if p.clientID != other.clientID {
		return 1
	}
	if p.stage != other.stage {
		return p.stage - other.stage
	}
	return p.index - other.index
}

func sendAccounts(sender *clientSender, path string, batchSize int, clientID string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	if _, err := r.Read(); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	var buf []protocol.Account
	total := 0
	batchIndex := 0

	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read row: %w", err)
		}
		if len(row) < 5 {
			continue
		}
		buf = append(buf, protocol.Account{
			BankName:      row[0],
			BankID:        row[1],
			AccountNumber: row[2],
			EntityID:      row[3],
			EntityName:    row[4],
		})
		if len(buf) >= batchSize {
			sent, err := flushAccounts(sender, buf, clientID, batchIndex)
			if err != nil {
				return err
			}
			batchIndex++
			if sent {
				total += len(buf)
			}
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		sent, err := flushAccounts(sender, buf, clientID, batchIndex)
		if err != nil {
			return err
		}
		if sent {
			total += len(buf)
		}
	}
	log.Printf("sent %d accounts", total)
	return nil
}

func flushAccounts(sender *clientSender, accounts []protocol.Account, clientID string, batchIndex int) (bool, error) {
	batchID := deterministicBatchID(clientID, protocol.BatchTypeAccounts, batchIndex)
	return sender.sendAndWait(protocol.Batch{
		Type:     protocol.BatchTypeAccounts,
		ClientID: clientID,
		Accounts: accounts,
		BatchID:  batchID,
	})
}

func sendTransactions(sender *clientSender, path string, batchSize int, clientID string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	if _, err := r.Read(); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	var buf []protocol.Transaction
	total := 0
	batchIndex := 0

	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read row: %w", err)
		}
		if len(row) < 11 {
			continue
		}
		amtReceived, _ := strconv.ParseFloat(row[5], 64)
		amtPaid, _ := strconv.ParseFloat(row[7], 64)
		isLaundering, _ := strconv.Atoi(row[10])

		buf = append(buf, protocol.Transaction{
			Timestamp:         row[0],
			FromBank:          row[1],
			FromAccount:       row[2],
			ToBank:            row[3],
			ToAccount:         row[4],
			AmountReceived:    amtReceived,
			ReceivingCurrency: row[6],
			AmountPaid:        amtPaid,
			PaymentCurrency:   row[8],
			PaymentFormat:     row[9],
			IsLaundering:      isLaundering,
		})
		if len(buf) >= batchSize {
			sent, err := flushTransactions(sender, buf, clientID, batchIndex)
			if err != nil {
				return err
			}
			batchIndex++
			if sent {
				total += len(buf)
			}
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		sent, err := flushTransactions(sender, buf, clientID, batchIndex)
		if err != nil {
			return err
		}
		if sent {
			total += len(buf)
		}
	}
	log.Printf("sent %d transactions", total)
	return nil
}

func flushTransactions(sender *clientSender, txns []protocol.Transaction, clientID string, batchIndex int) (bool, error) {
	batchID := deterministicBatchID(clientID, protocol.BatchTypeTransactions, batchIndex)
	return sender.sendAndWait(protocol.Batch{
		Type:         protocol.BatchTypeTransactions,
		ClientID:     clientID,
		Transactions: txns,
		BatchID:      batchID,
	})
}

func waitForACK(conn net.Conn, batchID string) error {
	ack, err := protocol.Receive(conn)
	if err != nil {
		return fmt.Errorf("receive ack for batch %s: %w", batchID, err)
	}
	if ack.Type != protocol.BatchTypeACK {
		return fmt.Errorf("expected ack for batch %s, got %s", batchID, ack.Type)
	}
	if ack.BatchID != batchID {
		return fmt.Errorf("expected ack for batch %s, got ack for %s", batchID, ack.BatchID)
	}
	return nil
}

func deterministicBatchID(clientID string, batchType protocol.BatchType, batchIndex int) string {
	return fmt.Sprintf("client:%s:%s:%d", clientID, batchType, batchIndex)
}

func eofBatchID(clientID string) string {
	return fmt.Sprintf("client:%s:eof", clientID)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDurationOrDefault(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
