package main

import (
	"log"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Period 2 Filter
//
// Entrada:
//   - Queue: usd_for_q3p2
//
// Condición: Timestamp en [2022-09-06, 2022-09-15]
//
// Salida:
//   - Queue: usd_period2  (routing key = PaymentFormat)
//     Las transacciones se agrupan por formato de pago para que el
//     JoinerQ3 reciba juntas todas las del mismo formato.
//
// EOF:
//   - Entrada: exchange "eof_usd_filtered", key "period2_filter"
//   - Salida:  exchange "eof_usd_period2", key ""

const dateLayout = "2006-01-02"

func main() {
	conn := config.ConnSettings()

	start, _ := time.Parse(dateLayout, "2022-09-06")
	end, _ := time.Parse(dateLayout, "2022-09-15")
	end = end.Add(24 * time.Hour)

	inputMW, err := middleware.NewQueueMiddleware("usd_for_q3p2", conn)
	if err != nil {
		log.Fatalf("[period2_filter] input queue: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.NewQueueMiddleware("usd_period2", conn)
	if err != nil {
		log.Fatalf("[period2_filter] output queue: %v", err)
	}
	defer outputMW.Close()

	eofInMW, err := middleware.NewExchangeMiddleware("eof_usd_filtered", []string{"period2_filter"}, conn)
	if err != nil {
		log.Fatalf("[period2_filter] eof input exchange: %v", err)
	}
	defer eofInMW.Close()

	eofOutMW, err := middleware.NewExchangeMiddleware("eof_usd_period2", []string{""}, conn)
	if err != nil {
		log.Fatalf("[period2_filter] eof output exchange: %v", err)
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
			{
				Middleware:    outputMW,
				GetKey:        func(t protocol.Transaction) string { return t.PaymentFormat },
				EOFMiddleware: eofOutMW,
			},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
