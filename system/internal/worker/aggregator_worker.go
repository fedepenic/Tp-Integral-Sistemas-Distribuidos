package worker

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

type AggregatorWorker[T any, K comparable, S any, O any] struct {
	/// T = Tipo de item de entrada
	/// K = Tipo de clave de agregacion
	/// S = Tipo del estado interno
	/// O = Tipo del resultado final
	cfg       AggregatorConfig
	extractor BatchExtractor[T]
	logic     AggregatorLogic[T, K, S, O]
	resultKey ResultKeyFunc[O]

	input         middleware.Middleware
	output        middleware.Middleware
	controlPub    middleware.Middleware
	controlSub    middleware.Middleware
	stateMu       sync.Mutex
	cond          *sync.Cond
	globalPending int
	clientPending map[string]int
	eofCount      map[string]int
	propagated    map[string]bool
	taskStates    map[string]*taskState[K, S]
}

func NewAggregatorWorker[T any, K comparable, S any, O any](
	cfg AggregatorConfig,
	extractor BatchExtractor[T],
	logic AggregatorLogic[T, K, S, O],
	resultKey ResultKeyFunc[O],
) (*AggregatorWorker[T, K, S, O], error) {
	if cfg.UpstreamInstances < 1 {
		return nil, fmt.Errorf("upstream instances must be >= 1")
	}
	if cfg.InputExchange == "" {
		return nil, fmt.Errorf("input exchange is required")
	}
	if cfg.InputKey == "" {
		return nil, fmt.Errorf("input key is required")
	}
	if cfg.OutputExchange == "" && cfg.OutputQueue == "" {
		return nil, fmt.Errorf("output queue or exchange is required")
	}
	if resultKey != nil {
		if cfg.OutputExchange == "" {
			return nil, fmt.Errorf("output exchange is required for partitioned output")
		}
		if cfg.OutputPartitions < 1 {
			return nil, fmt.Errorf("output partitions must be >= 1")
		}
		if cfg.OutputKeyPrefix == "" {
			return nil, fmt.Errorf("output key prefix is required for partitioned output")
		}
	}
	input, err := middleware.NewExchangeMiddleware(cfg.InputExchange, []string{cfg.InputKey}, cfg.ConnSettings)
	if err != nil {
		return nil, err
	}

	var output middleware.Middleware
	if cfg.OutputExchange != "" {
		output, err = middleware.NewExchangeMiddleware(cfg.OutputExchange, []string{cfg.OutputKey}, cfg.ConnSettings)
	} else {
		output, err = middleware.NewQueueMiddleware(cfg.OutputQueue, cfg.ConnSettings)
	}
	if err != nil {
		_ = input.Close()
		return nil, err
	}

	controlPub, err := middleware.NewExchangeMiddleware(cfg.ControlExchange, []string{cfg.ControlKey}, cfg.ConnSettings)
	if err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}

	controlSub, err := middleware.NewExchangeMiddleware(cfg.ControlExchange, []string{cfg.ControlKey}, cfg.ConnSettings)
	if err != nil {
		_ = input.Close()
		_ = output.Close()
		_ = controlPub.Close()
		return nil, err
	}

	worker := &AggregatorWorker[T, K, S, O]{
		cfg:           cfg,
		extractor:     extractor,
		logic:         logic,
		resultKey:     resultKey,
		input:         input,
		output:        output,
		controlPub:    controlPub,
		controlSub:    controlSub,
		clientPending: map[string]int{},
		eofCount:      map[string]int{},
		propagated:    map[string]bool{},
		taskStates:    map[string]*taskState[K, S]{},
	}
	worker.cond = sync.NewCond(&worker.stateMu)
	return worker, nil
}

func (w *AggregatorWorker[T, K, S, O]) Run() {
	go func() {
		err := w.controlSub.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
			w.handleEOFBroadcast(msg, ack, nack)
		})
		if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
			log.Printf("[aggregator] control consumer error: %v", err)
		}
	}()

	err := w.input.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
		w.handleDataMessage(msg, ack, nack)
	})
	if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
		log.Printf("[aggregator] input consumer error: %v", err)
	}
}

func (w *AggregatorWorker[T, K, S, O]) Close() {
	_ = w.input.Close()
	_ = w.output.Close()
	_ = w.controlPub.Close()
	_ = w.controlSub.Close()
}

