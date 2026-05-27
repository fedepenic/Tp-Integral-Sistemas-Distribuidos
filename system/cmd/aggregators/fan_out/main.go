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
	outputKeyPrefix, outputPartitions := outputRoutingConfig()
	cfg := worker.AggregatorConfig{
		InstanceID:        mustEnvInt("INSTANCE_ID"),
		ConnSettings:      connSettings(),
		InputExchange:     mustEnv("INPUT_EXCHANGE"),
		InputKey:          mustEnv("INPUT_KEY"),
		OutputQueue:       outputQueue,
		OutputExchange:    outputExchange,
		OutputKey:         outputKey,
		OutputKeyPrefix:   outputKeyPrefix,
		OutputPartitions:  outputPartitions,
		ControlExchange:   mustEnv("EOF_CONTROL_EXCHANGE"),
		ControlKey:        mustEnv("EOF_CONTROL_KEY"),
		UpstreamInstances: mustEnvInt("UPSTREAM_INSTANCES"),
		DataType:          "fanout_result",
	}

	extractor := func(batch protocol.Batch) ([]protocol.Transaction, bool) {
		if batch.Type != protocol.BatchTypeTransactions {
			return nil, false
		}
		return batch.Transactions, true
	}

	resultKey := func(res aggregators.FanOutResult) string {
		return res.MiddleAccount
	}

	workerInstance, err := worker.NewAggregatorWorker[
		protocol.Transaction,
		aggregators.AccountRef,
		aggregators.FanOutState,
		aggregators.FanOutResult,
	](cfg, extractor, aggregators.FanOutLogic{}, resultKey)
	if err != nil {
		log.Fatalf("[fan_out] init error: %v", err)
	}
	defer workerInstance.Close()

	log.Printf("[fan_out] started (instance %d)", cfg.InstanceID)
	workerInstance.Run()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[fan_out] env var %s is required", key)
	}
	return v
}

func mustEnvInt(key string) int {
	v := mustEnv(key)
	value, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("[fan_out] env var %s must be a number: %v", key, err)
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
		log.Fatalf("[fan_out] env var OUTPUT_QUEUE or OUTPUT_EXCHANGE is required")
	}
	return queue, exchange, key
}

func outputRoutingConfig() (string, int) {
	return mustEnv("OUTPUT_KEY_PREFIX"), mustEnvInt("OUTPUT_PARTITIONS")
}
