package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.New("period1_filter")
	conn := svc.Conn()

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	outQ3MW := config.Exchange("OUTPUT_Q3_EXCHANGE", []string{}, conn)
	defer outQ3MW.Close()

	outFOMW := config.Exchange("OUTPUT_Q4_FO_EXCHANGE", []string{}, conn)
	defer outFOMW.Close()

	outFIMW := config.Exchange("OUTPUT_Q4_FI_EXCHANGE", []string{}, conn)
	defer outFIMW.Close()

	q3KeyPrefix  := config.EnvOrDefault("OUTPUT_Q3_KEY_PREFIX", "avgfmt")
	q3Partitions := config.MustEnvInt("OUTPUT_Q3_PARTITIONS")

	svc.Run(inputMW, outQ3MW, newProcess(outQ3MW, outFOMW, outFIMW, q3KeyPrefix, q3Partitions))
}
