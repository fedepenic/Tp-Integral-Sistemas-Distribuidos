package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.NewExclusive("fan_src_filter")
	conn := svc.Conn()

	inputMW := config.ExchangeWithKey("INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	outFOMW := config.Exchange("OUTPUT_FO_EXCHANGE", []string{}, conn)
	defer outFOMW.Close()

	outFIMW := config.Exchange("OUTPUT_FI_EXCHANGE", []string{}, conn)
	defer outFIMW.Close()

	foKeyPrefix  := config.EnvOrDefault("OUTPUT_FO_KEY_PREFIX", "fo")
	foPartitions := config.MustEnvInt("OUTPUT_FO_PARTITIONS")
	fiKeyPrefix  := config.EnvOrDefault("OUTPUT_FI_KEY_PREFIX", "fi")
	fiPartitions := config.MustEnvInt("OUTPUT_FI_PARTITIONS")

	f := newFanSrcFilter(outFOMW, outFIMW, foKeyPrefix, foPartitions, fiKeyPrefix, fiPartitions)
	svc.Run(inputMW, outFOMW, f.process)
}
