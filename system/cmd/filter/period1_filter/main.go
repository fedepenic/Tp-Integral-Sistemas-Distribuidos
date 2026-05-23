package main

import (
	"log"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Period 1 Filter (pipeline USD)
//
// Entrada:
//   - Queue: usd_for_p1
//
// Condición: Timestamp en [2022-09-01, 2022-09-05]
//
// Salidas (simultáneas, toda tx filtrada va a las tres):
//   1. Exchange: usd_period1_for_q3  (key = PaymentFormat)
//      → AvgPerFormat acumula promedios por formato de pago.
//   2. Exchange: usd_period1_for_q4_fo  (key = FromAccount)
//      → FanOutDetector agrupa por cuenta origen.
//   3. Exchange: usd_period1_for_q4_fi  (key = ToAccount)
//      → FanInDetector agrupa por cuenta destino.
//
// EOF:
//   - Entrada:  exchange "eof_usd_filtered",        key "period1_filter"
//   - Salida 1: exchange "eof_usd_period1_for_q3",  key ""
//   - Salida 2: exchange "eof_usd_period1_for_q4_fo", key ""
//   - Salida 3: exchange "eof_usd_period1_for_q4_fi", key ""

const dateLayout = "2006-01-02"

func main() {
	conn := config.ConnSettings()

	start, _ := time.Parse(dateLayout, "2022-09-01")
	end, _ := time.Parse(dateLayout, "2022-09-05")
	end = end.Add(24 * time.Hour)

	inputMW, err := middleware.NewQueueMiddleware("usd_for_p1", conn)
	if err != nil {
		log.Fatalf("[period1_filter] input queue: %v", err)
	}
	defer inputMW.Close()

	outQ3MW, err := middleware.NewExchangeMiddleware("usd_period1_for_q3", []string{}, conn)
	if err != nil {
		log.Fatalf("[period1_filter] output q3 exchange: %v", err)
	}
	defer outQ3MW.Close()

	outFOMW, err := middleware.NewExchangeMiddleware("usd_period1_for_q4_fo", []string{}, conn)
	if err != nil {
		log.Fatalf("[period1_filter] output q4_fo exchange: %v", err)
	}
	defer outFOMW.Close()

	outFIMW, err := middleware.NewExchangeMiddleware("usd_period1_for_q4_fi", []string{}, conn)
	if err != nil {
		log.Fatalf("[period1_filter] output q4_fi exchange: %v", err)
	}
	defer outFIMW.Close()

	eofInMW, err := middleware.NewExchangeMiddleware("eof_usd_filtered", []string{"period1_filter"}, conn)
	if err != nil {
		log.Fatalf("[period1_filter] eof input exchange: %v", err)
	}
	defer eofInMW.Close()

	eofQ3MW, err := middleware.NewExchangeMiddleware("eof_usd_period1_for_q3", []string{""}, conn)
	if err != nil {
		log.Fatalf("[period1_filter] eof q3 exchange: %v", err)
	}
	defer eofQ3MW.Close()

	eofFOMW, err := middleware.NewExchangeMiddleware("eof_usd_period1_for_q4_fo", []string{""}, conn)
	if err != nil {
		log.Fatalf("[period1_filter] eof q4_fo exchange: %v", err)
	}
	defer eofFOMW.Close()

	eofFIMW, err := middleware.NewExchangeMiddleware("eof_usd_period1_for_q4_fi", []string{""}, conn)
	if err != nil {
		log.Fatalf("[period1_filter] eof q4_fi exchange: %v", err)
	}
	defer eofFIMW.Close()

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
				Middleware:    outQ3MW,
				GetKey:        func(t protocol.Transaction) string { return t.PaymentFormat },
				EOFMiddleware: eofQ3MW,
			},
			{
				Middleware:    outFOMW,
				GetKey:        func(t protocol.Transaction) string { return t.FromAccount },
				EOFMiddleware: eofFOMW,
			},
			{
				Middleware:    outFIMW,
				GetKey:        func(t protocol.Transaction) string { return t.ToAccount },
				EOFMiddleware: eofFIMW,
			},
		},
		inputMW,
		eofInMW,
		config.UpstreamCount(),
	).Run()
}
