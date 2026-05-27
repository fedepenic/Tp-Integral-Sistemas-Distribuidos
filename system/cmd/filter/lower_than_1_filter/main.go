package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// USD < 1 Filter — modo coordinated, escalable horizontalmente.
//
// Igual que usd_filter y amt50_filter: input queue compartida + broadcast
// interno de EOFs entre instancias del nivel + conteo per-cliente.
//
// Entrada (data + EOFs):
//   - Queue: converted_usd (EOFs llegan inline desde currency_converter)
//     Las transacciones llegan con AmountPaid ya convertido a USD.
//
// Condicion: AmountPaid < 1  (en USD convertido)
//
// Salida:
//   - Queue: q5_filtered  (data y EOF en la misma cola, FIFO para sink_5)
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES, INSTANCE_ID, INSTANCE_TOTAL
//   INPUT_QUEUE            -- cola de entrada (converted_usd)
//   EOF_BROADCAST_EXCHANGE -- exchange interno para coordinar EOFs entre instancias
//   OUTPUT_QUEUE           -- cola de salida (q5_filtered)

func mustEnvInt(key string) int {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("env var %s is required", key)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("env var %s must be an integer: %v", key, err)
	}
	return n
}

func main() {
	conn := config.ConnSettings()

	instanceID := mustEnvInt("INSTANCE_ID")
	instanceTotal := mustEnvInt("INSTANCE_TOTAL")

	log.Printf("[lower_than_1_filter] %d/%d started — keeping AmountPaid < 1 USD", instanceID, instanceTotal)

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	allEOFKeys := make([]string, instanceTotal)
	for i := 0; i < instanceTotal; i++ {
		allEOFKeys[i] = fmt.Sprintf("usd_lower_than_one_%d", i+1)
	}
	eofBroadcast := config.Exchange("EOF_BROADCAST_EXCHANGE", allEOFKeys, conn)
	defer eofBroadcast.Close()

	ownKey := fmt.Sprintf("usd_lower_than_one_%d", instanceID)
	eofReceiver, err := middleware.CreateExchangeMiddleware(config.MustEnv("EOF_BROADCAST_EXCHANGE"), []string{ownKey}, conn)
	if err != nil {
		log.Fatalf("connect to EOF receiver exchange: %v", err)
	}
	defer eofReceiver.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	var seen, passed int

	filterworker.NewWorkerCoordinated(
		func(t protocol.Transaction) bool {
			seen++
			ok := t.AmountPaid < 1.0
			if seen <= 5 {
				log.Printf("[lower_than_1_filter] txn=%d amount=%.6f pass=%v", seen, t.AmountPaid, ok)
			}
			if ok {
				passed++
				if passed%1000 == 0 {
					log.Printf("[lower_than_1_filter] passed=%d seen=%d", passed, seen)
				}
			}
			return ok
		},
		[]*filterworker.Output{
			{Middleware: outputMW, GetKey: nil, EOFMiddleware: outputMW},
		},
		inputMW,
		eofBroadcast,
		eofReceiver,
		config.UpstreamCount(),
	).Run()
}
