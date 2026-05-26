package filterworker

import (
	"encoding/json"
	"fmt"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

// KeyFunc extrae la routing key de una transacción.
// Si una salida no necesita routing key (queue simple o fanout), usar nil.
type KeyFunc func(t protocol.Transaction) string

// Output representa un destino de publicación del filter.
//
//   - GetKey == nil → todas las transacciones van en un único batch,
//     publicado con Send (queue simple o fanout sin routing key).
//   - GetKey != nil → las transacciones se agrupan por el valor
//     retornado y se publica un batch por grupo con SendWithKey.
//   - EOFMiddleware es el middleware al que se publica el EOF de este output.
//     Puede ser distinto al Middleware de datos (exchange de EOF separado).
type Output struct {
	Middleware    middleware.Middleware
	GetKey        KeyFunc
	EOFMiddleware middleware.Middleware
}

// publish toma las transacciones ya filtradas y las publica al middleware
// de este Output, agrupando por routing key si corresponde.
func (o *Output) publish(clientID string, txns []protocol.Transaction) error {
	if o.GetKey == nil {
		// Sin routing key: un único batch con todas las transacciones.
		return o.sendBatch(clientID, txns, "")
	}

	// Con routing key dinámica: agrupar por valor de key.
	groups := make(map[string][]protocol.Transaction)
	for _, tx := range txns {
		key := o.GetKey(tx)
		groups[key] = append(groups[key], tx)
	}

	for key, group := range groups {
		if err := o.sendBatch(clientID, group, key); err != nil {
			return err
		}
	}
	return nil
}

// sendBatch serializa el batch y lo envía.
// Si key == "" usa Send (queue o fanout), si key != "" usa SendWithKey (direct exchange).
func (o *Output) sendBatch(clientID string, txns []protocol.Transaction, key string) error {
	batch := protocol.Batch{
		Type:         protocol.BatchTypeTransactions,
		ClientID:     clientID,
		Transactions: txns,
	}
	data, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal batch: %w", err)
	}
	msg := middleware.Message{Body: string(data)}

	if key == "" {
		return o.Middleware.Send(msg)
	}
	return o.Middleware.SendWithKey(msg, key)
}

// sendEOF publica un batch de tipo EOF usando EOFMiddleware.
// Los EOFs no tienen routing key — siempre usan Send.
// Si EOFMiddleware es nil, no se envía nada (output sin EOF, e.g. la salida
// direct hacia un pipeline aún no implementado).
func (o *Output) sendEOF(clientID string) error {
	if o.EOFMiddleware == nil {
		return nil
	}
	batch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
	}
	data, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal eof: %w", err)
	}
	return o.EOFMiddleware.Send(middleware.Message{Body: string(data)})
}
