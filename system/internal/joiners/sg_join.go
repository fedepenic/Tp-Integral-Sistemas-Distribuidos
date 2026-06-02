package joiners

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/aggregators"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// SGJoin joins fanout and fanin results into scatter-gather items.
type SGJoin struct {
	inputMW       middleware.Middleware
	outputMW      middleware.Middleware
	upstreamTotal int

	mu           sync.Mutex
	fanOutByMid  map[string]map[string][]aggregators.FanOutResult
	fanInByMid   map[string]map[string][]fanInResult
	eofOutCount  map[string]int
	eofInCount   map[string]int
	eofPropagate map[string]bool
}

func NewSGJoin(inputMW, outputMW middleware.Middleware, upstreamTotal int) *SGJoin {
	return &SGJoin{
		inputMW:       inputMW,
		outputMW:      outputMW,
		upstreamTotal: upstreamTotal,
		fanOutByMid:   make(map[string]map[string][]aggregators.FanOutResult),
		fanInByMid:    make(map[string]map[string][]fanInResult),
		eofOutCount:   make(map[string]int),
		eofInCount:    make(map[string]int),
		eofPropagate:  make(map[string]bool),
	}
}

func (j *SGJoin) Run() {
	err := j.inputMW.StartConsuming(j.handle)
	if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
		log.Printf("[sg_join] input consumer error: %v", err)
	}
}

func (j *SGJoin) handle(msg middleware.Message, ack func(), nack func()) {
	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("[sg_join] malformed batch: %v", err)
		ack()
		return
	}

	clientID := batch.ClientID
	if clientID == "" {
		clientID = "default"
	}

	if batch.Type == protocol.BatchTypeEOF {
		j.handleEOF(clientID, batch.DataType, ack, nack)
		return
	}

	switch batch.DataType {
	case "fanout_result":
		j.handleFanOut(clientID, batch.Records, ack, nack)
	case "fanin_result":
		j.handleFanIn(clientID, batch.Records, ack, nack)
	default:
		ack()
	}
}

func (j *SGJoin) handleEOF(clientID, dataType string, ack func(), nack func()) {
	j.mu.Lock()
	switch dataType {
	case "fanout_result":
		j.eofOutCount[clientID]++
	case "fanin_result":
		j.eofInCount[clientID]++
	default:
		j.eofOutCount[clientID]++
	}
	outDone := j.eofOutCount[clientID] >= j.upstreamTotal
	inDone := j.eofInCount[clientID] >= j.upstreamTotal
	alreadyPropagated := j.eofPropagate[clientID]
	if outDone && inDone && !alreadyPropagated {
		j.eofPropagate[clientID] = true
	}
	shouldSend := outDone && inDone && !alreadyPropagated
	j.mu.Unlock()

	if !shouldSend {
		ack()
		return
	}

	outBatch := protocol.Batch{Type: protocol.BatchTypeEOF, ClientID: clientID}
	data, err := json.Marshal(outBatch)
	if err != nil {
		nack()
		return
	}
	if err := j.outputMW.Send(middleware.Message{Body: string(data)}); err != nil {
		nack()
		return
	}
	ack()
}

func (j *SGJoin) handleFanOut(clientID string, records json.RawMessage, ack func(), nack func()) {
	var results []aggregators.FanOutResult
	if err := json.Unmarshal(records, &results); err != nil {
		nack()
		return
	}

	j.mu.Lock()
	fanOutMap := j.fanOutByMid[clientID]
	if fanOutMap == nil {
		fanOutMap = make(map[string][]aggregators.FanOutResult)
		j.fanOutByMid[clientID] = fanOutMap
	}
	fanInMap := j.fanInByMid[clientID]
	j.mu.Unlock()

	items := make([]protocol.ScatterGatherItem, 0)
	for _, res := range results {
		key := middleKey(res.MiddleBank, res.MiddleAccount)
		j.mu.Lock()
		fanOutMap[key] = append(fanOutMap[key], res)
		fanin := fanInMap[key]
		j.mu.Unlock()

		for _, in := range fanin {
			items = append(items, protocol.ScatterGatherItem{
				FromBank:      res.FromBank,
				FromAccount:   res.FromAccount,
				MiddleBank:    res.MiddleBank,
				MiddleAccount: res.MiddleAccount,
				ToBank:        in.ToBank,
				ToAccount:     in.ToAccount,
			})
		}
	}

	if err := j.sendItems(clientID, items); err != nil {
		nack()
		return
	}
	ack()
}

func (j *SGJoin) handleFanIn(clientID string, records json.RawMessage, ack func(), nack func()) {
	var results []fanInResult
	if err := json.Unmarshal(records, &results); err != nil {
		nack()
		return
	}

	j.mu.Lock()
	fanInMap := j.fanInByMid[clientID]
	if fanInMap == nil {
		fanInMap = make(map[string][]fanInResult)
		j.fanInByMid[clientID] = fanInMap
	}
	fanOutMap := j.fanOutByMid[clientID]
	j.mu.Unlock()

	items := make([]protocol.ScatterGatherItem, 0)
	for _, res := range results {
		key := middleKey(res.MiddleBank, res.MiddleAccount)
		j.mu.Lock()
		fanInMap[key] = append(fanInMap[key], res)
		fanout := fanOutMap[key]
		j.mu.Unlock()

		for _, out := range fanout {
			items = append(items, protocol.ScatterGatherItem{
				FromBank:      out.FromBank,
				FromAccount:   out.FromAccount,
				MiddleBank:    res.MiddleBank,
				MiddleAccount: res.MiddleAccount,
				ToBank:        res.ToBank,
				ToAccount:     res.ToAccount,
			})
		}
	}

	if err := j.sendItems(clientID, items); err != nil {
		nack()
		return
	}
	ack()
}

func (j *SGJoin) sendItems(clientID string, items []protocol.ScatterGatherItem) error {
	if len(items) == 0 {
		return nil
	}
	outBatch := protocol.Batch{
		Type:               protocol.BatchTypeScatterGather,
		ClientID:           clientID,
		ScatterGatherItems: items,
	}
	data, err := json.Marshal(outBatch)
	if err != nil {
		return err
	}
	return j.outputMW.Send(middleware.Message{Body: string(data)})
}

func middleKey(bank, account string) string {
	return bank + "|" + account
}
