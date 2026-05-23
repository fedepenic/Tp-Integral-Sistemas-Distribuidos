package main

import (
	"log"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Period 1 Filter (pipeline Q5)
//
// Esta instancia del filtro de período 1 opera sobre el pipeline de Q5,
// que recibe todas las transacciones (no solo USD) desde el cleaner.
//
// Entrada:
//   - Queue: txn_for_q5
//
// Condición: Timestamp en [2022-09-01, 2022-09-05]
//
// Salida:
//   - Queue: period1_for_q5  (sin routing key)
//
// EOF:
//   - Entrada: exchange "eof_cleaner",       key "period1_q5_filter"
//   - Salida:  exchange "eof_period1_for_q5", key ""

const dateLayout = "2006-01-02"

func main() {
	conn := config.ConnSettings()

	start, _ := time.Parse(dateLayout, "2022-09-01")
	end, _ := time.Parse(dateLayout, "2022-09-05")
	end = end.Add(24 * time.Hour)

	inputMW, err := middleware.NewQueueMiddleware("txn_for_q5", conn)
	if err != nil {
		log.Fatalf("[period1_q5_filter] input queue: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.NewQueueMiddleware("period1_for_q5", conn)
	if err != nil {
		log.Fatalf("[period1_q5_filter] output queue: %v", err)
	}
	defer outputMW.Close()

	eofInMW, err := middleware.NewExchangeMiddleware("eof_cleaner", []string{"period1_q5_filter"}, conn)
	if err != nil {
		log.Fatalf("[period1_q5_filter] eof input exchange: %v", err)
	}
	defer eofInMW.Close()

	eofOutMW, err := middleware.NewExchangeMiddleware("eof_period1_for_q5", []string{""}, conn)
	if err != nil {
		log.Fatalf("[period1_q5_filter] eof output exchange: %v", err)
	}
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
			{Middleware: outputMW, GetKey: nil, EOFMiddleware: eofOutMW},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
