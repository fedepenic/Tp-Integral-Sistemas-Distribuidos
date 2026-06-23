package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.NewExclusive("fan_out")
	conn := svc.Conn()

	inputMW := config.ExchangeWithKey("INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	outputMW := config.ExchangePublisher("OUTPUT_EXCHANGE", []string{}, conn)
	defer outputMW.Close()

	f := newFanOut(outputMW)
	f.recover()

	log.Printf("[fan_out] starting with %d clients in state", len(f.state))
	svc.Run(inputMW, outputMW, f.process)
}
