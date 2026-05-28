package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	n := node.NewNode()
	conn := n.Conn()

	inputQueue  := config.EnvOrDefault("INPUT_QUEUE", "q1_results")
	outputQueue := config.EnvOrDefault("OUTPUT_QUEUE", "reports")
	queryID     := config.EnvOrDefault("QUERY_ID", "1")

	inputMW, err := middleware.CreateQueueMiddleware(inputQueue, conn)
	if err != nil {
		log.Fatalf("[sink_%s] connect to input queue: %v", queryID, err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.CreateQueueMiddleware(outputQueue, conn)
	if err != nil {
		log.Fatalf("[sink_%s] connect to output queue: %v", queryID, err)
	}
	defer outputMW.Close()

	log.Printf("[sink_%s] started: %s -> %s (upstream=%d)", queryID, inputQueue, outputQueue, n.UpstreamCount())

	n.Run(inputMW, outputMW, newProcess(queryID))
}
