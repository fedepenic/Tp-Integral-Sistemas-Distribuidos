package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// USD < 1 Filter
//
// Entrada:
//   - Queue: converted_usd
//     Las transacciones llegan con AmountPaid ya convertido a USD
//     por el Currency Converter upstream.
//
// Condición: AmountPaid < 1  (en USD convertido)
//
// Salida:
//   - Queue: q5_filtered  (sin routing key)
//
// EOF:
//   - Entrada: exchange "eof_converted_usd", key "usd1_filter"
//   - Salida:  exchange "eof_q5_filtered",   key ""
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_QUEUE         — cola de entrada (converted_usd)
//   OUTPUT_QUEUE        — cola de salida  (q5_filtered)
//   EOF_INPUT_EXCHANGE  — exchange EOF entrada (eof_converted_usd)
//   EOF_INPUT_KEY       — routing key propia   (usd1_filter)
//   EOF_OUTPUT_EXCHANGE — exchange EOF salida  (eof_q5_filtered)

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
		func(t protocol.Transaction) bool { return t.AmountPaid < 1.0 },
		[]*filterworker.Output{
			{Middleware: outputMW, GetKey: nil, EOFMiddleware: eofOutMW},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
