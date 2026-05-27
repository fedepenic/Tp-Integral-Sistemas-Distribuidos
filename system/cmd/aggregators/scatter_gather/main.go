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
		DataType:          "scatter_gather_result",
	}

	extractor := func(batch protocol.Batch) ([]protocol.ScatterGatherItem, bool) {
		if batch.Type != protocol.BatchTypeScatterGather {
			return nil, false
		}
		return batch.ScatterGatherItems, true
	}

	workerInstance, err := worker.NewAggregatorWorker[
		protocol.ScatterGatherItem,
		string,
		aggregators.ScatterGatherState,
		aggregators.ScatterGatherResult,
	](cfg, extractor, aggregators.ScatterGatherLogic{}, nil)
	if err != nil {
		log.Fatalf("[scatter_gather] init error: %v", err)
	}
	defer workerInstance.Close()

	log.Printf("[scatter_gather] started (instance %d)", cfg.InstanceID)
	workerInstance.Run()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[scatter_gather] env var %s is required", key)
	}
	return v
}

func mustEnvInt(key string) int {
	v := mustEnv(key)
	value, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("[scatter_gather] env var %s must be a number: %v", key, err)
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
		log.Fatalf("[scatter_gather] env var OUTPUT_QUEUE or OUTPUT_EXCHANGE is required")
	}
	return queue, exchange, key
}
