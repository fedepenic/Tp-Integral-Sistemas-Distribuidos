package main

import (
	"fmt"
	"log"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

// Period 1 Filter (pipeline USD)
//
// Entrada:
//   - Queue: usd_for_p1
//
// Condición: Timestamp en [2022-09-01, 2022-09-05]
//
// Salidas (simultáneas, toda tx filtrada va a las tres):
//   1. Exchange: usd_period1_for_q3  (key = PaymentFormat)
//      → AvgPerFormat acumula promedios por formato de pago.
//   2. Exchange: usd_period1_for_q4_fo  (key = FromAccount)
//      → FanOutDetector agrupa por cuenta origen.
//   3. Exchange: usd_period1_for_q4_fi  (key = ToAccount)
//      → FanInDetector agrupa por cuenta destino.
//
// EOF:
//   - Entrada:  exchange "eof_usd_filtered",        key "period1_filter"
//   - Salida 1: exchange "eof_usd_period1_for_q3",  key ""
//   - Salida 2: exchange "eof_usd_period1_for_q4_fo", key ""
//   - Salida 3: exchange "eof_usd_period1_for_q4_fi", key ""
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_QUEUE              — cola de entrada        (usd_for_p1)
//   OUTPUT_Q3_EXCHANGE       — exchange salida Q3     (usd_period1_for_q3)
//   OUTPUT_Q3_PREFIX         — prefijo routing key Q3
//   OUTPUT_Q3_PARTITIONS     — particiones Q3
//   OUTPUT_Q4_FO_EXCHANGE    — exchange salida Q4 FO  (usd_period1_for_q4_fo)
//   OUTPUT_Q4_FO_PREFIX      — prefijo routing key Q4 FO
//   OUTPUT_Q4_FO_PARTITIONS  — particiones Q4 FO
//   OUTPUT_Q4_FI_EXCHANGE    — exchange salida Q4 FI  (usd_period1_for_q4_fi)
//   OUTPUT_Q4_FI_PREFIX      — prefijo routing key Q4 FI
//   OUTPUT_Q4_FI_PARTITIONS  — particiones Q4 FI
//   EOF_INPUT_EXCHANGE       — exchange EOF entrada    (eof_usd_filtered)
//   EOF_INPUT_KEY            — routing key propia      (period1_filter)
//   EOF_Q3_EXCHANGE          — exchange EOF salida Q3  (eof_usd_period1_for_q3)
//   EOF_Q4_FO_EXCHANGE       — exchange EOF salida FO  (eof_usd_period1_for_q4_fo)
//   EOF_Q4_FI_EXCHANGE       — exchange EOF salida FI  (eof_usd_period1_for_q4_fi)

const dateLayout = "2006-01-02"

func main() {
	conn := config.ConnSettings()

	start, _ := time.Parse(dateLayout, "2022-09-01")
	end, _ := time.Parse(dateLayout, "2022-09-05")
	end = end.Add(24 * time.Hour)

	q3Prefix := config.MustEnv("OUTPUT_DIRECT_PREFIX_1")
	q3Partitions := config.MustEnvInt("OUTPUT_DIRECT_PARTITIONS_1")

	q4FOPrefix := config.MustEnv("OUTPUT_DIRECT_PREFIX_2")
	q4FOPartitions := config.MustEnvInt("OUTPUT_DIRECT_PARTITIONS_2")

	q4FIPrefix := config.MustEnv("OUTPUT_DIRECT_PREFIX_3")
	q4FIPartitions := config.MustEnvInt("OUTPUT_DIRECT_PARTITIONS_3")

	instanceID := config.MustEnvInt("INSTANCE_ID")
	instanceTotal := config.MustEnvInt("INSTANCE_TOTAL")

	inputMW := config.SharedQueue("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", []string{""}, conn)
	defer inputMW.Close()

	// eofBroadcast publica a todas las keys del nivel (broadcast a peers).
	allEOFKeys := make([]string, instanceTotal)
	for i := 0; i < instanceTotal; i++ {
		allEOFKeys[i] = fmt.Sprintf("period1_filter_%d", i+1)
	}
	eofBroadcast := config.Exchange("EOF_BROADCAST_EXCHANGE", allEOFKeys, conn)
	defer eofBroadcast.Close()

	// eofReceiver se bindea solo a la key propia de esta instancia.
	ownKey := fmt.Sprintf("period1_filter_%d", instanceID)
	eofReceiver, err := middleware.CreateExchangeMiddleware(config.MustEnv("EOF_BROADCAST_EXCHANGE"), []string{ownKey}, conn)
	if err != nil {
		log.Fatalf("connect to EOF receiver exchange: %v", err)
	}
	defer eofReceiver.Close()

	directMW := config.Exchange("OUTPUT_DIRECT_EXCHANGE_1", []string{}, conn)
	defer directMW.Close()
	log.Printf("usd_filter %d/%d started", instanceID, instanceTotal)

	directMW2 := config.Exchange("OUTPUT_DIRECT_EXCHANGE_2", []string{}, conn)
	defer directMW.Close()
	log.Printf("usd_filter %d/%d started", instanceID, instanceTotal)

	directMW3 := config.Exchange("OUTPUT_DIRECT_EXCHANGE_3", []string{}, conn)
	defer directMW.Close()
	log.Printf("usd_filter %d/%d started", instanceID, instanceTotal)

	log.Printf("period1_filter %d/%d started", instanceID, instanceTotal)

	worker.NewWorkerCoordinated(
		func(t protocol.Transaction) bool {
			ts, err := time.Parse(dateLayout, t.Timestamp[:10])
			if err != nil {
				return false
			}
			return !ts.Before(start) && ts.Before(end)
		},
		[]*worker.Output{
			{
				Middleware:     directMW,
				GetBusinessKey: func(t protocol.Transaction) string { return t.PaymentFormat },
				RoutingPrefix:  q3Prefix,
				Partitions:     q3Partitions,
				EOFMiddleware:  directMW,
			},
			{
				Middleware:     directMW2,
				GetBusinessKey: func(t protocol.Transaction) string { return t.FromAccount },
				RoutingPrefix:  q4FOPrefix,
				Partitions:     q4FOPartitions,
				EOFMiddleware:  directMW2,
			},
			{
				Middleware:     directMW3,
				GetBusinessKey: func(t protocol.Transaction) string { return t.ToAccount },
				RoutingPrefix:  q4FIPrefix,
				Partitions:     q4FIPartitions,
				EOFMiddleware:  directMW3,
			},
		},
		inputMW,
		eofBroadcast,
		eofReceiver,
		config.UpstreamCount(),
	).Run()
}
