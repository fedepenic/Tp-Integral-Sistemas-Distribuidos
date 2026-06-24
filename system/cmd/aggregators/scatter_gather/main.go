package main

import (
	"log"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.NewExclusive("scatter_gather")
	conn := svc.Conn()

	inputMW := config.DurableExchangeWithKey("INPUT_EXCHANGE", "INPUT_KEY", conn)
	defer inputMW.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	sg := newScatterGather(outputMW)
	sg.recover()

	log.Printf("[scatter_gather] starting with %d clients in state", len(sg.state))
	svc.Run(inputMW, outputMW, newProcess(sg))
}
