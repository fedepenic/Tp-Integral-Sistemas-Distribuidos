package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
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

func main() {
	conn := config.ConnSettings()

	inputMW, err := middleware.NewQueueMiddleware("q3_candidates", conn)
	if err != nil {
		log.Fatalf("[amt_avg_filter] input queue: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.NewQueueMiddleware("q3_data", conn)
	if err != nil {
		log.Fatalf("[amt_avg_filter] output queue: %v", err)
	}
	defer outputMW.Close()

	eofInMW, err := middleware.NewExchangeMiddleware("eof_q3_candidates", []string{"amt_avg_filter"}, conn)
	if err != nil {
		log.Fatalf("[amt_avg_filter] eof input exchange: %v", err)
	}
	defer eofInMW.Close()

	eofOutMW, err := middleware.NewExchangeMiddleware("eof_q3_data", []string{""}, conn)
	if err != nil {
		log.Fatalf("[amt_avg_filter] eof output exchange: %v", err)
	}
	defer eofOutMW.Close()

	filterworker.NewWorker(
		func(t protocol.Transaction) bool {
			return t.AmountPaid < t.AvgForFormat/100.0
		},
		[]*filterworker.Output{
			{Middleware: outputMW, GetKey: nil, EOFMiddleware: eofOutMW},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
