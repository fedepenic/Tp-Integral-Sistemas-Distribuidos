package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.New("period1_filter")
	conn := svc.Conn()

	inputMW := config.SharedQueue("INPUT_QUEUE_NAME", "INPUT_EXCHANGE", []string{""}, conn)
	defer inputMW.Close()

	outQ3MW := config.Exchange("OUTPUT_Q3_EXCHANGE", []string{}, conn)
	defer outQ3MW.Close()

	outQ4MW := config.Exchange("OUTPUT_Q4_EXCHANGE", []string{}, conn)
	defer outQ4MW.Close()

	q3KeyPrefix  := config.EnvOrDefault("OUTPUT_Q3_KEY_PREFIX", "avgfmt")
	q3Partitions := config.MustEnvInt("OUTPUT_Q3_PARTITIONS")
	q4KeyPrefix  := config.EnvOrDefault("OUTPUT_Q4_KEY_PREFIX", "q4sf")
	q4Partitions := config.MustEnvInt("OUTPUT_Q4_PARTITIONS")

	svc.Run(inputMW, outQ3MW, newProcess(
		outQ3MW, outQ4MW,
		q3KeyPrefix, q3Partitions,
		q4KeyPrefix, q4Partitions,
	))
}
