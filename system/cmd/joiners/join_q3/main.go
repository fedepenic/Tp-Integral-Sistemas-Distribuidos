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

	inputMW, err := middleware.NewSharedQueueMultiExchangeMiddleware(
		mustEnv("INPUT_QUEUE_NAME"),
		[]string{mustEnv("AVG_INPUT_EXCHANGE"), mustEnv("TXN_INPUT_EXCHANGE")},
		[]string{mustEnv("INPUT_KEY")},
		conn,
	)
	if err != nil {
		log.Fatalf("[join_q3] input middleware: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := buildOutputMW(conn)
	if err != nil {
		log.Fatalf("[join_q3] output middleware: %v", err)
	}
	defer outputMW.Close()

	avgUpstream := mustEnvInt("AVG_UPSTREAM_INSTANCES")
	txnUpstream := mustEnvInt("TXN_UPSTREAM_INSTANCES")

	classify := func(batch protocol.Batch) bool {
		return batch.DataType == avgPerFormatDataType
	}

	svc := node.NewJoin("join_q3", avgUpstream, txnUpstream, classify)

	log.Printf("[join_q3] started avg_upstream=%d txn_upstream=%d", avgUpstream, txnUpstream)

	svc.Run(inputMW, outputMW, newProcess())
}

func buildOutputMW(conn middleware.ConnSettings) (middleware.Middleware, error) {
	if exchange := os.Getenv("OUTPUT_EXCHANGE"); exchange != "" {
		return middleware.NewExchangeMiddleware(exchange, []string{os.Getenv("OUTPUT_KEY")}, conn)
	}
	return middleware.NewQueueMiddleware(mustEnv("OUTPUT_QUEUE"), conn)
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
