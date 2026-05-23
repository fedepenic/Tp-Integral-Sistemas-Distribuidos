package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
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

func main() {
	conn := config.ConnSettings()

	inputMW, err := middleware.NewQueueMiddleware("usd_for_q1", conn)
	if err != nil {
		log.Fatalf("[amt50_filter] input queue: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.NewQueueMiddleware("q1_data", conn)
	if err != nil {
		log.Fatalf("[amt50_filter] output queue: %v", err)
	}
	defer outputMW.Close()

	eofInMW, err := middleware.NewExchangeMiddleware("eof_usd_filtered", []string{"amt50_filter"}, conn)
	if err != nil {
		log.Fatalf("[amt50_filter] eof input exchange: %v", err)
	}
	defer eofInMW.Close()

	eofOutMW, err := middleware.NewExchangeMiddleware("eof_q1_data", []string{""}, conn)
	if err != nil {
		log.Fatalf("[amt50_filter] eof output exchange: %v", err)
	}
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
