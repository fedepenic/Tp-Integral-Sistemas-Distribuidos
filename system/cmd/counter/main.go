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

	inputQueue  := config.EnvOrDefault("INPUT_QUEUE", "q5_filtered")
	outputQueue := config.EnvOrDefault("OUTPUT_QUEUE", "q5_count")

	inputMW, err := middleware.CreateQueueMiddleware(inputQueue, conn)
	if err != nil {
		log.Fatalf("[counter] connect to input queue: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.CreateQueueMiddleware(outputQueue, conn)
	if err != nil {
		log.Fatalf("[counter] connect to output queue: %v", err)
	}
	defer outputMW.Close()

	log.Printf("[counter] started: %s -> %s (upstream=%d)", inputQueue, outputQueue, n.UpstreamCount())

	n.Run(inputMW, outputMW, newProcess(outputMW))
}
