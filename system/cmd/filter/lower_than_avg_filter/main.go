package main

import (
	"log"
	"os"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

// Amount < avg/100 Filter  (Q3 final filter)
//
// Entrada:
//   - Queue: q3_candidates
//
// Condición: AmountPaid < AvgForFormat / 100
//   El promedio por formato de pago llega precalculado en el campo
//   AvgForFormat del Transaction struct (calculado por el JoinerQ3 upstream).
//
// Salida:
//   - Queue: q3_data  (sin routing key)
//
// EOF:
//   - Entrada preferida: en la misma queue de entrada, emitido por JoinQ3.
//   - Salida preferida: en la misma queue de salida, consumida por sink_3.
//   - Legacy: exchanges separados configurados por EOF_INPUT_EXCHANGE y
//     EOF_OUTPUT_EXCHANGE.
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_QUEUE         — cola de entrada (q3_candidates)
//   OUTPUT_QUEUE        — cola de salida  (q3_data)
//   EOF_INPUT_EXCHANGE  — exchange EOF entrada (eof_q3_candidates)
//   EOF_INPUT_KEY       — routing key propia   (amt_avg_filter)
//   EOF_OUTPUT_EXCHANGE — exchange EOF salida  (eof_q3_data)

func main() {
	conn := config.ConnSettings()

	instanceID := config.MustEnvInt("INSTANCE_ID")
	instanceTotal := config.MustEnvInt("INSTANCE_TOTAL")

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	var eofInMW middleware.Middleware
	if os.Getenv("EOF_INPUT_EXCHANGE") != "" {
		eofInMW = config.ExchangeWithKey("EOF_INPUT_EXCHANGE", "EOF_INPUT_KEY", conn)
		defer eofInMW.Close()
	}

	eofOutMW := outputMW
	if os.Getenv("EOF_OUTPUT_EXCHANGE") != "" {
		eofOutMW = config.Exchange("EOF_OUTPUT_EXCHANGE", []string{""}, conn)
		defer eofOutMW.Close()
	}

	log.Printf("lower_than_avg_filter %d/%d started", instanceID, instanceTotal)

	worker.NewWorker(
		func(t protocol.Transaction) bool {
			return t.AmountPaid < t.AvgForFormat/100.0
		},
		[]*worker.Output{
			{Middleware: outputMW, GetBusinessKey: nil, EOFMiddleware: eofOutMW},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
