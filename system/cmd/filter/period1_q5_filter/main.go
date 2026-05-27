package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	filterworker "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/filter-worker"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// Period 1 Filter (pipeline Q5) — modo coordinated, escalable horizontalmente.
//
// Igual que usd_filter y amt50_filter: shared input queue + broadcast interno
// de EOFs entre instancias del nivel + conteo per-cliente.
//
// Entrada (data + EOFs):
//   - Shared queue (INPUT_QUEUE_NAME) bindeada a transactions_clean con key txn_for_q5
//
// Condicion: Timestamp en [2022-09-01, 2022-09-05]
//
// Salida:
//   - Queue: period1_for_q5  (data y EOF en la misma cola, FIFO para wireach_filter)
//
// Variables de entorno:
//   RABBITMQ_HOST, RABBITMQ_PORT, UPSTREAM_INSTANCES, INSTANCE_ID, INSTANCE_TOTAL
//   INPUT_QUEUE_NAME       -- shared queue compartida entre instancias
//   INPUT_EXCHANGE         -- exchange al que se bindea (transactions_clean)
//   INPUT_KEY              -- routing key del binding (txn_for_q5)
//   EOF_BROADCAST_EXCHANGE -- exchange interno para coordinar EOFs entre instancias
//   OUTPUT_QUEUE           -- cola de salida (period1_for_q5)

const dateLayout = "2006-01-02"

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

	start, _ := time.Parse(dateLayout, "2022-09-01")
	end, _ := time.Parse(dateLayout, "2022-09-05")
	end = end.Add(24 * time.Hour)

	log.Printf("[period1_q5_filter] %d/%d started — window [2022-09-01, 2022-09-05]", instanceID, instanceTotal)

	inputMW := config.SharedQueueWithKey("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	allEOFKeys := make([]string, instanceTotal)
	for i := 0; i < instanceTotal; i++ {
		allEOFKeys[i] = fmt.Sprintf("period1_q5_filter_%d", i+1)
	}
	eofBroadcast := config.Exchange("EOF_BROADCAST_EXCHANGE", allEOFKeys, conn)
	defer eofBroadcast.Close()

	ownKey := fmt.Sprintf("period1_q5_filter_%d", instanceID)
	eofReceiver, err := middleware.CreateExchangeMiddleware(config.MustEnv("EOF_BROADCAST_EXCHANGE"), []string{ownKey}, conn)
	if err != nil {
		log.Fatalf("connect to EOF receiver exchange: %v", err)
	}
	defer eofReceiver.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	var seen, passed, parseErrs int

	filterworker.NewWorkerCoordinated(
		func(t protocol.Transaction) bool {
			seen++
			date := strings.ReplaceAll(t.Timestamp[:10], "/", "-")
			ts, err := time.Parse(dateLayout, date)
			if err != nil {
				parseErrs++
				if parseErrs <= 3 {
					log.Printf("[period1_q5_filter] parse error txn=%d timestamp=%q: %v", seen, t.Timestamp, err)
				}
				return false
			}
			ok := !ts.Before(start) && ts.Before(end)
			if ok {
				passed++
				if passed <= 3 || passed%5000 == 0 {
					log.Printf("[period1_q5_filter] pass #%d date=%s (seen=%d)", passed, date, seen)
				}
			} else if seen <= 5 {
				log.Printf("[period1_q5_filter] skip txn=%d date=%s (out of window)", seen, date)
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
