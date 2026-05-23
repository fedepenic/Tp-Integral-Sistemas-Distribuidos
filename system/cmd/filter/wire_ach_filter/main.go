package main

import (
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Wire/ACH Filter
//
// Entrada:
//   - Queue: period1_for_q5
//
// Condición: PaymentFormat == "Wire" OR PaymentFormat == "ACH"
//
// Salida:
//   - Queue: wireach_txn  (sin routing key)
//
// EOF:
//   - Entrada: exchange "eof_period1_for_q5", key "wireach_filter"
//   - Salida:  exchange "eof_wireach_txn",    key ""

func main() {
	conn := connSettings()

	inputMW, err := middleware.NewQueueMiddleware("period1_for_q5", conn)
	if err != nil {
		log.Fatalf("[wireach_filter] input queue: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.NewQueueMiddleware("wireach_txn", conn)
	if err != nil {
		log.Fatalf("[wireach_filter] output queue: %v", err)
	}
	defer outputMW.Close()

	eofInMW, err := middleware.NewExchangeMiddleware(
		"eof_period1_for_q5",
		[]string{"wireach_filter"},
		conn,
	)
	if err != nil {
		log.Fatalf("[wireach_filter] eof input exchange: %v", err)
	}
	defer eofInMW.Close()

	eofOutMW, err := middleware.NewExchangeMiddleware("eof_wireach_txn", []string{""}, conn)
	if err != nil {
		log.Fatalf("[wireach_filter] eof output exchange: %v", err)
	}
	defer eofOutMW.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[wireach_filter] SIGTERM — shutting down")
		inputMW.StopConsuming()
		eofInMW.StopConsuming()
	}()

	worker := filterworker.NewWorker(
		func(t protocol.Transaction) bool {
			return t.PaymentFormat == "Wire" || t.PaymentFormat == "ACH"
		},
		[]*filterworker.Output{
			{Middleware: outputMW, GetKey: nil, EOFMiddleware: eofOutMW},
		},
		inputMW,
		eofInMW,
		upstreamCount(),
	)

	worker.Run()
}

func connSettings() middleware.ConnSettings {
	port, err := strconv.Atoi(mustEnv("RABBITMQ_PORT"))
	if err != nil {
		log.Fatalf("[wireach_filter] RABBITMQ_PORT must be a number: %v", err)
	}
	return middleware.ConnSettings{Hostname: mustEnv("RABBITMQ_HOST"), Port: port}
}

func upstreamCount() int {
	n, err := strconv.Atoi(mustEnv("UPSTREAM_INSTANCES"))
	if err != nil || n < 1 {
		log.Fatalf("[wireach_filter] UPSTREAM_INSTANCES must be a positive integer: %v", err)
	}
	return n
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[wireach_filter] env var %s is required", key)
	}
	return v
}
