package main

import (
	"fmt"
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
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
//   - Entrada preferida: en la misma queue de entrada, emitido por JoinQ3.
//   - Salida preferida: en la misma queue de salida, consumida por sink_3.
//   - Legacy: exchanges separados configurados por EOF_INPUT_EXCHANGE y
//     EOF_OUTPUT_EXCHANGE.
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES
//   INPUT_QUEUE         — cola de entrada (q3_candidates)
//   OUTPUT_QUEUE        — cola de salida  (q3_data)
//   EOF_INPUT_EXCHANGE  — exchange EOF entrada (eof_q3_candidates)
//   EOF_INPUT_KEY       — routing key propia   (amt_avg_filter)
//   EOF_OUTPUT_EXCHANGE — exchange EOF salida  (eof_q3_data)

func main() {
	conn := config.ConnSettings()

	instanceID := config.MustEnvInt("INSTANCE_ID")
	instanceTotal := config.MustEnvInt("INSTANCE_TOTAL")

	inputMW := config.SharedQueue("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", []string{""}, conn)
	defer inputMW.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	// eofBroadcast publica a todas las keys del nivel (broadcast a peers).
	allEOFKeys := make([]string, instanceTotal)
	for i := 0; i < instanceTotal; i++ {
		allEOFKeys[i] = fmt.Sprintf("amt_avg_filter_%d", i+1)
	}
	eofBroadcast := config.Exchange("EOF_BROADCAST_EXCHANGE", allEOFKeys, conn)
	defer eofBroadcast.Close()

	// eofReceiver se bindea solo a la key propia de esta instancia.
	ownKey := fmt.Sprintf("amt_avg_filter_%d", instanceID)
	eofReceiver, err := middleware.CreateExchangeMiddleware(config.MustEnv("EOF_BROADCAST_EXCHANGE"), []string{ownKey}, conn)
	if err != nil {
		log.Fatalf("connect to EOF receiver exchange: %v", err)
	}
	defer eofReceiver.Close()

	log.Printf("lower_than_avg_filter %d/%d started", instanceID, instanceTotal)

	worker.NewWorkerCoordinated(
		func(t protocol.Transaction) bool {
			log.Printf("lower_than_avg_filter TRABAJANDO")
			return t.AmountPaid < t.AvgForFormat/100.0
		},
		[]*worker.Output{
			{Middleware: outputMW, GetBusinessKey: nil, EOFMiddleware: outputMW},
		},
		inputMW,
		eofBroadcast,
		eofReceiver,
		config.UpstreamCount(),
	).Run()
}
