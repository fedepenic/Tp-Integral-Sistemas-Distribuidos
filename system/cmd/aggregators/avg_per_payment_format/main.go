package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.NewExclusive("avg_per_payment_format")
	conn := svc.Conn()

	inputMW := config.ExchangeWithKey("INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	outputMW := config.Exchange("OUTPUT_EXCHANGE", []string{}, conn)
	defer outputMW.Close()

	m := newAvgPerPaymentFormat(outputMW)
	svc.Run(inputMW, outputMW, m.process)
}
