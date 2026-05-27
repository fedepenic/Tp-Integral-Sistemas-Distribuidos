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

// Wire/ACH Filter — modo coordinated, escalable horizontalmente.
//
// Igual que usd_filter y amt50_filter: input queue compartida + broadcast
// interno de EOFs entre instancias del nivel + conteo per-cliente.
//
// Entrada (data + EOFs):
//   - Queue: period1_for_q5 (EOFs llegan inline desde period1_q5_filter)
//
// Condicion: PaymentFormat == "Wire" OR PaymentFormat == "ACH"
//
// Salida:
//   - Queue: wireach_txn  (data y EOF en la misma cola, FIFO para currency_converter)
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES, INSTANCE_ID, INSTANCE_TOTAL
//   INPUT_QUEUE            -- cola de entrada (period1_for_q5)
//   EOF_BROADCAST_EXCHANGE -- exchange interno para coordinar EOFs entre instancias
//   OUTPUT_QUEUE           -- cola de salida (wireach_txn)

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

	log.Printf("[wireach_filter] %d/%d started — keeping Wire and ACH transactions", instanceID, instanceTotal)

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	allEOFKeys := make([]string, instanceTotal)
	for i := 0; i < instanceTotal; i++ {
		allEOFKeys[i] = fmt.Sprintf("wireach_filter_%d", i+1)
	}
	eofBroadcast := config.Exchange("EOF_BROADCAST_EXCHANGE", allEOFKeys, conn)
	defer eofBroadcast.Close()

	ownKey := fmt.Sprintf("wireach_filter_%d", instanceID)
	eofReceiver, err := middleware.CreateExchangeMiddleware(config.MustEnv("EOF_BROADCAST_EXCHANGE"), []string{ownKey}, conn)
	if err != nil {
		log.Fatalf("connect to EOF receiver exchange: %v", err)
	}
	defer eofReceiver.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	var seen, passed int

	filterworker.NewWorkerCoordinated(
		func(t protocol.Transaction) bool {
			seen++
			ok := t.PaymentFormat == "Wire" || t.PaymentFormat == "ACH"
			if seen <= 5 {
				log.Printf("[wireach_filter] txn=%d format=%q pass=%v", seen, t.PaymentFormat, ok)
			}
			if ok {
				passed++
				if passed%1000 == 0 {
					log.Printf("[wireach_filter] passed=%d seen=%d", passed, seen)
				}
			}
			return ok
		},
		[]*filterworker.Output{
			{Middleware: outputMW, GetKey: nil, EOFMiddleware: outputMW},
		},
		inputMW,
		eofBroadcast,
		eofReceiver,
		config.UpstreamCount(),
	).Run()
}
