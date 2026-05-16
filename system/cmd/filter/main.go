package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// filterFunc evalúa una transacción y devuelve true si debe conservarse.
type filterFunc func(t protocol.Transaction) bool

// condition es la condición leída de env vars.
type condition struct {
	field string
	op    string
	value string
	end   string
}

// Variables de entorno requeridas:
//
//	FILTER_FIELD      — campo de Transaction: payment_currency | amount_paid | timestamp | payment_format | amount_received | receiving_currency
//	FILTER_OP         — operación: eq | neq | lt | gt | lte | gte | date_range
//	FILTER_VALUE      — valor de referencia (ej: "US Dollar", "50", "2022-09-01")
//	FILTER_END        — (solo date_range) fecha fin inclusive (ej: "2022-09-05")

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[filter] env var %s is required", key)
	}
	return v
}

func loadCondition() condition {
	c := condition{
		field: mustEnv("FILTER_FIELD"),
		op:    mustEnv("FILTER_OP"),
		value: mustEnv("FILTER_VALUE"),
		end:   os.Getenv("FILTER_END"),
	}
	if c.op == "date_range" && c.end == "" {
		log.Fatal("[filter] FILTER_END is required when FILTER_OP=date_range")
	}
	return c
}

func connSettings() middleware.ConnSettings {
	port, err := strconv.Atoi(mustEnv("RABBITMQ_PORT"))
	if err != nil {
		log.Fatalf("[filter] RABBITMQ_PORT must be a number: %v", err)
	}
	return middleware.ConnSettings{
		Hostname: mustEnv("RABBITMQ_HOST"),
		Port:     port,
	}
}

const dateLayout = "2006-01-02"

func buildFilter(c condition) filterFunc {
	switch c.op {

	case "eq":
		return func(t protocol.Transaction) bool {
			return fieldAsString(t, c.field) == c.value
		}

	case "neq":
		return func(t protocol.Transaction) bool {
			return fieldAsString(t, c.field) != c.value
		}

	case "lt", "gt", "lte", "gte":
		ref, err := strconv.ParseFloat(c.value, 64)
		if err != nil {
			log.Fatalf("[filter] FILTER_VALUE %q is not a valid float for op %s: %v", c.value, c.op, err)
		}
		return func(t protocol.Transaction) bool {
			v := fieldAsFloat(t, c.field)
			switch c.op {
			case "lt":
				return v < ref
			case "gt":
				return v > ref
			case "lte":
				return v <= ref
			case "gte":
				return v >= ref
			}
			return false
		}

	case "date_range":
		start, err := time.Parse(dateLayout, c.value)
		if err != nil {
			log.Fatalf("[filter] FILTER_VALUE %q is not a valid date (YYYY-MM-DD): %v", c.value, err)
		}
		end, err := time.Parse(dateLayout, c.end)
		if err != nil {
			log.Fatalf("[filter] FILTER_END %q is not a valid date (YYYY-MM-DD): %v", c.end, err)
		}
		// end es inclusivo: avanzamos al inicio del día siguiente
		end = end.Add(24 * time.Hour)
		return func(t protocol.Transaction) bool {
			ts, err := time.Parse(dateLayout, t.Timestamp[:10])
			if err != nil {
				return false
			}
			return !ts.Before(start) && ts.Before(end)
		}

	default:
		log.Fatalf("[filter] unknown FILTER_OP %q — valid values: eq | neq | lt | gt | lte | gte | date_range", c.op)
	}
	return nil
}

func fieldAsString(t protocol.Transaction, field string) string {
	switch field {
	case "payment_currency":
		return t.PaymentCurrency
	case "receiving_currency":
		return t.ReceivingCurrency
	case "payment_format":
		return t.PaymentFormat
	case "from_bank":
		return t.FromBank
	case "from_account":
		return t.FromAccount
	case "to_bank":
		return t.ToBank
	case "to_account":
		return t.ToAccount
	case "timestamp":
		return t.Timestamp
	default:
		log.Fatalf("[filter] unknown string field %q", field)
	}
	return ""
}

func fieldAsFloat(t protocol.Transaction, field string) float64 {
	switch field {
	case "amount_paid":
		return t.AmountPaid
	case "amount_received":
		return t.AmountReceived
	default:
		log.Fatalf("[filter] unknown numeric field %q — valid: amount_paid | amount_received", field)
	}
	return 0
}

