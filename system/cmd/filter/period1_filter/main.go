package main

import (
	"strings"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
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
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_QUEUE              — cola de entrada        (usd_for_p1)
//   OUTPUT_Q3_EXCHANGE       — exchange salida Q3     (usd_period1_for_q3)
//   OUTPUT_Q4_FO_EXCHANGE    — exchange salida Q4 FO  (usd_period1_for_q4_fo)
//   OUTPUT_Q4_FI_EXCHANGE    — exchange salida Q4 FI  (usd_period1_for_q4_fi)
//   EOF_INPUT_EXCHANGE       — exchange EOF entrada    (eof_usd_filtered)
//   EOF_INPUT_KEY            — routing key propia      (period1_filter)
//   EOF_Q3_EXCHANGE          — exchange EOF salida Q3  (eof_usd_period1_for_q3)
//   EOF_Q4_FO_EXCHANGE       — exchange EOF salida FO  (eof_usd_period1_for_q4_fo)
//   EOF_Q4_FI_EXCHANGE       — exchange EOF salida FI  (eof_usd_period1_for_q4_fi)

const dateLayout = "2006-01-02"

func main() {
	conn := config.ConnSettings()

	start, _ := time.Parse(dateLayout, "2022-09-01")
	end, _ := time.Parse(dateLayout, "2022-09-05")
	end = end.Add(24 * time.Hour)

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	outQ3MW := config.Exchange("OUTPUT_Q3_EXCHANGE", []string{}, conn)
	defer outQ3MW.Close()

	outFOMW := config.Exchange("OUTPUT_Q4_FO_EXCHANGE", []string{}, conn)
	defer outFOMW.Close()

	outFIMW := config.Exchange("OUTPUT_Q4_FI_EXCHANGE", []string{}, conn)
	defer outFIMW.Close()

	eofInMW := config.ExchangeWithKey("EOF_INPUT_EXCHANGE", "EOF_INPUT_KEY", conn)
	defer eofInMW.Close()

	eofQ3MW := config.Exchange("EOF_Q3_EXCHANGE", []string{""}, conn)
	defer eofQ3MW.Close()

	eofFOMW := config.Exchange("EOF_Q4_FO_EXCHANGE", []string{""}, conn)
	defer eofFOMW.Close()

	eofFIMW := config.Exchange("EOF_Q4_FI_EXCHANGE", []string{""}, conn)
	defer eofFIMW.Close()

	filterworker.NewWorker(
		func(t protocol.Transaction) bool {
			date := strings.ReplaceAll(t.Timestamp[:10], "/", "-")
			ts, err := time.Parse(dateLayout, date)
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
