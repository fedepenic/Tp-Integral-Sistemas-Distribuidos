package main

import (
	"log"
	"os"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
)

func main() {
	conn := connSettings()

	inputExchange := mustEnv("INPUT_EXCHANGE")
	inputKey := mustEnv("INPUT_KEY")

	outputQueue := os.Getenv("OUTPUT_QUEUE")
	outputExchange := os.Getenv("OUTPUT_EXCHANGE")
	outputKey := os.Getenv("OUTPUT_KEY")

	if outputQueue == "" && outputExchange == "" {
		log.Fatalf("[join_q2] OUTPUT_QUEUE or OUTPUT_EXCHANGE required")
	}

	controlExchange := mustEnv("EOF_CONTROL_EXCHANGE")
	controlKey := mustEnv("EOF_CONTROL_KEY")

	accountsUpstream := mustEnvInt("ACCOUNTS_UPSTREAM_INSTANCES")
	maxUpstream := mustEnvInt("MAX_PER_BANK_UPSTREAM_INSTANCES")

	inputMW, err := middleware.NewExchangeMiddleware(
		inputExchange,
		[]string{inputKey},
		conn,
	)
	if err != nil {
		log.Fatalf("[join_q2] input middleware: %v", err)
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
		log.Fatalf("[join_q2] output middleware: %v", err)
	}
	defer outputMW.Close()

	controlPub, err := middleware.NewExchangeMiddleware(
		controlExchange,
		[]string{controlKey},
		conn,
	)
	if err != nil {
		log.Fatalf("[join_q2] control publisher: %v", err)
	}
	defer controlPub.Close()

	controlSub, err := middleware.NewExchangeMiddleware(
		controlExchange,
		[]string{controlKey},
		conn,
	)
	if err != nil {
		log.Fatalf("[join_q2] control subscriber: %v", err)
	}
	defer controlSub.Close()

	node := newJoinQ2(
		inputMW,
		outputMW,
		controlPub,
		controlSub,
		accountsUpstream,
		maxUpstream,
	)

	defer node.Close()

	log.Printf(
		"[join_q2] started accounts_upstream=%d max_upstream=%d",
		accountsUpstream,
		maxUpstream,
	)

	node.Run()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[join_q2] env var %s is required", key)
	}
	return v
}

func mustEnvInt(key string) int {
	v := mustEnv(key)

	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("[join_q2] env var %s must be int: %v", key, err)
	}

	return n
}

func connSettings() middleware.ConnSettings {
	return middleware.ConnSettings{
		Hostname: mustEnv("RABBITMQ_HOST"),
		Port:     mustEnvInt("RABBITMQ_PORT"),
	}
}
