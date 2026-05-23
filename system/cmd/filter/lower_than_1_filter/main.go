package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
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

func main() {
	conn := config.ConnSettings()

	inputMW, err := middleware.NewQueueMiddleware("converted_usd", conn)
	if err != nil {
		log.Fatalf("[usd1_filter] input queue: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.NewQueueMiddleware("q5_filtered", conn)
	if err != nil {
		log.Fatalf("[usd1_filter] output queue: %v", err)
	}
	defer outputMW.Close()

	eofInMW, err := middleware.NewExchangeMiddleware("eof_converted_usd", []string{"usd1_filter"}, conn)
	if err != nil {
		log.Fatalf("[usd1_filter] eof input exchange: %v", err)
	}
	defer eofInMW.Close()

	eofOutMW, err := middleware.NewExchangeMiddleware("eof_q5_filtered", []string{""}, conn)
	if err != nil {
		log.Fatalf("[usd1_filter] eof output exchange: %v", err)
	}
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
