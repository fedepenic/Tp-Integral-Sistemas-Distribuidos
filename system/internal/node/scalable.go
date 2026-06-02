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

// Scalable is a horizontally-scalable pipeline node. It embeds Node and
// extends it with peer-coordination for EOF propagation:
//
// All instances compete for messages on a shared input queue. When any
// instance receives a client EOF it broadcasts it to all peer instances.
// Each instance counts received broadcasts per client; once the count
// reaches UPSTREAM_INSTANCES, that instance drains its in-flight work
// and forwards one EOF downstream. Every instance does this, so
// downstream receives N EOFs — one per running instance.
//
// NewJoin produces a Scalable in two-sided join mode. In join mode the
// node counts EOFs from a left upstream (e.g. accounts) and a right
// upstream (e.g. aggregator results) independently, forwarding the
// downstream EOF only when both sides are satisfied. Because each join
// instance owns its own partition queue there is no need to coordinate
// with peer instances, so the EOF exchange only loops back to the same
// instance.
//
// Additional required environment variables (beyond Node's):
//
//	INSTANCE_ID, INSTANCE_TOTAL  — identity within the peer group
type Scalable struct {
	Node // embeds Node: provides Conn(), upstreamCount, and RABBITMQ settings

	name          string
	instanceID    int
	instanceTotal int

	// in-flight tracking — guarded by mu
	mu            sync.Mutex
	cond          *sync.Cond
	globalPending int
	clientPending map[string]int

	// single-sided EOF counting (New)
	eofCount map[string]int

	// two-sided EOF counting (NewJoin)
	leftUpstream  int
	rightUpstream int
	eofLeftCount  map[string]int
	eofRightCount map[string]int
	classifyEOF   func(protocol.Batch) bool // true = left, false = right
}

