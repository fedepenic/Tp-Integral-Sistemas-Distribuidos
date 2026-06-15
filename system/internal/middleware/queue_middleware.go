package middleware

import (
	"context"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

var _ Middleware = (*QueueMiddleware)(nil)

type QueueMiddleware struct {
	conn      *amqp.Connection
	ch        *amqp.Channel
	queueName string
	stopCh    chan struct{}
	// sendMu serializes publishes. An amqp Channel is not safe for concurrent
	// publishing: a single publish spans several frames (method, header, body)
	// and the library locks the connection per-frame, so concurrent senders can
	// interleave frames and trip a 505 UNEXPECTED_FRAME that closes the
	// connection. Nodes publish from two goroutines (data + EOF), so we guard it.
	sendMu sync.Mutex
}

func NewQueueMiddleware(queueName string, connectionSettings ConnSettings) (Middleware, error) {
	connStr := fmt.Sprintf("amqp://guest:guest@%s:%d/", connectionSettings.Hostname, connectionSettings.Port)
	conn, err := amqp.Dial(connStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMessageMiddlewareDisconnected, err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrMessageMiddlewareDisconnected, err)
	}

	_, err = ch.QueueDeclare(
		queueName,
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
	}

	return &QueueMiddleware{
		conn:      conn,
		ch:        ch,
		queueName: queueName,
		stopCh:    make(chan struct{}),
	}, nil
}

// NewSharedQueueMiddleware declares a named, durable, non-exclusive queue
// AND binds it to an exchange with the given routing keys. All N consumers
// that call this constructor with the same queue name end up consuming from
// the same queue, so RabbitMQ distributes messages round-robin (competing
// consumers / load balancing).
//
// Use this instead of NewExchangeMiddleware when you want to scale a stage
// horizontally with work distribution — NewExchangeMiddleware creates an
// auto-generated EXCLUSIVE queue per instance, which gives fanout-style
// duplicate delivery instead of load balancing.
func NewSharedQueueMiddleware(queueName, exchange string, keys []string, connectionSettings ConnSettings) (Middleware, error) {
	return NewSharedQueueMultiExchangeMiddleware(queueName, []string{exchange}, keys, connectionSettings)
}

// NewSharedQueueMultiExchangeMiddleware declares a named, durable,
// non-exclusive queue and binds it to multiple direct exchanges with the same
// routing keys. This is useful for joiners that consume a multiplexed stream
// from two upstream branches while preserving a single FIFO input per
// partition.
func NewSharedQueueMultiExchangeMiddleware(queueName string, exchanges []string, keys []string, connectionSettings ConnSettings) (Middleware, error) {
	connStr := fmt.Sprintf("amqp://guest:guest@%s:%d/", connectionSettings.Hostname, connectionSettings.Port)
	conn, err := amqp.Dial(connStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMessageMiddlewareDisconnected, err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrMessageMiddlewareDisconnected, err)
	}

	for _, exchange := range exchanges {
		if err := ch.ExchangeDeclare(exchange, "direct", true, false, false, false, nil); err != nil {
			ch.Close()
			conn.Close()
			return nil, fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
		}
	}

	q, err := ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
	}

	for _, exchange := range exchanges {
		for _, key := range keys {
			if err := ch.QueueBind(q.Name, key, exchange, false, nil); err != nil {
				ch.Close()
				conn.Close()
				return nil, fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
			}
		}
	}

	return &QueueMiddleware{
		conn:      conn,
		ch:        ch,
		queueName: q.Name,
		stopCh:    make(chan struct{}),
	}, nil
}

// DeclareBoundQueue declares a durable, non-exclusive, non-auto-delete queue
// bound to a direct exchange with the given key, then releases the connection.
// Producers call this before publishing so the destination queue exists and is
// bound even if the consumer has not started yet — otherwise messages routed to
// an unbound key on a direct exchange are silently dropped as unroutable. The
// queue survives the connection close because it is durable and not auto-delete,
// so the consumer later attaches to the same queue and drains what buffered.
func DeclareBoundQueue(queueName, exchange, key string, connectionSettings ConnSettings) error {
	mw, err := NewSharedQueueMiddleware(queueName, exchange, []string{key}, connectionSettings)
	if err != nil {
		return err
	}
	return mw.Close()
}

func (qm *QueueMiddleware) StartConsuming(callbackFunc func(msg Message, ack func(), nack func())) error {
	if err := qm.ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
	}

	deliveries, err := qm.ch.Consume(
		qm.queueName,
		qm.queueName,
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMessageMiddlewareDisconnected, err)
	}

	for {
		select {
		case <-qm.stopCh:
			return nil
		case delivery, ok := <-deliveries:
			if !ok {
				return ErrMessageMiddlewareDisconnected
			}
			msg := Message{Body: string(delivery.Body)}
			ack := func() { delivery.Ack(false) }
			nack := func() { delivery.Nack(false, true) }
			callbackFunc(msg, ack, nack)
		}
	}
}

func (qm *QueueMiddleware) StopConsuming() error {
	select {
	case <-qm.stopCh:
	default:
		close(qm.stopCh)
	}
	return nil
}

func (qm *QueueMiddleware) Send(msg Message) error {
	return qm.publish(msg)
}

// SendWithKey ignora la key — una queue simple siempre publica a su nombre fijo.
func (qm *QueueMiddleware) SendWithKey(msg Message, _ string) error {
	return qm.publish(msg)
}

func (qm *QueueMiddleware) publish(msg Message) error {
	qm.sendMu.Lock()
	defer qm.sendMu.Unlock()
	if err := qm.ch.PublishWithContext(
		context.Background(),
		"",
		qm.queueName,
		false,
		false,
		amqp.Publishing{
			ContentType:  "text/plain",
			DeliveryMode: amqp.Persistent,
			Body:         []byte(msg.Body),
		},
	); err != nil {
		return fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
	}
	return nil
}

func (qm *QueueMiddleware) Close() error {
	if err := qm.ch.Close(); err != nil {
		return ErrMessageMiddlewareClose
	}
	if err := qm.conn.Close(); err != nil {
		return ErrMessageMiddlewareClose
	}
	return nil
}
