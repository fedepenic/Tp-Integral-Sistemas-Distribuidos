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
// Soporta dos modos según eofInMW:
//
//   - eofInMW == nil → single-queue: data y EOFs llegan por la misma queue
//     (igual que el cleaner desde raw_transactions). El consumer procesa
//     ambos en orden FIFO, y al recibir upstreamCount EOFs propaga.
//
//   - eofInMW != nil → dual-queue: data por inputMW y EOFs por un exchange
//     dedicado eofInMW. Dos consumers en paralelo. Modo legacy que NO drena
//     la queue de datos al recibir EOF — solo úsalo en pipelines en los que
//     ya no se está modificando la lógica.
type Worker struct {
	filterFn      FilterFunc
	outputs       []*Output
	inputMW       middleware.Middleware
	eofInMW       middleware.Middleware
	upstreamCount int
}

// NewWorker construye un Worker listo para ejecutar.
//
// Pasar eofInMW == nil activa el modo single-queue.
func NewWorker(
	filterFn FilterFunc,
	outputs []*Output,
	inputMW middleware.Middleware,
	eofInMW middleware.Middleware,
	upstreamCount int,
) *Worker {
	return &Worker{
		filterFn:      filterFn,
		outputs:       outputs,
		inputMW:       inputMW,
		eofInMW:       eofInMW,
		upstreamCount: upstreamCount,
	}
}

// Run bloquea hasta que el worker termina (todos los EOFs propagados o SIGTERM).
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
	}()

	if w.eofInMW == nil {
		w.runSingleQueue()
	} else {
		w.runDualQueue()
	}

	log.Println("[filter-worker] done")
}

// runSingleQueue: data y EOFs llegan por la misma input queue. Con QoS=1
// y un único consumer, cuando se procesa el EOF Nº upstreamCount se sabe
// que toda la data previa ya fue procesada (FIFO de la queue), así que es
// seguro propagar y detenerse.
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

// runDualQueue: comportamiento legacy con consumers separados para data y EOF.
// Tiene la race condition de no drenar la queue de datos al recibir EOF; se
// mantiene únicamente para filtros que aún no migraron a single-queue.
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
