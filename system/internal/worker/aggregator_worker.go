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

	input      middleware.Middleware
	output     middleware.Middleware
	controlPub middleware.Middleware
	controlSub middleware.Middleware
	stateMu    sync.Mutex
	taskStates map[string]*taskState[K, S]
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

	return &AggregatorWorker[T, K, S, O]{
		cfg:        cfg,
		extractor:  extractor,
		logic:      logic,
		resultKey:  resultKey,
		input:      input,
		output:     output,
		controlPub: controlPub,
		controlSub: controlSub,
		taskStates: map[string]*taskState[K, S]{},
	}, nil
}

func (w *AggregatorWorker[T, K, S, O]) Run() {
	go func() {
		err := w.controlSub.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
			w.handleControlMessage(msg, ack, nack)
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

func (w *AggregatorWorker[T, K, S, O]) handleControlMessage(msg middleware.Message, ack func(), nack func()) {
	var ctrl ControlMessage
	if err := json.Unmarshal([]byte(msg.Body), &ctrl); err != nil {
		log.Printf("[aggregator] malformed control message: %v", err)
		nack()
		return
	}
	if ctrl.Type != ControlTypeEOF {
		ack()
		return
	}
	if ctrl.SenderID == w.cfg.InstanceID {
		ack()
		return
	}
	clientID := ctrl.ClientID
	if clientID == "" {
		clientID = defaultClientID
	}
	if err := w.handleControlEOF(clientID, ctrl.SenderID, ctrl.Seq); err != nil {
		nack()
		return
	}
	ack()
}

func (w *AggregatorWorker[T, K, S, O]) handleDataMessage(msg middleware.Message, ack func(), nack func()) {
	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		nack()
		log.Printf("[aggregator] malformed batch: %v", err)
		return
	}

	clientID := batch.ClientID
	if clientID == "" {
		clientID = defaultClientID
	}

	if batch.Type == protocol.BatchTypeEOF {
		if err := w.handleEOF(clientID, true); err != nil {
			nack()
			return
		}
		ack()
		return
	}

	items, ok := w.extractor(batch)
	if !ok || len(items) == 0 {
		ack()
		return
	}

	if err := w.handleItems(clientID, items); err != nil {
		nack()
		return
	}
	ack()
}

func (w *AggregatorWorker[T, K, S, O]) handleItems(clientID string, items []T) error {
	shouldFlush := false

	w.stateMu.Lock()
	task := w.getOrCreateTaskLocked(clientID)
	if task.FlushDone || task.Flushing {
		w.stateMu.Unlock()
		return nil
	}
	task.PendingMessages++
	for _, item := range items {
		key := w.logic.Key(item)
		state, ok := task.State[key]
		if !ok {
			state = w.logic.Zero()
		}
		task.State[key] = w.logic.Accumulate(state, item)
	}
	task.PendingMessages--
	if task.canFlush() {
		task.Flushing = true
		shouldFlush = true
	}
	w.stateMu.Unlock()

	if shouldFlush {
		if err := w.flush(clientID); err != nil {
			w.resetFlushing(clientID)
			return err
		}
	}
	return nil
}

func (w *AggregatorWorker[T, K, S, O]) handleEOF(clientID string, publishControl bool) error {
	shouldFlush := false
	pending := 0
	received := 0
	expected := 0
	seq := 0

	w.stateMu.Lock()
	task := w.getOrCreateTaskLocked(clientID)
	if !task.FlushDone && !task.Flushing {
		if task.ReceivedEOFs < task.ExpectedEOFs {
			task.ReceivedEOFs++
		}
		if publishControl {
			task.NextControlSeq++
			seq = task.NextControlSeq
		}
		pending = task.PendingMessages
		received = task.ReceivedEOFs
		expected = task.ExpectedEOFs
		if task.canFlush() {
			task.Flushing = true
			shouldFlush = true
		}
	}
	w.stateMu.Unlock()

	if publishControl {
		if err := w.publishControlEOF(clientID, seq); err != nil {
			return err
		}
	}

	if shouldFlush {
		if err := w.flush(clientID); err != nil {
			w.resetFlushing(clientID)
			return err
		}
		return nil
	}

	log.Printf("[aggregator] EOF registered client=%s pending=%d received=%d expected=%d", clientID, pending, received, expected)
	return nil
}

func (w *AggregatorWorker[T, K, S, O]) handleControlEOF(clientID string, senderID int, seq int) error {
	shouldFlush := false
	pending := 0
	received := 0
	expected := 0

	w.stateMu.Lock()
	task := w.getOrCreateTaskLocked(clientID)
	if !task.FlushDone && !task.Flushing {
		lastSeq := task.LastControlSeq[senderID]
		if seq > lastSeq {
			task.LastControlSeq[senderID] = seq
			if task.ReceivedEOFs < task.ExpectedEOFs {
				task.ReceivedEOFs++
			}
		}
		pending = task.PendingMessages
		received = task.ReceivedEOFs
		expected = task.ExpectedEOFs
		if task.canFlush() {
			task.Flushing = true
			shouldFlush = true
		}
	}
	w.stateMu.Unlock()

	if shouldFlush {
		if err := w.flush(clientID); err != nil {
			w.resetFlushing(clientID)
			return err
		}
		return nil
	}

	log.Printf("[aggregator] control EOF registered client=%s pending=%d received=%d expected=%d", clientID, pending, received, expected)
	return nil
}

func (w *AggregatorWorker[T, K, S, O]) publishControlEOF(clientID string, seq int) error {
	ctrl := ControlMessage{Type: ControlTypeEOF, ClientID: clientID, SenderID: w.cfg.InstanceID, Seq: seq}
	body, err := json.Marshal(ctrl)
	if err != nil {
		return fmt.Errorf("marshal control: %w", err)
	}
	return w.controlPub.Send(middleware.Message{Body: string(body)})
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
		task = newTaskState[K, S](w.cfg.UpstreamInstances)
		w.taskStates[clientID] = task
	}
	return task
}

func (w *AggregatorWorker[T, K, S, O]) resetFlushing(clientID string) {
	w.stateMu.Lock()
	if task, ok := w.taskStates[clientID]; ok {
		task.Flushing = false
	}
	w.stateMu.Unlock()
}
