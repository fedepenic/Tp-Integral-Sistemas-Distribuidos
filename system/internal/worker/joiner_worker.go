package worker

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

type JoinerWorker[L any, R any, S any, O any] struct {
	cfg   JoinerConfig[L, R]
	logic JoinerLogic[L, R, S, O]

	stateMu       sync.Mutex
	cond          *sync.Cond
	globalPending int
	clientPending map[string]int

	eofLeftCount  map[string]int
	eofRightCount map[string]int
	propagated    map[string]bool
	clientStates  map[string]S
}

func NewJoinerWorker[L any, R any, S any, O any](
	cfg JoinerConfig[L, R],
	logic JoinerLogic[L, R, S, O],
) *JoinerWorker[L, R, S, O] {
	if cfg.Name == "" {
		cfg.Name = "joiner"
	}
	w := &JoinerWorker[L, R, S, O]{
		cfg:           cfg,
		logic:         logic,
		clientPending: make(map[string]int),
		eofLeftCount:  make(map[string]int),
		eofRightCount: make(map[string]int),
		propagated:    make(map[string]bool),
		clientStates:  make(map[string]S),
	}
	w.cond = sync.NewCond(&w.stateMu)
	return w
}

func (w *JoinerWorker[L, R, S, O]) Run() {
	go func() {
		err := w.cfg.ControlSub.StartConsuming(w.handleEOFBroadcast)
		if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
			log.Printf("[%s] control consumer error: %v", w.cfg.Name, err)
		}
	}()

	err := w.cfg.Input.StartConsuming(w.handleData)
	if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
		log.Printf("[%s] input consumer error: %v", w.cfg.Name, err)
	}
}

func (w *JoinerWorker[L, R, S, O]) Close() {
	_ = w.cfg.Input.Close()
	_ = w.cfg.Output.Close()
	_ = w.cfg.ControlPub.Close()
	_ = w.cfg.ControlSub.Close()
}

func (w *JoinerWorker[L, R, S, O]) handleData(msg middleware.Message, ack func(), nack func()) {
	w.stateMu.Lock()
	w.globalPending++
	w.stateMu.Unlock()

	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("[%s] malformed batch: %v", w.cfg.Name, err)
		w.stateMu.Lock()
		w.globalPending--
		w.cond.Broadcast()
		w.stateMu.Unlock()
		ack()
		return
	}

	clientID := batch.ClientID
	if clientID == "" {
		clientID = defaultClientID
	}

	if batch.Type == protocol.BatchTypeEOF {
		w.stateMu.Lock()
		w.globalPending--
		w.cond.Broadcast()
		w.stateMu.Unlock()

		if err := w.cfg.ControlPub.Send(msg); err != nil {
			log.Printf("[%s] broadcast EOF error: %v", w.cfg.Name, err)
			nack()
			return
		}
		ack()
		log.Printf("[%s] broadcast EOF for client %s", w.cfg.Name, clientID)
		return
	}

	leftItems, leftOK := w.cfg.ExtractLeft(batch)
	rightItems, rightOK := w.cfg.ExtractRight(batch)
	if (!leftOK || len(leftItems) == 0) && (!rightOK || len(rightItems) == 0) {
		w.stateMu.Lock()
		w.globalPending--
		w.cond.Broadcast()
		w.stateMu.Unlock()
		ack()
		return
	}

	w.stateMu.Lock()
	w.globalPending--
	w.clientPending[clientID]++
	if w.propagated[clientID] {
		w.clientPending[clientID]--
		w.cond.Broadcast()
		w.stateMu.Unlock()
		ack()
		return
	}

	state, ok := w.clientStates[clientID]
	if !ok {
		state = w.logic.Zero()
	}
	var outputs []O
	for _, item := range leftItems {
		var out []O
		state, out = w.logic.ProcessLeft(state, item)
		outputs = append(outputs, out...)
	}
	for _, item := range rightItems {
		var out []O
		state, out = w.logic.ProcessRight(state, item)
		outputs = append(outputs, out...)
	}
	w.clientStates[clientID] = state
	w.stateMu.Unlock()

	if err := w.sendResults(clientID, outputs); err != nil {
		w.stateMu.Lock()
		w.clientPending[clientID]--
		w.cond.Broadcast()
		w.stateMu.Unlock()
		nack()
		return
	}

	w.stateMu.Lock()
	w.clientPending[clientID]--
	w.cond.Broadcast()
	w.stateMu.Unlock()
	ack()
}

