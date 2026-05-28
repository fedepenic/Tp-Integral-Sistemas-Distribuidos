package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.New("period2_filter")
	conn := svc.Conn()

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	outputMW := config.Exchange("OUTPUT_EXCHANGE", []string{""}, conn)
	defer outputMW.Close()

	svc.Run(inputMW, outputMW, newProcess(outputMW))
}