func applyFilter(batch protocol.Batch, fn filterFunc) protocol.Batch {
	filtered := batch.Transactions[:0]
	for _, tx := range batch.Transactions {
		if fn(tx) {
			filtered = append(filtered, tx)
		}
	}
	return protocol.Batch{
		Type:         batch.Type,
		ClientID:     batch.ClientID,
		Transactions: filtered,
	}
}

func main() {
	cond := loadCondition()
	fn := buildFilter(cond)
	conn := connSettings()

	upstreamInstances, err := strconv.Atoi(mustEnv("UPSTREAM_INSTANCES"))
	if err != nil || upstreamInstances < 1 {
		log.Fatalf("[filter] UPSTREAM_INSTANCES must be a positive integer: %v", err)
	}

	log.Printf("[filter] starting — field=%s op=%s value=%q end=%q upstream=%d",
		cond.field, cond.op, cond.value, cond.end, upstreamInstances)

	mwIn, err := middleware.NewQueueMiddleware(mustEnv("INPUT_QUEUE"), conn)
	if err != nil {
		log.Fatalf("[filter] input queue: %v", err)
	}
	defer mwIn.Close()

	mwOut, err := middleware.NewQueueMiddleware(mustEnv("OUTPUT_QUEUE"), conn)
	if err != nil {
		log.Fatalf("[filter] output queue: %v", err)
	}
	defer mwOut.Close()

	eofIn, err := middleware.NewExchangeMiddleware(
		mustEnv("EOF_INPUT_EXCHANGE"),
		[]string{mustEnv("EOF_INPUT_KEY")},
		conn,
	)
	if err != nil {
		log.Fatalf("[filter] eof input exchange: %v", err)
	}
	defer eofIn.Close()

	eofOut, err := middleware.NewExchangeMiddleware(
		mustEnv("EOF_OUTPUT_EXCHANGE"),
		[]string{mustEnv("EOF_OUTPUT_KEY")},
		conn,
	)
	if err != nil {
		log.Fatalf("[filter] eof output exchange: %v", err)
	}
	defer eofOut.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[filter] SIGTERM — shutting down")
		mwIn.StopConsuming()
		eofIn.StopConsuming()
	}()

	// done se cierra cuando se recibieron todos los EOFs upstream y se propagó
	// el EOF propio.
	done := make(chan struct{})

	var eofCount atomic.Int32

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		consumeErr := eofIn.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
			current := eofCount.Add(1)
			ack()
			log.Printf("[filter] EOF received (%d/%d)", current, upstreamInstances)

			if int(current) < upstreamInstances {
				return
			}

			log.Println("[filter] all EOFs received — propagating")
			eofBatch := protocol.Batch{Type: protocol.BatchTypeEOF}
			if sendErr := sendBatch(eofOut, eofBatch); sendErr != nil {
				log.Printf("[filter] error propagating EOF: %v", sendErr)
			}

			close(done)
			eofIn.StopConsuming()
		})

		if consumeErr != nil && consumeErr != middleware.ErrMessageMiddlewareDisconnected {
			log.Printf("[filter] eof consumer error: %v", consumeErr)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		// Cuando el EOF se propague, detenemos el consumer de datos.
		// En ese punto la queue de entrada ya está drenada porque el upstream
		// no manda más datos después de su EOF.
		go func() {
			<-done
			mwIn.StopConsuming()
		}()

		consumeErr := mwIn.StartConsuming(func(msg middleware.Message, ack func(), nack func()) {
			var batch protocol.Batch
			if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
				log.Printf("[filter] malformed message — discarding: %v", err)
				nack()
				return
			}

			if batch.Type != protocol.BatchTypeTransactions {
				ack()
				return
			}

			out := applyFilter(batch, fn)

			// Solo publicamos si el batch tiene transacciones tras el filtro.
			if len(out.Transactions) > 0 {
				if sendErr := sendBatch(mwOut, out); sendErr != nil {
					log.Printf("[filter] error sending batch: %v", sendErr)
					nack()
					return
				}
			}

			ack()
		})

		if consumeErr != nil && consumeErr != middleware.ErrMessageMiddlewareDisconnected {
			log.Printf("[filter] data consumer error: %v", consumeErr)
		}
	}()

	wg.Wait()
	log.Println("[filter] done")
}

func sendBatch(mw middleware.Middleware, batch protocol.Batch) error {
	data, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return mw.Send(middleware.Message{Body: string(data)})
}
