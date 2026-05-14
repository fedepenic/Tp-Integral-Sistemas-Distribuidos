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
		handleClient(conn)
	}
}

func handleClient(conn net.Conn) {
	defer conn.Close()

	totalAccounts := 0
	totalTransactions := 0

	for {
		batch, err := protocol.Receive(conn)
		if err != nil {
			if err == io.EOF {
				log.Printf("client %s disconnected", conn.RemoteAddr())
			} else {
				log.Printf("receive error from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}

		switch batch.Type {
		case protocol.BatchTypeAccounts:
			totalAccounts += len(batch.Accounts)
			log.Printf("[accounts] batch of %d received (running total: %d)", len(batch.Accounts), totalAccounts)

		case protocol.BatchTypeTransactions:
			totalTransactions += len(batch.Transactions)
			log.Printf("[transactions] batch of %d received (running total: %d)", len(batch.Transactions), totalTransactions)

		case protocol.BatchTypeEOF:
			log.Printf("[eof] stream complete — accounts=%d transactions=%d", totalAccounts, totalTransactions)
			if err := protocol.Send(conn, protocol.Batch{Type: protocol.BatchTypeACK}); err != nil {
				log.Printf("send ack: %v", err)
			}
			return
		}
	}
}
