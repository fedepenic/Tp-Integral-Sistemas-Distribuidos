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
// Cada filter concreto lo construye con su propia FilterFunc y sus Outputs.
//
// Flujo:
//  1. Consume batches de datos desde inputMW.
//  2. Aplica filterFn a cada transacción.
//  3. Publica los resultados agrupados a cada Output.
//  4. Escucha EOFs en eofInMW; cuando recibe upstreamCount EOFs,
//     propaga un EOF a cada Output y se detiene.
type Worker struct {
	filterFn      FilterFunc
	outputs       []*Output
	inputMW       middleware.Middleware
	eofInMW       middleware.Middleware
	upstreamCount int
}

// NewWorker construye un Worker listo para ejecutar.
//
// Parámetros:
//   - filterFn:      condición de filtrado
//   - outputs:       lista de destinos de publicación
//   - inputMW:       middleware de la queue de datos de entrada
//   - eofInMW:       middleware del exchange de EOF de entrada
//   - upstreamCount: cantidad de EOFs a recibir antes de propagar
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

// Run bloquea hasta que el worker termina (todos los EOFs recibidos o SIGTERM).
// El caller es responsable de llamar Close() en todos los middlewares al salir.
func (w *Worker) Run() {
	// Manejo centralizado de SIGTERM: detiene ambos consumers limpiamente.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[filter-worker] SIGTERM — shutting down")
		w.inputMW.StopConsuming()
		w.eofInMW.StopConsuming()
	}()

	// done se cierra cuando se propagaron todos los EOFs de salida.
	// La goroutine de datos lo usa para detenerse limpiamente.
	done := make(chan struct{})

	var eofCount atomic.Int32
	var wg sync.WaitGroup

	// ── Goroutine: consumer de EOFs ───────────────────────────────────────────
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

			// Recibimos todos los EOFs upstream: propagar uno por cada output.
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

	// ── Goroutine: consumer de datos ──────────────────────────────────────────
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Cuando el EOF se propague, detenemos el consumer de datos.
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

			// Los EOFs llegan por el exchange separado, no por esta queue.
			if batch.Type != protocol.BatchTypeTransactions {
				ack()
				return
			}

			filtered := w.applyFilter(batch.Transactions)

			// Si no quedó nada tras el filtro no publicamos.
			if len(filtered) == 0 {
				ack()
				return
			}

			// Publicar a cada output (con su propia lógica de agrupado por key).
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
	log.Println("[filter-worker] done")
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
