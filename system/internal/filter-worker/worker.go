package filterworker

import (
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// FilterFunc evalúa una transacción y devuelve true si debe conservarse.
type FilterFunc func(t protocol.Transaction) bool

// Worker es el núcleo genérico de todos los filters del sistema.
//
// Soporta TRES modos:
//
//   - Coordinated: eofBroadcast y eofReceiver no son nil. Igual al patrón del
//     cleaner para escalado horizontal: data + EOFs llegan por la inputMW
//     compartida (competing consumers entre instancias), y cuando una
//     instancia recibe un EOF lo retransmite a TODAS las instancias del nivel
//     vía eofBroadcast. eofReceiver entrega esos broadcasts a esta instancia.
//     Conteo per-cliente: cada instancia espera upstreamCount EOFs por cliente
//     y luego, tras drenar su in-flight (cleaner pattern), reenvía 1 EOF por
//     cliente downstream.
//
//   - Single-queue: eofInMW == nil y eofBroadcast == nil. Una sola instancia,
//     EOFs FIFO con la data en la misma queue. Conteo global, propaga al
//     llegar a upstreamCount.
//
//   - Dual-queue (legacy): eofInMW != nil. EOFs por un exchange dedicado,
//     dos consumers en paralelo. Tiene race condition: NO drena al recibir
//     EOF. Se mantiene para filtros que aún no migraron.
type Worker struct {
	filterFn      FilterFunc
	outputs       []*Output
	inputMW       middleware.Middleware
	eofInMW       middleware.Middleware
	eofBroadcast  middleware.Middleware
	eofReceiver   middleware.Middleware
	upstreamCount int

	mu            sync.Mutex
	cond          *sync.Cond
	globalPending int
	clientPending map[string]int
	eofCount      map[string]int
	propagated    map[string]bool
}

// NewWorker construye un Worker en modo single-queue (eofInMW=nil) o
// dual-queue legacy (eofInMW!=nil).
func NewWorker(
	filterFn FilterFunc,
	outputs []*Output,
	inputMW middleware.Middleware,
	eofInMW middleware.Middleware,
	upstreamCount int,
) *Worker {
	return newWorkerInternal(filterFn, outputs, inputMW, eofInMW, nil, nil, upstreamCount)
}

// NewWorkerCoordinated construye un Worker en modo coordinated: usa el patrón
// del cleaner (broadcast de EOFs entre instancias del mismo nivel + drenado
// per-cliente) para que el filtro escale horizontalmente con múltiples
// instancias compitiendo en una shared queue.
//
//   - inputMW:       queue compartida con data + EOFs (competing consumers)
//   - eofBroadcast:  exchange interno con TODOS los routing keys del nivel,
//     usado para retransmitir un EOF que llegó por inputMW a
//     todas las instancias.
//   - eofReceiver:   queue propia de esta instancia bindeada a eofBroadcast
//     con su routing key única, donde llegan los broadcasts.
//   - upstreamCount: cantidad de instancias upstream → cuántos EOFs por
//     cliente esperar antes de propagar.
func NewWorkerCoordinated(
	filterFn FilterFunc,
	outputs []*Output,
	inputMW middleware.Middleware,
	eofBroadcast middleware.Middleware,
	eofReceiver middleware.Middleware,
	upstreamCount int,
) *Worker {
	return newWorkerInternal(filterFn, outputs, inputMW, nil, eofBroadcast, eofReceiver, upstreamCount)
}

func newWorkerInternal(
	filterFn FilterFunc,
	outputs []*Output,
	inputMW middleware.Middleware,
	eofInMW middleware.Middleware,
	eofBroadcast middleware.Middleware,
	eofReceiver middleware.Middleware,
	upstreamCount int,
) *Worker {
	w := &Worker{
		filterFn:      filterFn,
		outputs:       outputs,
		inputMW:       inputMW,
		eofInMW:       eofInMW,
		eofBroadcast:  eofBroadcast,
		eofReceiver:   eofReceiver,
		upstreamCount: upstreamCount,
		clientPending: make(map[string]int),
		eofCount:      make(map[string]int),
		propagated:    make(map[string]bool),
	}
	w.cond = sync.NewCond(&w.mu)
	return w
}

// Run bloquea hasta que el worker termina.
// El caller es responsable de llamar Close() en todos los middlewares al salir.
func (w *Worker) Run() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[filter-worker] SIGTERM — shutting down")
		w.inputMW.StopConsuming()
		if w.eofInMW != nil {
			w.eofInMW.StopConsuming()
		}
		if w.eofReceiver != nil {
			w.eofReceiver.StopConsuming()
		}
	}()

	switch {
	case w.eofBroadcast != nil && w.eofReceiver != nil:
		w.runCoordinated()
	case w.eofInMW != nil:
		w.runDualQueue()
	default:
		w.runSingleQueue()
	}

	log.Println("[filter-worker] done")
}

