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

// Period 2 Filter
//
// Entrada:
//   - Queue: usd_for_q3p2
//
// Condición: Timestamp en [2022-09-06, 2022-09-15]
//
// Salida:
//   - Queue: usd_period2  (routing key = PaymentFormat)
//     Las transacciones se agrupan por formato de pago para que el
//     JoinerQ3 reciba juntas todas las del mismo formato.
//
// EOF:
//   - Entrada: exchange "eof_usd_filtered", key "period2_filter"
//   - Salida:  exchange "eof_usd_period2", key ""
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_QUEUE         — cola de entrada (usd_for_q3p2)
//   OUTPUT_QUEUE        — cola de salida  (usd_period2)
//   OUTPUT_PREFIX       — prefijo routing key salida
//   OUTPUT_PARTITIONS   — particiones de salida
//   EOF_INPUT_EXCHANGE  — exchange EOF de entrada (eof_usd_filtered)
//   EOF_INPUT_KEY       — routing key propia       (period2_filter)
//   EOF_OUTPUT_EXCHANGE — exchange EOF de salida   (eof_usd_period2)

const dateLayout = "2006-01-02"

func main() {
	conn := config.ConnSettings()

	start, _ := time.Parse(dateLayout, "2022-09-06")
	end, _ := time.Parse(dateLayout, "2022-09-15")
	end = end.Add(24 * time.Hour)

	outputPrefix := config.MustEnv("OUTPUT_PREFIX")
	outputPartitions := config.MustEnvInt("OUTPUT_PARTITIONS")

	instanceID := config.MustEnvInt("INSTANCE_ID")
	instanceTotal := config.MustEnvInt("INSTANCE_TOTAL")

	inputMW := config.SharedQueue("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", []string{""}, conn)
	defer inputMW.Close()

	allEOFKeys := make([]string, instanceTotal)
	for i := 0; i < instanceTotal; i++ {
		allEOFKeys[i] = fmt.Sprintf("period2_filter_%d", i+1)
	}
	eofBroadcast := config.Exchange("EOF_BROADCAST_EXCHANGE", allEOFKeys, conn)
	defer eofBroadcast.Close()

	ownKey := fmt.Sprintf("period2_filter_%d", instanceID)
	eofReceiver, err := middleware.CreateExchangeMiddleware(config.MustEnv("EOF_BROADCAST_EXCHANGE"), []string{ownKey}, conn)
	if err != nil {
		log.Fatalf("connect to EOF receiver exchange: %v", err)
	}
	defer eofReceiver.Close()

	outputMW := config.Exchange("OUTPUT_DIRECT_EXCHANGE", []string{}, conn)
	defer outputMW.Close()

	log.Printf("period2_filter %d/%d started", instanceID, instanceTotal)

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
				Middleware:     outputMW,
				GetBusinessKey: func(t protocol.Transaction) string { return t.PaymentFormat },
				RoutingPrefix:  outputPrefix,
				Partitions:     outputPartitions,
				EOFMiddleware:  outputMW,
			},
		},
		inputMW,
		eofBroadcast,
		eofReceiver,
		config.UpstreamCount(),
	).Run()
}
