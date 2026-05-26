package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// USD Filter
//
// Modo single-queue: data y EOFs llegan por la misma input queue
// (named queue compartida bindeada a transactions_clean con key txn_for_usd),
// igual que el cleaner. Todas las instancias del filtro consumen de la misma
// queue (competing consumers → load balancing).
//
// Entrada (data + EOFs):
//   - Queue compartida (INPUT_QUEUE_NAME) bindeada a transactions_clean, key txn_for_usd
//
// Condición: PaymentCurrency == "US Dollar"
//
// Salidas (data y EOFs comparten exchange):
//  1. Exchange fanout "usd_filtered" (GetKey = nil)
//     amt50_filter (y, en su momento, period2/period1) leen de este fanout.
//  2. Exchange direct "usd_for_q2" (GetKey = from_bank)
//     Particiona por banco para el worker MaxBank de Q2.
//     EOFMiddleware = nil porque el pipeline Q2 todavía no está implementado.
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_QUEUE_NAME       — nombre de la queue compartida (e.g. usd_filter_input)
//   INPUT_EXCHANGE         — exchange al que se bindea (transactions_clean)
//   INPUT_KEY              — routing key del binding (txn_for_usd)
//   OUTPUT_FANOUT_EXCHANGE — exchange fanout de salida (usd_filtered)
//   OUTPUT_DIRECT_EXCHANGE — exchange direct de salida (usd_for_q2)

func main() {
	conn := config.ConnSettings()

	inputMW := config.SharedQueueWithKey("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	fanoutMW := config.Exchange("OUTPUT_FANOUT_EXCHANGE", []string{""}, conn)
	defer fanoutMW.Close()

	directMW := config.Exchange("OUTPUT_DIRECT_EXCHANGE", []string{}, conn)
	defer directMW.Close()

	filterworker.NewWorker(
		func(t protocol.Transaction) bool {
			return t.PaymentCurrency == "US Dollar"
		},
		[]*filterworker.Output{
			// Fanout: EOF se propaga aquí para que llegue a amt50_filter (single-queue).
			{Middleware: fanoutMW, GetKey: nil, EOFMiddleware: fanoutMW},
			// Direct para Q2: aún no hay consumidor, no propagamos EOF.
			{
				Middleware:    directMW,
				GetKey:        func(t protocol.Transaction) string { return t.FromBank },
				EOFMiddleware: nil,
			},
		},
		inputMW,
		nil, // single-queue: data + EOFs por inputMW
		config.UpstreamCount(),
	).Run()
}