// runCoordinated implementa el patrón del cleaner para múltiples instancias
// del filtro compartiendo una input queue.
func (w *Worker) runCoordinated() {
	var wg sync.WaitGroup

	// EOF receiver: consume broadcasts internos entre instancias del nivel.
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := w.eofReceiver.StartConsuming(w.handleEOFBroadcast)
		if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
			log.Printf("[filter-worker] eof receiver error: %v", err)
		}
	}()

	// Data consumer: lee data + EOFs de la shared input queue.
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := w.inputMW.StartConsuming(w.handleDataCoordinated)
		if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
			log.Printf("[filter-worker] data consumer error: %v", err)
		}
	}()

	wg.Wait()
}

// handleDataCoordinated procesa data y retransmite EOFs vía eofBroadcast.
// Sigue el mismo patrón que cleaner.handleData.
func (w *Worker) handleDataCoordinated(msg middleware.Message, ack func(), nack func()) {
	w.mu.Lock()
	w.globalPending++
	w.mu.Unlock()

	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("[filter-worker] malformed message — discarding: %v", err)
		w.mu.Lock()
		w.globalPending--
		w.cond.Broadcast()
		w.mu.Unlock()
		ack()
		return
	}

	if batch.Type == protocol.BatchTypeEOF {
		w.mu.Lock()
		w.globalPending--
		w.cond.Broadcast()
		w.mu.Unlock()

		log.Printf("[filter-worker] EOF received from input client=%s — broadcasting to peers", batch.ClientID)
		if err := w.eofBroadcast.Send(msg); err != nil {
			log.Printf("[filter-worker] broadcast EOF: %v", err)
			nack()
			return
		}
		ack()
		return
	}

	if batch.Type != protocol.BatchTypeTransactions {
		w.mu.Lock()
		w.globalPending--
		w.cond.Broadcast()
		w.mu.Unlock()
		ack()
		return
	}

	w.mu.Lock()
	w.globalPending--
	w.clientPending[batch.ClientID]++
	w.mu.Unlock()

	log.Printf("[filter-worker] batch received client=%s txns=%d", batch.ClientID, len(batch.Transactions))

	filtered := w.applyFilter(batch.Transactions)
	log.Printf("[filter-worker] batch filtered client=%s in=%d out=%d", batch.ClientID, len(batch.Transactions), len(filtered))

	if len(filtered) > 0 {
		for i, out := range w.outputs {
			if err := out.publish(batch.ClientID, filtered); err != nil {
				log.Printf("[filter-worker] error publishing to output %d: %v", i, err)
				w.mu.Lock()
				w.clientPending[batch.ClientID]--
				w.cond.Broadcast()
				w.mu.Unlock()
				nack()
				return
			}
		}
	}

	w.mu.Lock()
	w.clientPending[batch.ClientID]--
	w.cond.Broadcast()
	w.mu.Unlock()
	ack()
}

// handleEOFBroadcast procesa los EOFs que retransmitieron las instancias del
// nivel (incluida esta misma) vía eofBroadcast. Cuenta por cliente, espera
// drain y propaga downstream.
func (w *Worker) handleEOFBroadcast(msg middleware.Message, ack func(), nack func()) {
	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("[filter-worker] malformed EOF broadcast — discarding: %v", err)
		ack()
		return
	}

	w.mu.Lock()
	w.eofCount[batch.ClientID]++
	count := w.eofCount[batch.ClientID]
	alreadyPropagated := w.propagated[batch.ClientID]
	w.mu.Unlock()

	log.Printf("[filter-worker] EOF broadcast client=%s (%d/%d)", batch.ClientID, count, w.upstreamCount)
	ack()

	if count < w.upstreamCount || alreadyPropagated {
		return
	}

	// Reclaim "propagator" status atomically.
	w.mu.Lock()
	if w.propagated[batch.ClientID] {
		w.mu.Unlock()
		return
	}
	w.propagated[batch.ClientID] = true

	// Drain in-flight: igual que cleaner.handleEOF.
	for w.globalPending > 0 || w.clientPending[batch.ClientID] > 0 {
		w.cond.Wait()
	}
	w.mu.Unlock()

	log.Printf("[filter-worker] all EOFs received for client=%s — propagating", batch.ClientID)
	for i, out := range w.outputs {
		if err := out.sendEOF(batch.ClientID); err != nil {
			log.Printf("[filter-worker] error propagating EOF for client=%s output=%d: %v", batch.ClientID, i, err)
		}
	}
}

