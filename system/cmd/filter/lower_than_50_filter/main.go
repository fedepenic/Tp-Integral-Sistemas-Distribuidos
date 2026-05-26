package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Amount < 50 Filter (modo coordinated, escalable horizontalmente)
//
// Igual al usd_filter: shared input queue + broadcast interno de EOFs entre
// instancias del nivel + conteo per-cliente + drenado del in-flight antes de
// propagar.
//
// Entrada (data + EOFs):
//   - Shared queue (INPUT_QUEUE_NAME) bindeada a usd_filtered con key ""
//
// Coordinación interna entre instancias:
//   - Exchange EOF_BROADCAST_EXCHANGE (direct) con keys amt50_filter_1..N
//
// Salida (data + EOFs):
//   - Queue: q1_data (el sink lee data y EOFs en orden FIFO)
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES, INSTANCE_ID, INSTANCE_TOTAL
//   INPUT_QUEUE_NAME       — shared queue compartida entre instancias
//   INPUT_EXCHANGE         — exchange al que se bindea (usd_filtered)
//   EOF_BROADCAST_EXCHANGE — exchange interno para coordinar EOFs entre instancias
//   OUTPUT_QUEUE           — cola de salida para data y EOFs (q1_data)

func mustEnvInt(key string) int {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("env var %s is required", key)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("env var %s must be an integer: %v", key, err)
	}
	return n
}

func main() {
	conn := config.ConnSettings()

	instanceID := mustEnvInt("INSTANCE_ID")
	instanceTotal := mustEnvInt("INSTANCE_TOTAL")

	// usd_filtered se publica con key "" (fanout-style).
	inputMW := config.SharedQueue("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", []string{""}, conn)
	defer inputMW.Close()

	allEOFKeys := make([]string, instanceTotal)
	for i := 0; i < instanceTotal; i++ {
		allEOFKeys[i] = fmt.Sprintf("amt50_filter_%d", i+1)
	}
	eofBroadcast := config.Exchange("EOF_BROADCAST_EXCHANGE", allEOFKeys, conn)
	defer eofBroadcast.Close()

	ownKey := fmt.Sprintf("amt50_filter_%d", instanceID)
	eofReceiver, err := middleware.CreateExchangeMiddleware(config.MustEnv("EOF_BROADCAST_EXCHANGE"), []string{ownKey}, conn)
	if err != nil {
		log.Fatalf("connect to EOF receiver exchange: %v", err)
	}
	defer eofReceiver.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	log.Printf("amt50_filter %d/%d started", instanceID, instanceTotal)

	filterworker.NewWorkerCoordinated(
		func(t protocol.Transaction) bool { return t.AmountPaid < 50 },
		[]*filterworker.Output{
			{Middleware: outputMW, GetBusinessKey: nil, EOFMiddleware: outputMW},
		},
		inputMW,
		eofBroadcast,
		eofReceiver,
		config.UpstreamCount(),
	).Run()
}
