package main

import (
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
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

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	outputMW := config.Exchange("OUTPUT_DIRECT_EXCHANGE", []string{}, conn)
	defer outputMW.Close()

	eofInMW := config.ExchangeWithKey("EOF_INPUT_EXCHANGE", "EOF_INPUT_KEY", conn)
	defer eofInMW.Close()

	eofOutMW := config.Exchange("EOF_OUTPUT_EXCHANGE", []string{""}, conn)
	defer eofOutMW.Close()

	filterworker.NewWorker(
		func(t protocol.Transaction) bool {
			ts, err := time.Parse(dateLayout, t.Timestamp[:10])
			if err != nil {
				return false
			}
			return !ts.Before(start) && ts.Before(end)
		},
		[]*filterworker.Output{
			{
				Middleware:     outputMW,
				GetBusinessKey: func(t protocol.Transaction) string { return t.PaymentFormat },
				RoutingPrefix:  outputPrefix,
				Partitions:     outputPartitions,
				EOFMiddleware:  eofOutMW,
			},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
