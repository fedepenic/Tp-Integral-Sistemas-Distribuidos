package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Amount < 50 Filter
//
// Entrada:
//   - Queue: usd_for_q1
//
// Condición: AmountPaid < 50
//
// Salida:
//   - Queue: q1_data  (sin routing key)
//
// EOF:
//   - Entrada: exchange "eof_usd_filtered", key "amt50_filter"
//   - Salida:  exchange "eof_q1_data", key ""
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_QUEUE         — cola de entrada (usd_for_q1)
//   OUTPUT_QUEUE        — cola de salida  (q1_data)
//   EOF_INPUT_EXCHANGE  — exchange EOF de entrada (eof_usd_filtered)
//   EOF_INPUT_KEY       — routing key propia       (amt50_filter)
//   EOF_OUTPUT_EXCHANGE — exchange EOF de salida   (eof_q1_data)

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
		func(t protocol.Transaction) bool { return t.AmountPaid < 50 },
		[]*filterworker.Output{
			{Middleware: outputMW, GetKey: nil, EOFMiddleware: eofOutMW},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
