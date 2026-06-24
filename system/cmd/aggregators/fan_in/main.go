package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.NewExclusive("fan_in")
	conn := svc.Conn()

	inputMW := config.DurableExchangeWithKey("INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	outputMW := config.ExchangePublisher("OUTPUT_EXCHANGE", []string{}, conn)
	defer outputMW.Close()

	f := newFanIn(outputMW)
	f.recover()

	log.Printf("[fan_in] starting with %d clients in state", len(f.state))
	svc.Run(inputMW, outputMW, f.process)
}
