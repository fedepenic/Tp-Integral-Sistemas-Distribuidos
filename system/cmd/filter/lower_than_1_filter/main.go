package main

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.New("usd_lower_than_one")
	conn := svc.Conn()

	inputMW := config.Queue("INPUT_QUEUE", conn)
	defer inputMW.Close()

	outputMW := config.Queue("OUTPUT_QUEUE", conn)
	defer outputMW.Close()

	svc.Run(inputMW, outputMW, newProcess())
}
