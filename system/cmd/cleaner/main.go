package main

import (
	"log"
	"strings"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
)

func main() {
	svc := node.New("cleaner")
	conn := svc.Conn()

	inputQueue     := config.EnvOrDefault("INPUT_QUEUE", "raw_transactions")
	outputExchange := config.EnvOrDefault("OUTPUT_EXCHANGE", "transactions_clean")
	outputKeys     := strings.Split(config.EnvOrDefault("OUTPUT_KEYS", "txn_for_usd,txn_for_q5"), ",")

	accountsExchange   := config.MustEnv("ACCOUNTS_JOIN_EXCHANGE")
	accountsKeyPrefix  := config.EnvOrDefault("ACCOUNTS_JOIN_KEY_PREFIX", "joinq2")
	accountsPartitions := config.MustEnvInt("ACCOUNTS_JOIN_PARTITIONS")

	inputMW, err := middleware.CreateQueueMiddleware(inputQueue, conn)
	if err != nil {
		log.Fatalf("[cleaner] connect to input queue: %v", err)
	}
	defer inputMW.Close()

	outputMW, err := middleware.CreateExchangeMiddleware(outputExchange, outputKeys, conn)
	if err != nil {
		log.Fatalf("[cleaner] connect to output exchange: %v", err)
	}
	defer outputMW.Close()

	accountsMW, err := middleware.CreateExchangeMiddleware(accountsExchange, []string{}, conn)
	if err != nil {
		log.Fatalf("[cleaner] connect to accounts exchange: %v", err)
	}
	defer accountsMW.Close()

	svc.Run(inputMW, outputMW, newProcess(accountsMW, accountsKeyPrefix, accountsPartitions))
}
