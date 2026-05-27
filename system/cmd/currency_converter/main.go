package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.New("currency_converter")
	conn := svc.Conn()

	inputQueue  := config.EnvOrDefault("INPUT_QUEUE", "wireach_txn")
	outputQueue := config.EnvOrDefault("OUTPUT_QUEUE", "converted_usd")

	inputMW, err := middleware.CreateQueueMiddleware(inputQueue, conn)
	if err != nil {
		log.Fatalf("[currency_converter] connect to input queue: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.CreateQueueMiddleware(outputQueue, conn)
	if err != nil {
		log.Fatalf("[currency_converter] connect to output queue: %v", err)
	}
	defer outputMW.Close()

	svc.Run(inputMW, outputMW, newProcess())
}
