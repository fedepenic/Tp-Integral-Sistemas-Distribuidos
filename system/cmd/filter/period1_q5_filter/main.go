package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.New("period1_q5_filter")
	conn := svc.Conn()

	inputMW := config.SharedQueueWithKey("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	svc.Run(inputMW, outputMW, newProcess())
}
