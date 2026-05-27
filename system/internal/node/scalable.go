package node

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/config"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// ProcessFunc is the only piece of business logic a scalable node must provide.
// It receives a data batch and returns the output batch and whether to send it.
// Return ok=false to discard the batch without sending anything downstream.
// EOF batches are never passed to ProcessFunc — the node handles them internally.
type ProcessFunc func(batch protocol.Batch) (result protocol.Batch, ok bool)

// Scalable is a horizontally-scalable pipeline node.
//
// All instances of the same node compete for messages on a shared input queue.
// When any instance receives a client EOF it broadcasts it to all peer instances
// via a dedicated internal exchange. Each peer independently waits until its own
// in-flight work for that client finishes, then forwards the EOF downstream.
// This means downstream always receives exactly N EOFs — one per running instance.
//
// Required environment variables:
//
//	INSTANCE_ID, INSTANCE_TOTAL  — identity within the peer group
//	EOF_EXCHANGE                 — name of the internal coordination exchange
//	RABBITMQ_HOST, RABBITMQ_PORT
type Scalable struct {
	name          string
	instanceID    int
	instanceTotal int
	conn          middleware.ConnSettings

	// in-flight tracking — guarded by mu
	mu            sync.Mutex
	cond          *sync.Cond
	globalPending int            // dequeued but clientID not yet known
	clientPending map[string]int // actively being processed, per client
}

// New reads INSTANCE_ID, INSTANCE_TOTAL and RabbitMQ connection settings from
// the environment. name is the service name used to build per-instance peer
// routing keys, e.g. "cleaner" produces "cleaner_1", "cleaner_2", etc.
func New(name string) *Scalable {
	s := &Scalable{
		name:          name,
		instanceID:    mustInt("INSTANCE_ID"),
		instanceTotal: mustInt("INSTANCE_TOTAL"),
		conn:          config.ConnSettings(),
		clientPending: make(map[string]int),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Conn returns the RabbitMQ connection settings so the caller can build its
// own input and output middlewares without duplicating the connection setup.
func (s *Scalable) Conn() middleware.ConnSettings { return s.conn }

// Run sets up the internal EOF broadcast exchange, subscribes this instance to
// its own receiver queue, and starts both consumer goroutines. It blocks until
// both goroutines finish (SIGTERM or queue closed).
//
// inputMW  — shared input queue (competing consumers across all instances).
// outputMW — where processed batches and EOFs are written downstream.
// fn       — the business logic: transforms each incoming data batch.
func (s *Scalable) Run(inputMW, outputMW middleware.Middleware, fn ProcessFunc) {
	allKeys := make([]string, s.instanceTotal)
	for i := 0; i < s.instanceTotal; i++ {
		allKeys[i] = fmt.Sprintf("%s_%d", s.name, i+1)
	}
	ownKey := fmt.Sprintf("%s_%d", s.name, s.instanceID)
	eofExchange := config.EnvOrDefault("EOF_EXCHANGE", s.name+"_eof")

	eofBroadcast, err := middleware.CreateExchangeMiddleware(eofExchange, allKeys, s.conn)
	if err != nil {
		log.Fatalf("[%s] connect to EOF broadcast exchange: %v", s.name, err)
	}
	defer eofBroadcast.Close()

	eofReceiver, err := middleware.CreateExchangeMiddleware(eofExchange, []string{ownKey}, s.conn)
	if err != nil {
		log.Fatalf("[%s] connect to EOF receiver queue: %v", s.name, err)
	}
	defer eofReceiver.Close()

	log.Printf("[%s] %d/%d started", s.name, s.instanceID, s.instanceTotal)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := eofReceiver.StartConsuming(s.handleEOF(outputMW)); err != nil &&
			err != middleware.ErrMessageMiddlewareDisconnected {
			log.Printf("[%s] EOF receiver error: %v", s.name, err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := inputMW.StartConsuming(s.handleData(outputMW, eofBroadcast, fn)); err != nil &&
			err != middleware.ErrMessageMiddlewareDisconnected {
			log.Printf("[%s] data consumer error: %v", s.name, err)
		}
	}()

	wg.Wait()
}

// handleData returns the input queue callback.
// Data batches are passed to fn and the result sent downstream.
// EOF batches are broadcast to all peer instances.
func (s *Scalable) handleData(outputMW, eofBroadcast middleware.Middleware, fn ProcessFunc) func(middleware.Message, func(), func()) {
	return func(msg middleware.Message, ack func(), nack func()) {
		// Increment before deserialization so the EOF drain cannot slip through
		// while this message's clientID is still unknown.
		s.mu.Lock()
		s.globalPending++
		s.mu.Unlock()

		var batch protocol.Batch
		if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
			log.Printf("[%s] malformed message — discarding: %v", s.name, err)
			s.mu.Lock()
			s.globalPending--
			s.cond.Broadcast()
			s.mu.Unlock()
			ack()
			return
		}

		if batch.Type == protocol.BatchTypeEOF {
			// Release globalPending before broadcasting so the drain in handleEOF
			// can complete without waiting on this goroutine.
			s.mu.Lock()
			s.globalPending--
			s.cond.Broadcast()
			s.mu.Unlock()
			if err := eofBroadcast.Send(msg); err != nil {
				log.Printf("[%s] broadcast EOF client=%s: %v", s.name, batch.ClientID, err)
				nack()
				return
			}
			ack()
			return
		}

		// Atomically move from globalPending to clientPending so the message
		// is never invisible to both counters simultaneously.
		s.mu.Lock()
		s.globalPending--
		s.clientPending[batch.ClientID]++
		s.mu.Unlock()

		result, ok := fn(batch)

		if !ok {
			s.mu.Lock()
			s.clientPending[batch.ClientID]--
			s.cond.Broadcast()
			s.mu.Unlock()
			ack()
			return
		}

		data, err := json.Marshal(result)
		if err != nil {
			log.Printf("[%s] marshal result: %v", s.name, err)
			s.mu.Lock()
			s.clientPending[batch.ClientID]--
			s.cond.Broadcast()
			s.mu.Unlock()
			nack()
			return
		}
		if err := outputMW.Send(middleware.Message{Body: string(data)}); err != nil {
			log.Printf("[%s] send to output: %v", s.name, err)
			s.mu.Lock()
			s.clientPending[batch.ClientID]--
			s.cond.Broadcast()
			s.mu.Unlock()
			nack()
			return
		}

		s.mu.Lock()
		s.clientPending[batch.ClientID]--
		s.cond.Broadcast()
		s.mu.Unlock()
		ack()
	}
}

// handleEOF returns the EOF receiver callback.
// It blocks until all in-flight work for the client finishes, then forwards the EOF.
func (s *Scalable) handleEOF(outputMW middleware.Middleware) func(middleware.Message, func(), func()) {
	return func(msg middleware.Message, ack func(), nack func()) {
		var batch protocol.Batch
		if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
			log.Printf("[%s] malformed EOF broadcast — discarding: %v", s.name, err)
			ack()
			return
		}

		s.mu.Lock()
		for s.globalPending > 0 || s.clientPending[batch.ClientID] > 0 {
			s.cond.Wait()
		}
		s.mu.Unlock()

		if err := outputMW.Send(msg); err != nil {
			log.Printf("[%s] send EOF client=%s: %v", s.name, batch.ClientID, err)
			nack()
			return
		}
		log.Printf("[%s] EOF forwarded client=%s", s.name, batch.ClientID)
		ack()
	}
}

func mustInt(key string) int {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("env var %s is required", key)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("env var %s must be an integer: %v", key, err)
	}
	return n
}
