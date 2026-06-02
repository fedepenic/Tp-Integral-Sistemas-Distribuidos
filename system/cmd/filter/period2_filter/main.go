package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.New("period2_filter")
	conn := svc.Conn()

	inputMW := config.SharedQueue("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", []string{""}, conn)
	defer inputMW.Close()

	outputMW := config.Exchange("OUTPUT_EXCHANGE", []string{}, conn)
	defer outputMW.Close()

	keyPrefix  := config.EnvOrDefault("OUTPUT_KEY_PREFIX", "joinerformat")
	partitions := config.MustEnvInt("OUTPUT_PARTITIONS")

	svc.Run(inputMW, outputMW, newProcess(outputMW, keyPrefix, partitions))
}
