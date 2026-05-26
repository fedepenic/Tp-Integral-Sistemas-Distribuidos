package filterworker

import (
	"encoding/json"
	"fmt"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/worker"
)

// KeyFunc extrae la routing key de una transacción.
// Si una salida no necesita routing key (queue simple o fanout), usar nil.
type KeyFunc func(t protocol.Transaction) string

// Output representa un destino de publicación del filter.
//
//   - GetBusinessKey == nil → todas las transacciones van en un único batch,
//     publicado con Send (queue simple o fanout sin routing key).
//   - GetBusinessKey != nil → las transacciones se agrupan por partición
//     y se publica un batch por partición con SendWithKey.
//   - EOFMiddleware es el middleware al que se publica el EOF de este output.
//     Puede ser distinto al Middleware de datos (exchange de EOF separado).
type Output struct {
	Middleware middleware.Middleware

	// Clave lógica para particionar (por ejemplo, account/bank/etc.).
	GetBusinessKey KeyFunc

	// Routing key física: <RoutingPrefix>_<partition>.
	RoutingPrefix string
	Partitions    int

	EOFMiddleware middleware.Middleware
}

// publish toma las transacciones ya filtradas y las publica al middleware
// de este Output, agrupando por routing key si corresponde.
func (o *Output) publish(clientID string, txns []protocol.Transaction) error {
	if err := o.validatePartitioning(); err != nil {
		return err
	}
	if o.GetBusinessKey == nil {
		// Sin routing key: un único batch con todas las transacciones.
		return o.sendBatch(clientID, txns, "")
	}

	// Con routing key física: agrupar por partición.
	groups := make(map[string][]protocol.Transaction)
	for _, tx := range txns {
		businessKey := o.GetBusinessKey(tx)
		partition := worker.PartitionForKey(businessKey, o.Partitions)
		routingKey := worker.RoutingKey(o.RoutingPrefix, partition)
		groups[routingKey] = append(groups[routingKey], tx)
	}

	for key, group := range groups {
		if err := o.sendBatch(clientID, group, key); err != nil {
			return err
		}
	}
	return nil
}

func (o *Output) validatePartitioning() error {
	if o.GetBusinessKey == nil {
		if o.Partitions != 0 || o.RoutingPrefix != "" {
			return fmt.Errorf("partitioning requires GetBusinessKey")
		}
		return nil
	}
	if o.Partitions < 1 {
		return fmt.Errorf("partitions must be >= 1")
	}
	if o.RoutingPrefix == "" {
		return fmt.Errorf("routing prefix is required")
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
// Si hay particionado, envía un EOF por partición usando SendWithKey.
// Si EOFMiddleware es nil, no se envía nada (output sin EOF, e.g. la salida
// direct hacia un pipeline aún no implementado).
func (o *Output) sendEOF(clientID string) error {
	if o.EOFMiddleware == nil {
		return nil
	}
	if err := o.validatePartitioning(); err != nil {
		return err
	}
	batch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
	}
	data, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal eof: %w", err)
	}
	msg := middleware.Message{Body: string(data)}
	if o.GetBusinessKey == nil {
		return o.EOFMiddleware.Send(msg)
	}
	for partition := 0; partition < o.Partitions; partition++ {
		routingKey := worker.RoutingKey(o.RoutingPrefix, partition)
		if err := o.EOFMiddleware.SendWithKey(msg, routingKey); err != nil {
			return err
		}
	}
	return nil
}
