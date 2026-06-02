package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.New("usd_filter")
	conn := svc.Conn()

	inputMW := config.SharedQueueWithKey("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	fanoutMW := config.Exchange("OUTPUT_FANOUT_EXCHANGE", []string{""}, conn)
	defer fanoutMW.Close()

	directMW := config.Exchange("OUTPUT_DIRECT_EXCHANGE", []string{}, conn)
	defer directMW.Close()

	directKey := config.MustEnv("OUTPUT_DIRECT_KEY")

	svc.Run(inputMW, fanoutMW, newProcess(directMW, directKey))
}