// runSingleQueue: single instancia, data + EOFs en la misma queue, FIFO.
func (w *Worker) runSingleQueue() {
	eofCount := 0

	err := w.inputMW.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
		var batch protocol.Batch
		if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
			log.Printf("[filter-worker] malformed message — discarding: %v", err)
			nack()
			return
		}

		if batch.Type == protocol.BatchTypeEOF {
			eofCount++
			log.Printf("[filter-worker] EOF received (%d/%d)", eofCount, w.upstreamCount)
			ack()

			if eofCount < w.upstreamCount {
				return
			}

			log.Println("[filter-worker] all EOFs received — propagating")
			for i, out := range w.outputs {
				if err := out.sendEOF(batch.ClientID); err != nil {
					log.Printf("[filter-worker] error propagating EOF to output %d: %v", i, err)
				}
			}
			w.inputMW.StopConsuming()
			return
		}

		if batch.Type != protocol.BatchTypeTransactions {
			ack()
			return
		}

		log.Printf("[filter-worker] batch received client=%s txns=%d", batch.ClientID, len(batch.Transactions))

		filtered := w.applyFilter(batch.Transactions)
		log.Printf("[filter-worker] batch filtered client=%s in=%d out=%d", batch.ClientID, len(batch.Transactions), len(filtered))

		if len(filtered) == 0 {
			ack()
			return
		}

		for i, out := range w.outputs {
			if err := out.publish(batch.ClientID, filtered); err != nil {
				log.Printf("[filter-worker] error publishing to output %d: %v", i, err)
				nack()
				return
			}
		}
		ack()
	})

	if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
		log.Printf("[filter-worker] data consumer error: %v", err)
	}
}

// runDualQueue (legacy): consumers separados para data y EOF. NO drena al
// recibir EOF — usar solo en filtros que no migraron al patrón coordinado.
func (w *Worker) runDualQueue() {
	done := make(chan struct{})

	var eofCount atomic.Int32
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		err := w.eofInMW.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
			current := eofCount.Add(1)
			ack()
			log.Printf("[filter-worker] EOF received (%d/%d)", current, w.upstreamCount)

			if int(current) < w.upstreamCount {
				return
			}

			log.Println("[filter-worker] all EOFs received — propagating")
			for i, out := range w.outputs {
				if err := out.sendEOF(""); err != nil {
					log.Printf("[filter-worker] error propagating EOF to output %d: %v", i, err)
				}
			}

			close(done)
			w.eofInMW.StopConsuming()
		})

		if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
			log.Printf("[filter-worker] eof consumer error: %v", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		go func() {
			<-done
			w.inputMW.StopConsuming()
		}()

		err := w.inputMW.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
			var batch protocol.Batch
			if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
				log.Printf("[filter-worker] malformed message — discarding: %v", err)
				nack()
				return
			}

			if batch.Type != protocol.BatchTypeTransactions {
				ack()
				return
			}

			log.Printf("[filter-worker] batch received client=%s txns=%d", batch.ClientID, len(batch.Transactions))

			filtered := w.applyFilter(batch.Transactions)
			log.Printf("[filter-worker] batch filtered client=%s in=%d out=%d", batch.ClientID, len(batch.Transactions), len(filtered))

			if len(filtered) == 0 {
				ack()
				return
			}

			for i, out := range w.outputs {
				if err := out.publish(batch.ClientID, filtered); err != nil {
					log.Printf("[filter-worker] error publishing to output %d: %v", i, err)
					nack()
					return
				}
			}

			ack()
		})

		if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
			log.Printf("[filter-worker] data consumer error: %v", err)
		}
	}()

	wg.Wait()
}

// applyFilter retorna solo las transacciones que pasan la condición.
func (w *Worker) applyFilter(txns []protocol.Transaction) []protocol.Transaction {
	out := make([]protocol.Transaction, 0, len(txns))
	for _, tx := range txns {
		if w.filterFn(tx) {
			out = append(out, tx)
		}
	}
	return out
}
