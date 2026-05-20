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
	cfg := worker.AggregatorConfig{
		InstanceID:        mustEnvInt("INSTANCE_ID"),
		ConnSettings:      connSettings(),
		InputExchange:     mustEnv("INPUT_EXCHANGE"),
		InputKey:          mustEnv("INPUT_KEY"),
		OutputQueue:       mustEnv("OUTPUT_QUEUE"),
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
		aggregators.AvgPerPaymentFormatState,
		aggregators.AvgPerPaymentFormatResult,
	](cfg, extractor, aggregators.AvgPerPaymentFormatLogic{})
	if err != nil {
		log.Fatalf("[avg_per_payment_format] init error: %v", err)
	}
	defer workerInstance.Close()

	log.Printf("[avg_per_payment_format] started (instance %d)", cfg.InstanceID)
	workerInstance.Run()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[avg_per_payment_format] env var %s is required", key)
	}
	return v
}

func mustEnvInt(key string) int {
	v := mustEnv(key)
	value, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("[avg_per_payment_format] env var %s must be a number: %v", key, err)
	}
	return value
}

func connSettings() middleware.ConnSettings {
	return middleware.ConnSettings{
		Hostname: mustEnv("RABBITMQ_HOST"),
		Port:     mustEnvInt("RABBITMQ_PORT"),
	}
}
