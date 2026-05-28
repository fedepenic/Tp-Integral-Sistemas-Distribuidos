package main

import (
	"log"
	"os"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/joiners"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
)

func main() {
	conn := connSettings()

	inputQueue := mustEnv("INPUT_QUEUE_NAME")
	inputKey := mustEnv("INPUT_KEY")
	avgInputExchange := mustEnv("AVG_INPUT_EXCHANGE")
	txnInputExchange := mustEnv("TXN_INPUT_EXCHANGE")

	outputQueue := os.Getenv("OUTPUT_QUEUE")
	outputExchange := os.Getenv("OUTPUT_EXCHANGE")
	outputKey := os.Getenv("OUTPUT_KEY")
	if outputQueue == "" && outputExchange == "" {
		log.Fatalf("[join_q3] OUTPUT_QUEUE or OUTPUT_EXCHANGE required")
	}

	controlExchange := mustEnv("EOF_CONTROL_EXCHANGE")
	controlKey := mustEnv("EOF_CONTROL_KEY")

	avgUpstream := mustEnvInt("AVG_UPSTREAM_INSTANCES")
	txnUpstream := mustEnvInt("TXN_UPSTREAM_INSTANCES")

	inputMW, err := middleware.NewSharedQueueMultiExchangeMiddleware(
		inputQueue,
		[]string{avgInputExchange, txnInputExchange},
		[]string{inputKey},
		conn,
	)
	if err != nil {
		log.Fatalf("[join_q3] input middleware: %v", err)
	}
	defer inputMW.Close()

	var outputMW middleware.Middleware
	if outputExchange != "" {
		outputMW, err = middleware.NewExchangeMiddleware(
			outputExchange,
			[]string{outputKey},
			conn,
		)
	} else {
		outputMW, err = middleware.NewQueueMiddleware(
			outputQueue,
			conn,
		)
	}
	if err != nil {
		log.Fatalf("[join_q3] output middleware: %v", err)
	}
	defer outputMW.Close()

	controlPub, err := middleware.NewExchangeMiddleware(
		controlExchange,
		[]string{controlKey},
		conn,
	)
	if err != nil {
		log.Fatalf("[join_q3] control publisher: %v", err)
	}
	defer controlPub.Close()

	controlSub, err := middleware.NewExchangeMiddleware(
		controlExchange,
		[]string{controlKey},
		conn,
	)
	if err != nil {
		log.Fatalf("[join_q3] control subscriber: %v", err)
	}
	defer controlSub.Close()

	node := joiners.NewJoinQ3(
		inputMW,
		outputMW,
		controlPub,
		controlSub,
		avgUpstream,
		txnUpstream,
	)
	defer node.Close()

	log.Printf(
		"[join_q3] started queue=%s key=%s avg_exchange=%s txn_exchange=%s avg_upstream=%d txn_upstream=%d",
		inputQueue,
		inputKey,
		avgInputExchange,
		txnInputExchange,
		avgUpstream,
		txnUpstream,
	)

	node.Run()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[join_q3] env var %s is required", key)
	}
	return v
}

func mustEnvInt(key string) int {
	v := mustEnv(key)
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("[join_q3] env var %s must be int: %v", key, err)
	}
	return n
}

func connSettings() middleware.ConnSettings {
	return middleware.ConnSettings{
		Hostname: mustEnv("RABBITMQ_HOST"),
		Port:     mustEnvInt("RABBITMQ_PORT"),
	}
}
