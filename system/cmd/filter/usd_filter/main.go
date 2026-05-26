package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// USD Filter
//
// Entrada:
//   - Exchange: transactions_clean, key: txn_for_usd
//
// Condición: PaymentCurrency == "US Dollar"
//
// Salidas:
//  1. Exchange fanout "usd_filtered" (GetBusinessKey = nil)
//     Las queues usd_for_q1, usd_for_q3p2 y usd_for_p1 están bound a este exchange.
//  2. Exchange direct "usd_for_q2"   (GetBusinessKey = from_bank)
//     Particiona por banco de origen para el worker MaxBank de Q2.
//
// EOF:
//   - Entrada:  exchange "eof_cleaner",  key "usd_filter"
//   - Salida 1: exchange "eof_usd_filtered"  (notifica a los consumidores del fanout)
//   - Salida 2: exchange "eof_usd_for_q2"    (notifica a los workers MaxBank)
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_EXCHANGE         — exchange de entrada (transactions_clean)
//   INPUT_KEY              — routing key propia (txn_for_usd)
//   OUTPUT_FANOUT_EXCHANGE — exchange fanout de salida (usd_filtered)
//   OUTPUT_DIRECT_EXCHANGE — exchange direct de salida (usd_for_q2)
//   OUTPUT_DIRECT_PREFIX   — prefijo routing key salida (por ej. maxbank)
//   OUTPUT_DIRECT_PARTITIONS — particiones de salida
//   EOF_INPUT_EXCHANGE     — exchange EOF de entrada (eof_cleaner)
//   EOF_INPUT_KEY          — routing key propia en ese exchange (usd_filter)
//   EOF_FANOUT_EXCHANGE    — exchange EOF para el fanout (eof_usd_filtered)
//   EOF_DIRECT_EXCHANGE    — exchange EOF para el direct (eof_usd_for_q2)

func main() {
	conn := config.ConnSettings()
	directPrefix := config.MustEnv("OUTPUT_DIRECT_PREFIX")
	directPartitions := config.MustEnvInt("OUTPUT_DIRECT_PARTITIONS")

	inputMW := config.ExchangeWithKey("INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	fanoutMW := config.Exchange("OUTPUT_FANOUT_EXCHANGE", []string{""}, conn)
	defer fanoutMW.Close()

	directMW := config.Exchange("OUTPUT_DIRECT_EXCHANGE", []string{}, conn)
	defer directMW.Close()

	eofInMW := config.ExchangeWithKey("EOF_INPUT_EXCHANGE", "EOF_INPUT_KEY", conn)
	defer eofInMW.Close()

	eofFanoutMW := config.Exchange("EOF_FANOUT_EXCHANGE", []string{""}, conn)
	defer eofFanoutMW.Close()

	eofDirectMW := config.Exchange("EOF_DIRECT_EXCHANGE", []string{""}, conn)
	defer eofDirectMW.Close()

	filterworker.NewWorker(
		func(t protocol.Transaction) bool {
			return t.PaymentCurrency == "US Dollar"
		},
		[]*filterworker.Output{
			{Middleware: fanoutMW, GetBusinessKey: nil, EOFMiddleware: eofFanoutMW},
			{
				Middleware:     directMW,
				GetBusinessKey: func(t protocol.Transaction) string { return t.FromBank },
				RoutingPrefix:  directPrefix,
				Partitions:     directPartitions,
				EOFMiddleware:  eofDirectMW,
			},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
