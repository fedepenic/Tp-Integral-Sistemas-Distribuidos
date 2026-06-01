package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.New("max_per_bank")
	conn := svc.Conn()

	inputMW := config.SharedQueueWithKey("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	outputMW := config.Exchange("OUTPUT_EXCHANGE", []string{}, conn)
	defer outputMW.Close()

	m := newMaxPerBank(outputMW)
	svc.Run(inputMW, outputMW, m.process)
}
