package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
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

	addr := fmt.Sprintf("%s:%s", gatewayHost, gatewayPort)
	conn := dialWithRetry(addr, 10, 2*time.Second)
	defer conn.Close()

	accountsFile := envOrDefault("ACCOUNTS_FILE", "LI-Medium_accounts.csv")
	transactionsFile := envOrDefault("TRANSACTIONS_FILE", "LI-Medium_Trans.csv")

	if err := sendAccounts(conn, inputDir+"/"+accountsFile, batchSize, clientID); err != nil {
		log.Fatalf("sending accounts: %v", err)
	}

	if err := sendTransactions(conn, inputDir+"/"+transactionsFile, batchSize, clientID); err != nil {
		log.Fatalf("sending transactions: %v", err)
	}

	eofID := eofBatchID(clientID)
	if err := protocol.Send(conn, protocol.Batch{Type: protocol.BatchTypeEOF, ClientID: clientID, BatchID: eofID}); err != nil {
		log.Fatalf("sending EOF: %v", err)
	}
	if err := waitForACK(conn, eofID); err != nil {
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

func sendAccounts(conn net.Conn, path string, batchSize int, clientID string) error {
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
			if err := flushAccounts(conn, buf, clientID, batchIndex); err != nil {
				return err
			}
			batchIndex++
			total += len(buf)
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		if err := flushAccounts(conn, buf, clientID, batchIndex); err != nil {
			return err
		}
		total += len(buf)
	}
	log.Printf("sent %d accounts", total)
	return nil
}

func flushAccounts(conn net.Conn, accounts []protocol.Account, clientID string, batchIndex int) error {
	batchID := deterministicBatchID(clientID, protocol.BatchTypeAccounts, batchIndex)
	if err := protocol.Send(conn, protocol.Batch{
		Type:     protocol.BatchTypeAccounts,
		ClientID: clientID,
		Accounts: accounts,
		BatchID:  batchID,
	}); err != nil {
		return err
	}
	return waitForACK(conn, batchID)
}

func sendTransactions(conn net.Conn, path string, batchSize int, clientID string) error {
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
			if err := flushTransactions(conn, buf, clientID, batchIndex); err != nil {
				return err
			}
			batchIndex++
			total += len(buf)
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		if err := flushTransactions(conn, buf, clientID, batchIndex); err != nil {
			return err
		}
		total += len(buf)
	}
	log.Printf("sent %d transactions", total)
	return nil
}

func flushTransactions(conn net.Conn, txns []protocol.Transaction, clientID string, batchIndex int) error {
	batchID := deterministicBatchID(clientID, protocol.BatchTypeTransactions, batchIndex)
	if err := protocol.Send(conn, protocol.Batch{
		Type:         protocol.BatchTypeTransactions,
		ClientID:     clientID,
		Transactions: txns,
		BatchID:      batchID,
	}); err != nil {
		return err
	}
	return waitForACK(conn, batchID)
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
