package main

import (
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Period 1 Filter (pipeline Q5)
//
// Esta instancia del filtro de período 1 opera sobre el pipeline de Q5,
// que recibe todas las transacciones (no solo USD) desde el cleaner.
//
// Entrada:
//   - Exchange: transactions_clean, key: txn_for_q5
//
// Condición: Timestamp en [2022-09-01, 2022-09-05]
//
// Salida:
//   - Queue: period1_for_q5  (sin routing key)
//
// EOF:
//   - Entrada: exchange "eof_cleaner",       key "period1_q5_filter"
//   - Salida:  exchange "eof_period1_for_q5", key ""
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_EXCHANGE      — exchange de entrada (transactions_clean)
//   INPUT_KEY           — routing key propia (txn_for_q5)
//   OUTPUT_QUEUE        — cola de salida  (period1_for_q5)
//   EOF_INPUT_EXCHANGE  — exchange EOF entrada (eof_cleaner)
//   EOF_INPUT_KEY       — routing key propia   (period1_q5_filter)
//   EOF_OUTPUT_EXCHANGE — exchange EOF salida  (eof_period1_for_q5)

const dateLayout = "2006-01-02"

func main() {
	conn := config.ConnSettings()

	start, _ := time.Parse(dateLayout, "2022-09-01")
	end, _ := time.Parse(dateLayout, "2022-09-05")
	end = end.Add(24 * time.Hour)

	inputMW := config.ExchangeWithKey("INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
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
			{Middleware: outputMW, GetBusinessKey: nil, EOFMiddleware: eofOutMW},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