// New reads instance identity and connection settings from the environment.
// name is the service name used to build per-instance peer routing keys.
func New(name string) *Scalable {
	s := &Scalable{
		Node:          newNode(),
		name:          name,
		instanceID:    mustInt("INSTANCE_ID"),
		instanceTotal: mustInt("INSTANCE_TOTAL"),
		clientPending: make(map[string]int),
		eofCount:      make(map[string]int),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// NewJoin creates a Scalable in two-sided join mode. leftUpstream and
// rightUpstream are the expected EOF counts from each side. classify
// returns true for left-side EOFs and false for right-side EOFs.
//
// In join mode the EOF exchange only routes to the calling instance
// (no peer broadcast), since each join instance owns its own partition.
func NewJoin(name string, leftUpstream, rightUpstream int, classify func(protocol.Batch) bool) *Scalable {
	s := &Scalable{
		Node:          newNode(),
		name:          name,
		instanceID:    mustInt("INSTANCE_ID"),
		instanceTotal: mustInt("INSTANCE_TOTAL"),
		clientPending: make(map[string]int),
		eofCount:      make(map[string]int),
		leftUpstream:  leftUpstream,
		rightUpstream: rightUpstream,
		eofLeftCount:  make(map[string]int),
		eofRightCount: make(map[string]int),
		classifyEOF:   classify,
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Run sets up the internal EOF broadcast exchange, subscribes this instance
// to its own receiver queue, and starts both consumer goroutines.
// It blocks until both goroutines finish (SIGTERM or queue closed).
func (s *Scalable) Run(inputMW, outputMW middleware.Middleware, fn ProcessFunc) {
	ownKey := fmt.Sprintf("%s_%d", s.name, s.instanceID)
	eofExchange := config.EnvOrDefault("EOF_EXCHANGE", s.name+"_eof")

	// In join mode each instance only routes EOFs to itself — no peer broadcast.
	var broadcastKeys []string
	if s.leftUpstream > 0 {
		broadcastKeys = []string{ownKey}
	} else {
		broadcastKeys = make([]string, s.instanceTotal)
		for i := 0; i < s.instanceTotal; i++ {
			broadcastKeys[i] = fmt.Sprintf("%s_%d", s.name, i+1)
		}
	}

	eofBroadcast, err := middleware.CreateExchangeMiddleware(eofExchange, broadcastKeys, s.conn)
	if err != nil {
		log.Fatalf("[%s] connect to EOF broadcast exchange: %v", s.name, err)
	}
	defer eofBroadcast.Close()

	eofReceiver, err := middleware.CreateExchangeMiddleware(eofExchange, []string{ownKey}, s.conn)
	if err != nil {
		log.Fatalf("[%s] connect to EOF receiver queue: %v", s.name, err)
	}
	defer eofReceiver.Close()

	if s.leftUpstream > 0 {
		log.Printf("[%s] %d/%d started (left_upstream=%d right_upstream=%d)",
			s.name, s.instanceID, s.instanceTotal, s.leftUpstream, s.rightUpstream)
	} else {
		log.Printf("[%s] %d/%d started (upstream=%d)", s.name, s.instanceID, s.instanceTotal, s.upstreamCount)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := eofReceiver.StartConsuming(s.handleEOF(outputMW, fn)); err != nil &&
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

func (s *Scalable) handleData(outputMW, eofBroadcast middleware.Middleware, fn ProcessFunc) func(middleware.Message, func(), func()) {
	return func(msg middleware.Message, ack func(), nack func()) {
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

func (s *Scalable) handleEOF(outputMW middleware.Middleware, fn ProcessFunc) func(middleware.Message, func(), func()) {
	return func(msg middleware.Message, ack func(), nack func()) {
		var batch protocol.Batch
		if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
			log.Printf("[%s] malformed EOF broadcast — discarding: %v", s.name, err)
			ack()
			return
		}

		clientID := batch.ClientID

		// Count EOFs — single-sided or two-sided depending on mode.
		var ready bool
		s.mu.Lock()
		if s.leftUpstream > 0 {
			if s.classifyEOF(batch) {
				s.eofLeftCount[clientID]++
			} else {
				s.eofRightCount[clientID]++
			}
			left := s.eofLeftCount[clientID]
			right := s.eofRightCount[clientID]
			ready = left >= s.leftUpstream && right >= s.rightUpstream
			log.Printf("[%s] EOF client=%s left=%d/%d right=%d/%d",
				s.name, clientID, left, s.leftUpstream, right, s.rightUpstream)
		} else {
			s.eofCount[clientID]++
			count := s.eofCount[clientID]
			ready = count >= s.upstreamCount
			log.Printf("[%s] EOF broadcast client=%s (%d/%d)", s.name, clientID, count, s.upstreamCount)
		}
		s.mu.Unlock()

		if !ready {
			ack()
			return
		}

		s.mu.Lock()
		for s.globalPending > 0 || s.clientPending[clientID] > 0 {
			s.cond.Wait()
		}
		s.mu.Unlock()

		// Give stateful nodes a chance to flush accumulated results before the
		// EOF propagates. Stateless nodes (filters) naturally return ok=false
		// here since the EOF batch carries no transactions to process.
		//
		// If fn returns (eofBatch, true) it means the node handled EOF
		// forwarding itself (e.g. via SendWithKey per partition). Skip our own
		// outputMW.Send so the EOF is not duplicated.
		if result, ok := fn(batch); ok {
			if result.Type == protocol.BatchTypeEOF {
				log.Printf("[%s] EOF forwarded by fn for client=%s", s.name, clientID)
				ack()
				return
			}
			data, err := json.Marshal(result)
			if err != nil {
				log.Printf("[%s] marshal flush result client=%s: %v", s.name, clientID, err)
				nack()
				return
			}
			if err := outputMW.Send(middleware.Message{Body: string(data)}); err != nil {
				log.Printf("[%s] send flush result client=%s: %v", s.name, clientID, err)
				nack()
				return
			}
		}

		if err := outputMW.Send(msg); err != nil {
			log.Printf("[%s] send EOF client=%s: %v", s.name, clientID, err)
			nack()
			return
		}
		log.Printf("[%s] EOF forwarded client=%s", s.name, clientID)

		if s.leftUpstream > 0 {
			s.mu.Lock()
			delete(s.eofLeftCount, clientID)
			delete(s.eofRightCount, clientID)
			delete(s.clientPending, clientID)
			s.mu.Unlock()
		}

		ack()
	}
}

func mustInt(key string) int {
	v := mustEnv(key)
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("env var %s must be an integer: %v", key, err)
	}
	return n
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("env var %s is required", key)
	}
	return v
}