func (w *AggregatorWorker[T, K, S, O]) handleEOFBroadcast(msg middleware.Message, ack func(), nack func()) {
	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("[aggregator] malformed EOF broadcast: %v", err)
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

	w.stateMu.Lock()
	w.eofCount[clientID]++
	count := w.eofCount[clientID]
	alreadyPropagated := w.propagated[clientID]
	w.stateMu.Unlock()

	log.Printf("[aggregator] EOF broadcast client=%s (%d/%d)", clientID, count, w.cfg.UpstreamInstances)
	ack()

	if count < w.cfg.UpstreamInstances || alreadyPropagated {
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
	w.stateMu.Unlock()

	if err := w.flush(clientID); err != nil {
		log.Printf("[aggregator] error flushing client=%s: %v", clientID, err)
	}
}

func (w *AggregatorWorker[T, K, S, O]) handleDataMessage(msg middleware.Message, ack func(), nack func()) {
	w.stateMu.Lock()
	w.globalPending++
	w.stateMu.Unlock()

	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		w.stateMu.Lock()
		w.globalPending--
		w.cond.Broadcast()
		w.stateMu.Unlock()
		nack()
		log.Printf("[aggregator] malformed batch: %v", err)
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
		if err := w.controlPub.Send(msg); err != nil {
			log.Printf("[aggregator] broadcast EOF error: %v", err)
			nack()
			return
		}
		ack()
		return
	}

	items, ok := w.extractor(batch)
	if !ok || len(items) == 0 {
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
	w.stateMu.Unlock()

	if err := w.handleItems(clientID, items); err != nil {
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

func (w *AggregatorWorker[T, K, S, O]) handleItems(clientID string, items []T) error {
	w.stateMu.Lock()
	task := w.getOrCreateTaskLocked(clientID)
	if w.propagated[clientID] {
		w.stateMu.Unlock()
		return nil
	}
	for _, item := range items {
		key := w.logic.Key(item)
		state, ok := task.State[key]
		if !ok {
			state = w.logic.Zero()
		}
		task.State[key] = w.logic.Accumulate(state, item)
	}
	w.stateMu.Unlock()
	return nil
}

func (w *AggregatorWorker[T, K, S, O]) flush(clientID string) error {
	w.stateMu.Lock()
	task, ok := w.taskStates[clientID]
	if !ok {
		w.stateMu.Unlock()
		return nil
	}
	state := task.State
	w.stateMu.Unlock()

	var results []O
	for key, value := range state {
		results = append(results, w.logic.Finalize(key, value)...)
	}

	partitions, err := w.sendResults(clientID, results)
	if err != nil {
		return err
	}

	if err := w.sendEOF(partitions, clientID); err != nil {
		return err
	}

	w.stateMu.Lock()
	delete(w.taskStates, clientID)
	w.stateMu.Unlock()
	return nil
}

func (w *AggregatorWorker[T, K, S, O]) sendResults(clientID string, results []O) (map[int]struct{}, error) {
	if w.resultKey == nil {
		resultBatch := ResultBatch[O]{Type: ResultTypeData, ClientID: clientID, Records: results}
		return nil, w.sendResultBatch(resultBatch, "")
	}

	groups := make(map[string][]O)
	for _, result := range results {
		key := w.resultKey(result)
		groups[key] = append(groups[key], result)
	}
	partitions := make(map[int]struct{}, len(groups))
	for key, group := range groups {
		partition := PartitionForKey(key, w.cfg.OutputPartitions)
		partitions[partition] = struct{}{}
		routingKey := RoutingKey(w.cfg.OutputKeyPrefix, partition)
		resultBatch := ResultBatch[O]{Type: ResultTypeData, ClientID: clientID, Records: group}
		if err := w.sendResultBatch(resultBatch, routingKey); err != nil {
			return nil, err
		}
	}
	return partitions, nil
}

func (w *AggregatorWorker[T, K, S, O]) sendEOF(partitions map[int]struct{}, clientID string) error {
	batch := ResultBatch[O]{Type: ResultTypeEOF, ClientID: clientID}
	if len(partitions) == 0 {
		return w.sendResultBatch(batch, "")
	}
	for partition := range partitions {
		routingKey := RoutingKey(w.cfg.OutputKeyPrefix, partition)
		if err := w.sendResultBatch(batch, routingKey); err != nil {
			return err
		}
	}
	return nil
}

func (w *AggregatorWorker[T, K, S, O]) sendResultBatch(batch ResultBatch[O], key string) error {
	data, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	msg := middleware.Message{Body: string(data)}
	if key == "" {
		return w.output.Send(msg)
	}
	return w.output.SendWithKey(msg, key)
}

func (w *AggregatorWorker[T, K, S, O]) getOrCreateTaskLocked(clientID string) *taskState[K, S] {
	task, ok := w.taskStates[clientID]
	if !ok {
		task = newTaskState[K, S]()
		w.taskStates[clientID] = task
	}
	return task
}
