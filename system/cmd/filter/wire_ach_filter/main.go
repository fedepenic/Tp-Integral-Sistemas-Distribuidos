package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

// Wire/ACH Filter
//
// Entrada:
//   - Queue: period1_for_q5
//
// Condición: PaymentFormat == "Wire" OR PaymentFormat == "ACH"
//
// Salida:
//   - Queue: wireach_txn  (sin routing key)
//
// EOF:
//   - Entrada: exchange "eof_period1_for_q5", key "wireach_filter"
//   - Salida:  exchange "eof_wireach_txn",    key ""
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_QUEUE         — cola de entrada (period1_for_q5)
//   OUTPUT_QUEUE        — cola de salida  (wireach_txn)
//   EOF_INPUT_EXCHANGE  — exchange EOF entrada (eof_period1_for_q5)
//   EOF_INPUT_KEY       — routing key propia   (wireach_filter)
//   EOF_OUTPUT_EXCHANGE — exchange EOF salida  (eof_wireach_txn)

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

	worker.NewWorker(
		func(t protocol.Transaction) bool {
			return t.PaymentFormat == "Wire" || t.PaymentFormat == "ACH"
		},
		[]*worker.Output{
			{Middleware: outputMW, GetBusinessKey: nil, EOFMiddleware: eofOutMW},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