func (w *JoinerWorker[L, R, S, O]) handleEOFBroadcast(msg middleware.Message, ack func(), nack func()) {
	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("[%s] malformed EOF broadcast: %v", w.cfg.Name, err)
		ack()
		return
	}
	if batch.Type != protocol.BatchTypeEOF {
		ack()
		return
	}

	clientID := batch.ClientID
	if clientID == "" {
		clientID = defaultClientID
	}

	_, isLeftEOF := w.cfg.ExtractLeft(batch)
	_, isRightEOF := w.cfg.ExtractRight(batch)

	w.stateMu.Lock()
	switch {
	case isLeftEOF && !isRightEOF:
		w.eofLeftCount[clientID]++
	case isRightEOF:
		w.eofRightCount[clientID]++
	default:
		w.eofRightCount[clientID]++
	}
	leftCount := w.eofLeftCount[clientID]
	rightCount := w.eofRightCount[clientID]
	leftDone := leftCount >= w.cfg.LeftUpstreamInstances
	rightDone := rightCount >= w.cfg.RightUpstreamInstances
	alreadyPropagated := w.propagated[clientID]
	w.stateMu.Unlock()

	log.Printf("[%s] EOF broadcast client=%s dataType=%s left=%d/%d right=%d/%d",
		w.cfg.Name, clientID, batch.DataType,
		leftCount, w.cfg.LeftUpstreamInstances,
		rightCount, w.cfg.RightUpstreamInstances,
	)
	ack()

	if !leftDone || !rightDone || alreadyPropagated {
		return
	}

	w.stateMu.Lock()
	if w.propagated[clientID] {
		w.stateMu.Unlock()
		return
	}
	w.propagated[clientID] = true
	for w.globalPending > 0 || w.clientPending[clientID] > 0 {
		w.cond.Wait()
	}
	state, ok := w.clientStates[clientID]
	if !ok {
		state = w.logic.Zero()
	}
	outputs := w.logic.Flush(state)
	w.stateMu.Unlock()

	log.Printf("[%s] all EOFs received for client=%s - flushing pending and propagating", w.cfg.Name, clientID)

	if err := w.sendResults(clientID, outputs); err != nil {
		log.Printf("[%s] error flushing pending for client=%s: %v", w.cfg.Name, clientID, err)
	}
	if err := w.sendEOF(clientID); err != nil {
		log.Printf("[%s] send EOF downstream: %v", w.cfg.Name, err)
		return
	}

	w.stateMu.Lock()
	delete(w.clientStates, clientID)
	delete(w.eofLeftCount, clientID)
	delete(w.eofRightCount, clientID)
	delete(w.propagated, clientID)
	delete(w.clientPending, clientID)
	w.stateMu.Unlock()
}

func (w *JoinerWorker[L, R, S, O]) sendResults(clientID string, results []O) error {
	if len(results) == 0 {
		return nil
	}
	records, err := json.Marshal(results)
	if err != nil {
		return fmt.Errorf("marshal records: %w", err)
	}
	outBatch := protocol.Batch{
		Type:     protocol.BatchTypeData,
		ClientID: clientID,
		DataType: w.cfg.OutputDataType,
		Records:  records,
	}
	data, err := json.Marshal(outBatch)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	return w.cfg.Output.Send(middleware.Message{Body: string(data)})
}

func (w *JoinerWorker[L, R, S, O]) sendEOF(clientID string) error {
	outBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: w.cfg.OutputDataType,
	}
	data, err := json.Marshal(outBatch)
	if err != nil {
		return fmt.Errorf("marshal EOF: %w", err)
	}
	return w.cfg.Output.Send(middleware.Message{Body: string(data)})
}
