package middleware

func CreateQueueMiddleware(queueName string, connectionSettings ConnSettings) (Middleware, error) {
	return NewQueueMiddleware(queueName, connectionSettings)
}

func CreateExchangeMiddleware(exchange string, keys []string, connectionSettings ConnSettings) (Middleware, error) {
	return NewExchangeMiddleware(exchange, keys, connectionSettings)
}

// CreateDurableExchangeMiddleware creates an exchange middleware with a durable,
// non-exclusive, non-auto-delete queue. Messages survive consumer crashes.
func CreateDurableExchangeMiddleware(exchange string, keys []string, connectionSettings ConnSettings) (Middleware, error) {
	return NewDurableExchangeMiddleware(exchange, keys, connectionSettings)
}

// CreateExchangePublisherMiddleware creates an exchange middleware that only publishes.
func CreateExchangePublisherMiddleware(exchange string, keys []string, connectionSettings ConnSettings) (Middleware, error) {
	return NewExchangePublisherMiddleware(exchange, keys, connectionSettings)
}
