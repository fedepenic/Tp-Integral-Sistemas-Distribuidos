package main

import (
	"log"
	"os"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/aggregators"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

func main() {
	outputQueue, outputExchange, outputKey := outputConfig()
	cfg := worker.AggregatorConfig{
		InstanceID:        mustEnvInt("INSTANCE_ID"),
		ConnSettings:      connSettings(),
		InputExchange:     mustEnv("INPUT_EXCHANGE"),
		InputKey:          mustEnv("INPUT_KEY"),
		OutputQueue:       outputQueue,
		OutputExchange:    outputExchange,
		OutputKey:         outputKey,
		ControlExchange:   mustEnv("EOF_CONTROL_EXCHANGE"),
		ControlKey:        mustEnv("EOF_CONTROL_KEY"),
		UpstreamInstances: mustEnvInt("UPSTREAM_INSTANCES"),
	}

	extractor := func(batch protocol.Batch) ([]protocol.Transaction, bool) {
		if batch.Type != protocol.BatchTypeTransactions {
			return nil, false
		}
		return batch.Transactions, true
	}

	workerInstance, err := worker.NewAggregatorWorker[
		protocol.Transaction,
		string,
		aggregators.MaxPerBankState,
		aggregators.MaxPerBankResult,
	](cfg, extractor, aggregators.MaxPerBankLogic{}, nil)
	if err != nil {
		log.Fatalf("[max_per_bank] init error: %v", err)
	}
	defer workerInstance.Close()

	log.Printf("[max_per_bank] started (instance %d)", cfg.InstanceID)
	workerInstance.Run()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[max_per_bank] env var %s is required", key)
	}
	return v
}

func mustEnvInt(key string) int {
	v := mustEnv(key)
	value, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("[max_per_bank] env var %s must be a number: %v", key, err)
	}
	return value
}

func connSettings() middleware.ConnSettings {
	return middleware.ConnSettings{
		Hostname: mustEnv("RABBITMQ_HOST"),
		Port:     mustEnvInt("RABBITMQ_PORT"),
	}
}

func outputConfig() (string, string, string) {
	queue := os.Getenv("OUTPUT_QUEUE")
	exchange := os.Getenv("OUTPUT_EXCHANGE")
	key := os.Getenv("OUTPUT_KEY")
	if queue == "" && exchange == "" {
		log.Fatalf("[max_per_bank] env var OUTPUT_QUEUE or OUTPUT_EXCHANGE is required")
	}
	return queue, exchange, key
}
