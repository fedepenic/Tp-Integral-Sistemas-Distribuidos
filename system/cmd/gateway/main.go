package main

import (
	"io"
	"log"
	"net"
	"os"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func main() {
	port := os.Getenv("GATEWAY_PORT")
	if port == "" {
		port = "8080"
	}

	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("listen on port %s: %v", port, err)
	}
	defer ln.Close()
	log.Printf("gateway listening on :%s", port)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		log.Printf("client connected: %s", conn.RemoteAddr())
		go handleClient(conn)
	}
}

func handleClient(conn net.Conn) {
	defer conn.Close()

	clientID := "unknown"
	totalAccounts := 0
	totalTransactions := 0

	for {
		batch, err := protocol.Receive(conn)
		if err != nil {
			if err == io.EOF {
				log.Printf("client %s disconnected unexpectedly", clientID)
			} else {
				log.Printf("client %s receive error: %v", clientID, err)
			}
			return
		}

		if batch.ClientID != "" {
			clientID = batch.ClientID
		}

		switch batch.Type {
		case protocol.BatchTypeAccounts:
			totalAccounts += len(batch.Accounts)
			log.Printf("[client %s] accounts batch of %d (total: %d)", clientID, len(batch.Accounts), totalAccounts)

		case protocol.BatchTypeTransactions:
			totalTransactions += len(batch.Transactions)
			log.Printf("[client %s] transactions batch of %d (total: %d)", clientID, len(batch.Transactions), totalTransactions)

		case protocol.BatchTypeEOF:
			log.Printf("[client %s] finished — accounts=%d transactions=%d", clientID, totalAccounts, totalTransactions)
			if err := protocol.Send(conn, protocol.Batch{Type: protocol.BatchTypeACK}); err != nil {
				log.Printf("client %s send ack: %v", clientID, err)
			}
			return
		}
	}
}
