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
		[]string{mustEnv("FO_INPUT_EXCHANGE"), mustEnv("FI_INPUT_EXCHANGE")},
		[]string{mustEnv("INPUT_KEY")},
		conn,
	)
	if err != nil {
		log.Fatalf("[joiner_sg] input middleware: %v", err)
	}
	defer inputMW.Close()

	outputKeyPrefix := mustEnv("OUTPUT_KEY_PREFIX")
	outputPartitions := mustEnvInt("OUTPUT_PARTITIONS")

	outputMW, err := middleware.NewExchangePublisherMiddleware(
		mustEnv("OUTPUT_EXCHANGE"),
		[]string{},
		conn,
	)
	if err != nil {
		log.Fatalf("[joiner_sg] output middleware: %v", err)
	}
	defer outputMW.Close()

	foUpstream := mustEnvInt("FO_UPSTREAM_INSTANCES")
	fiUpstream := mustEnvInt("FI_UPSTREAM_INSTANCES")

	classify := func(batch protocol.Batch) bool {
		return batch.DataType == "fanout_result"
	}

	svc := node.NewJoin("joiner_sg", foUpstream, fiUpstream, classify)

	log.Printf("[joiner_sg] started fo_upstream=%d fi_upstream=%d output_partitions=%d", foUpstream, fiUpstream, outputPartitions)

	svc.Run(inputMW, outputMW, newProcess(outputMW, outputKeyPrefix, outputPartitions))
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[joiner_sg] env var %s is required", key)
	}
	return v
}

func mustEnvInt(key string) int {
	v := mustEnv(key)
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("[joiner_sg] env var %s must be int: %v", key, err)
	}
	return n
}

func connSettings() middleware.ConnSettings {
	return middleware.ConnSettings{
		Hostname: mustEnv("RABBITMQ_HOST"),
		Port:     mustEnvInt("RABBITMQ_PORT"),
	}
}
