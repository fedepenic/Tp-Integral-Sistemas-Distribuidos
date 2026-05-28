package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

// USD Filter (modo coordinated, escalable horizontalmente)
//
// Lee data + EOFs de una shared input queue (load-balanced entre instancias).
// Igual que el cleaner, cuando una instancia recibe un EOF lo retransmite a
// todas las instancias del nivel vía el exchange EOF_BROADCAST_EXCHANGE.
// Cada instancia cuenta EOFs per-cliente y propaga 1 EOF por cliente
// downstream una vez que recibió upstreamCount EOFs y drenó su in-flight.
//
// Entrada (data + EOFs):
//   - Shared queue (INPUT_QUEUE_NAME) bindeada a transactions_clean con key txn_for_usd
//
// Salidas:
//  1. Exchange fanout "usd_filtered" (GetBusinessKey = nil)
//     Las queues usd_for_q1, usd_for_q3p2 y usd_for_p1 están bound a este exchange.
//  2. Exchange direct "usd_for_q2"   (GetBusinessKey = from_bank)
//     Particiona por banco de origen para el worker MaxBank de Q2.
//
// Salidas (data y EOFs comparten exchange):
//  1. usd_filtered (fanout) → amt50_filter (y, en su momento, period2/period1)
//  2. usd_for_q2 (direct, GetKey=from_bank) → Q2 (con EOF)
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES, INSTANCE_ID, INSTANCE_TOTAL
//   INPUT_QUEUE_NAME       — shared queue compartida entre instancias
//   INPUT_EXCHANGE         — exchange al que se bindea (transactions_clean)
//   INPUT_KEY              — routing key del binding (txn_for_usd)
//   EOF_BROADCAST_EXCHANGE — exchange interno para coordinar EOFs entre instancias
//   OUTPUT_FANOUT_EXCHANGE — exchange fanout de salida (usd_filtered)
//   OUTPUT_DIRECT_EXCHANGE — exchange direct de salida (usd_for_q2)
//   OUTPUT_DIRECT_PREFIX   — prefijo routing key salida (por ej. maxbank)
//   OUTPUT_DIRECT_PARTITIONS — particiones de salida
//   EOF_INPUT_EXCHANGE     — exchange EOF de entrada (eof_cleaner)
//   EOF_INPUT_KEY          — routing key propia en ese exchange (usd_filter)
//   EOF_FANOUT_EXCHANGE    — exchange EOF para el fanout (eof_usd_filtered)
//   EOF_DIRECT_EXCHANGE    — exchange EOF para el direct (eof_usd_for_q2)

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
	directPrefix := config.MustEnv("OUTPUT_DIRECT_PREFIX")
	directPartitions := config.MustEnvInt("OUTPUT_DIRECT_PARTITIONS")

	instanceID := mustEnvInt("INSTANCE_ID")
	instanceTotal := mustEnvInt("INSTANCE_TOTAL")

	inputMW := config.SharedQueueWithKey("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	// eofBroadcast publica a todas las keys del nivel (broadcast a peers).
	allEOFKeys := make([]string, instanceTotal)
	for i := 0; i < instanceTotal; i++ {
		allEOFKeys[i] = fmt.Sprintf("usd_filter_%d", i+1)
	}
	eofBroadcast := config.Exchange("EOF_BROADCAST_EXCHANGE", allEOFKeys, conn)
	defer eofBroadcast.Close()

	// eofReceiver se bindea solo a la key propia de esta instancia.
	ownKey := fmt.Sprintf("usd_filter_%d", instanceID)
	eofReceiver, err := middleware.CreateExchangeMiddleware(config.MustEnv("EOF_BROADCAST_EXCHANGE"), []string{ownKey}, conn)
	if err != nil {
		log.Fatalf("connect to EOF receiver exchange: %v", err)
	}
	defer eofReceiver.Close()

	fanoutMW := config.Exchange("OUTPUT_FANOUT_EXCHANGE", []string{""}, conn)
	defer fanoutMW.Close()

	directMW := config.Exchange("OUTPUT_DIRECT_EXCHANGE", []string{}, conn)
	defer directMW.Close()
	log.Printf("usd_filter %d/%d started", instanceID, instanceTotal)

	worker.NewWorkerCoordinated(
		func(t protocol.Transaction) bool {
			return t.PaymentCurrency == "US Dollar"
		},
		[]*worker.Output{
			{Middleware: fanoutMW, GetBusinessKey: nil, EOFMiddleware: fanoutMW},
			{
				Middleware:     directMW,
				GetBusinessKey: func(t protocol.Transaction) string { return t.FromBank },
				RoutingPrefix:  directPrefix,
				Partitions:     directPartitions,
				EOFMiddleware:  directMW,
			},
		},
		inputMW,
		eofBroadcast,
		eofReceiver,
		config.UpstreamCount(),
	).Run()
}
