package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Amount < 50 Filter
//
// Modo single-queue: data y EOFs llegan por la misma input queue
// (fanout usd_filtered), igual que el cleaner.
//
// Entrada (data + EOFs):
//   - Exchange: usd_filtered (fanout desde usd_filter)
//
// Condición: AmountPaid < 50
//
// Salida (data + EOFs):
//   - Queue: q1_data (mismo destino — el sink lee data y EOF en orden FIFO)
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_EXCHANGE — exchange de entrada (usd_filtered)
//   OUTPUT_QUEUE   — cola de salida para data y EOFs (q1_data)

func main() {
	conn := config.ConnSettings()

	inputMW := config.Exchange("INPUT_EXCHANGE", []string{""}, conn)
	defer inputMW.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	filterworker.NewWorker(
		func(t protocol.Transaction) bool { return t.AmountPaid < 50 },
		[]*filterworker.Output{
			// EOFMiddleware reusa outputMW: data y EOFs viajan por q1_data en
			// orden FIFO hasta el sink.
			{Middleware: outputMW, GetKey: nil, EOFMiddleware: outputMW},
		},
		inputMW,
		nil, // single-queue: data + EOFs por inputMW
		config.UpstreamCount(),
	).Run()
}
