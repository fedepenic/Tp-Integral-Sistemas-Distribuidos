package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
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
//   - Entrada: exchange "eof_q3_candidates", key "amt_avg_filter"
//   - Salida:  exchange "eof_q3_data", key ""
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

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	eofInMW := config.ExchangeWithKey("EOF_INPUT_EXCHANGE", "EOF_INPUT_KEY", conn)
	defer eofInMW.Close()

	eofOutMW := config.Exchange("EOF_OUTPUT_EXCHANGE", []string{""}, conn)
	defer eofOutMW.Close()

	filterworker.NewWorker(
		func(t protocol.Transaction) bool {
			return t.AmountPaid < t.AvgForFormat/100.0
		},
		[]*filterworker.Output{
			{Middleware: outputMW, GetBusinessKey: nil, EOFMiddleware: eofOutMW},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
