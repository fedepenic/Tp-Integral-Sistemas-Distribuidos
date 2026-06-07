package middleware

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

var _ Middleware = (*QueueMiddleware)(nil)

type QueueMiddleware struct {
	conn      *amqp.Connection
	ch        *amqp.Channel
	queueName string
	stopCh    chan struct{}
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

func (qm *QueueMiddleware) StartConsuming(callbackFunc func(msg Message, ack func(), nack func())) error {
	if err := qm.ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
	}

	deliveries, err := qm.ch.Consume(
		qm.queueName,
		"",
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
