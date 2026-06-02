package main

import "github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"

type joinQ3State struct {
	thresholdsByFormat map[string]float64
	pendingTxns        map[string][]protocol.Transaction
}
