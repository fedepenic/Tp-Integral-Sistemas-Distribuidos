package main

import (
	"log"
	"os"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

func main() {
	conn := connSettings()

	inputMW, err := middleware.NewExchangeMiddleware(
		mustEnv("INPUT_EXCHANGE"),
		[]string{mustEnv("INPUT_KEY")},
		conn,
	)
	if err != nil {
		log.Fatalf("[join_q2] input middleware: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.NewQueueMiddleware(mustEnv("OUTPUT_QUEUE"), conn)
	if err != nil {
		log.Fatalf("[join_q2] output middleware: %v", err)
	}
	defer outputMW.Close()

	accountsUpstream := mustEnvInt("ACCOUNTS_UPSTREAM_INSTANCES")
	maxUpstream := mustEnvInt("MAX_PER_BANK_UPSTREAM_INSTANCES")

	classify := func(batch protocol.Batch) bool {
		return batch.DataType == "accounts" || batch.Type == protocol.BatchTypeAccounts
	}

	svc := node.NewJoin("join_q2", accountsUpstream, maxUpstream, classify)

	log.Printf("[join_q2] started accounts_upstream=%d max_upstream=%d", accountsUpstream, maxUpstream)

	svc.Run(inputMW, outputMW, newProcess())
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
