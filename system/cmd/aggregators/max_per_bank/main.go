package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.NewExclusive("max_per_bank")
	conn := svc.Conn()

	inputMW := config.DurableExchangeWithKey("INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	outputMW := config.ExchangePublisher("OUTPUT_EXCHANGE", []string{}, conn)
	defer outputMW.Close()

	m := newMaxPerBank(outputMW)
	m.recover()

	log.Printf("[max_per_bank] starting with %d clients in state", len(m.state))
	svc.Run(inputMW, outputMW, m.process)
}
