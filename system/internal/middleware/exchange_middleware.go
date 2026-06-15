package middleware

import (
	"context"
	"fmt"
	"strings"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

var _ Middleware = (*ExchangeMiddleware)(nil)

type ExchangeMiddleware struct {
	conn      *amqp.Connection
	ch        *amqp.Channel
	exchange  string
	keys      []string
	queueName string
	stopCh    chan struct{}
	// sendMu serializes publishes. An amqp Channel is not safe for concurrent
	// publishing: a single publish spans several frames (method, header, body)
	// and the library locks the connection per-frame, so concurrent senders can
	// interleave frames and trip a 505 UNEXPECTED_FRAME that closes the
	// connection. Nodes publish from two goroutines (data + EOF), so we guard it.
	sendMu sync.Mutex
}

// exclusiveQueueName builds a human-readable name for the exclusive,
// auto-delete queue that each ExchangeMiddleware consumer gets. Format:
// "{exchange}.{keys}" — identifiable in the RabbitMQ UI and unique for the
// current topology, where each exchange consumer owns a distinct routing key.
func exclusiveQueueName(exchange string, keys []string) string {
	nonEmpty := make([]string, 0, len(keys))
	for _, k := range keys {
		if k != "" {
			nonEmpty = append(nonEmpty, k)
		}
	}
	if len(nonEmpty) == 0 {
		return fmt.Sprintf("%s.all", exchange)
	}
	return fmt.Sprintf("%s.%s", exchange, strings.Join(nonEmpty, "+"))
}

func NewExchangeMiddleware(exchange string, keys []string, connectionSettings ConnSettings) (Middleware, error) {
	return newExchangeMiddleware(exchange, keys, connectionSettings, true)
}

// NewExchangePublisherMiddleware declares a direct exchange for publishing only.
// It does not declare or bind a queue because publishers do not consume from one.
func NewExchangePublisherMiddleware(exchange string, keys []string, connectionSettings ConnSettings) (Middleware, error) {
	return newExchangeMiddleware(exchange, keys, connectionSettings, false)
}

func newExchangeMiddleware(exchange string, keys []string, connectionSettings ConnSettings, declareQueue bool) (Middleware, error) {
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

	if err := ch.ExchangeDeclare(
		exchange,
		"direct",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
	}

	queueName := ""
	if declareQueue {
		q, err := ch.QueueDeclare(
			exclusiveQueueName(exchange, keys),
			false,
			true,
			true,
			false,
			nil,
		)
		if err != nil {
			ch.Close()
			conn.Close()
			return nil, fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
		}

		queueName = q.Name
		for _, key := range keys {
			if err := ch.QueueBind(queueName, key, exchange, false, nil); err != nil {
				ch.Close()
				conn.Close()
				return nil, fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
			}
		}
	}

	return &ExchangeMiddleware{
		conn:      conn,
		ch:        ch,
		exchange:  exchange,
		keys:      keys,
		queueName: queueName,
		stopCh:    make(chan struct{}),
	}, nil
}

func (em *ExchangeMiddleware) StartConsuming(callbackFunc func(msg Message, ack func(), nack func())) error {
	if em.queueName == "" {
		return ErrMessageMiddlewareMessage
	}

	if err := em.ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
	}

	deliveries, err := em.ch.Consume(
		em.queueName,
		em.queueName,
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
		case <-em.stopCh:
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

func (em *ExchangeMiddleware) StopConsuming() error {
	select {
	case <-em.stopCh:
	default:
		close(em.stopCh)
	}
	return nil
}

// Send publica el mensaje a todas las keys configuradas en el constructor.
// Útil para fanout o cuando la routing key es fija y conocida de antemano.
func (em *ExchangeMiddleware) Send(msg Message) error {
	if len(em.keys) == 0 {
		return ErrMessageMiddlewareMessage
	}
	for _, key := range em.keys {
		if err := em.publish(msg, key); err != nil {
			return err
		}
	}
	return nil
}

// SendWithKey publica el mensaje usando la routing key provista,
// ignorando las keys configuradas en el constructor.
// Útil para direct exchanges donde la key se determina en ejecución.
func (em *ExchangeMiddleware) SendWithKey(msg Message, key string) error {
	return em.publish(msg, key)
}

// publish es el métod0 interno que realiza el publish a RabbitMQ.
func (em *ExchangeMiddleware) publish(msg Message, key string) error {
	em.sendMu.Lock()
	defer em.sendMu.Unlock()
	if err := em.ch.PublishWithContext(
		context.Background(),
		em.exchange,
		key,
		false,
		false,
		amqp.Publishing{
			ContentType: "text/plain",
			Body:        []byte(msg.Body),
		},
	); err != nil {
		return fmt.Errorf("%w: %v", ErrMessageMiddlewareMessage, err)
	}
	return nil
}

func (em *ExchangeMiddleware) Close() error {
	if err := em.ch.Close(); err != nil {
		return ErrMessageMiddlewareClose
	}
	if err := em.conn.Close(); err != nil {
		return ErrMessageMiddlewareClose
	}
	return nil
}
