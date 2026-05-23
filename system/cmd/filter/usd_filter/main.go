package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// USD Filter
//
// Entrada:
//   - Queue: txn_for_usd
//
// Condición: PaymentCurrency == "US Dollar"
//
// Salidas:
//  1. Exchange fanout "usd_filtered" (GetKey = nil)
//     Las queues usd_for_q1, usd_for_q3p2 y usd_for_p1 están bound a este exchange.
//  2. Exchange direct "usd_for_q2"   (GetKey = from_bank)
//     Particiona por banco de origen para el worker MaxBank de Q2.
//
// EOF:
//   - Entrada:  exchange "eof_cleaner",  key "usd_filter"
//   - Salida 1: exchange "eof_usd_filtered"  (notifica a los consumidores del fanout)
//   - Salida 2: exchange "eof_usd_for_q2"    (notifica a los workers MaxBank)

func main() {
	conn := config.ConnSettings()

	inputMW, err := middleware.NewQueueMiddleware("txn_for_usd", conn)
	if err != nil {
		log.Fatalf("[usd_filter] input queue: %v", err)
	}
	defer inputMW.Close()

	fanoutMW, err := middleware.NewExchangeMiddleware("usd_filtered", []string{""}, conn)
	if err != nil {
		log.Fatalf("[usd_filter] fanout exchange: %v", err)
	}
	defer fanoutMW.Close()

	directMW, err := middleware.NewExchangeMiddleware("usd_for_q2", []string{}, conn)
	if err != nil {
		log.Fatalf("[usd_filter] direct exchange: %v", err)
	}
	defer directMW.Close()

	eofInMW, err := middleware.NewExchangeMiddleware("eof_cleaner", []string{"usd_filter"}, conn)
	if err != nil {
		log.Fatalf("[usd_filter] eof input exchange: %v", err)
	}
	defer eofInMW.Close()

	eofFanoutMW, err := middleware.NewExchangeMiddleware("eof_usd_filtered", []string{""}, conn)
	if err != nil {
		log.Fatalf("[usd_filter] eof fanout exchange: %v", err)
	}
	defer eofFanoutMW.Close()

	eofDirectMW, err := middleware.NewExchangeMiddleware("eof_usd_for_q2", []string{""}, conn)
	if err != nil {
		log.Fatalf("[usd_filter] eof direct exchange: %v", err)
	}
	defer eofDirectMW.Close()

	filterworker.NewWorker(
		func(t protocol.Transaction) bool {
			return t.PaymentCurrency == "US Dollar"
		},
		[]*filterworker.Output{
			{Middleware: fanoutMW, GetKey: nil, EOFMiddleware: eofFanoutMW},
			{
				Middleware:    directMW,
				GetKey:        func(t protocol.Transaction) string { return t.FromBank },
				EOFMiddleware: eofDirectMW,
			},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
