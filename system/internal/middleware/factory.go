package middleware

func CreateQueueMiddleware(queueName string, connectionSettings ConnSettings) (Middleware, error) {
	return NewQueueMiddleware(queueName, connectionSettings)
}

func CreateExchangeMiddleware(exchange string, keys []string, connectionSettings ConnSettings) (Middleware, error) {
	return NewExchangeMiddleware(exchange, keys, connectionSettings)
}

// CreateExchangePublisherMiddleware creates an exchange middleware that only publishes.
func CreateExchangePublisherMiddleware(exchange string, keys []string, connectionSettings ConnSettings) (Middleware, error) {
	return NewExchangePublisherMiddleware(exchange, keys, connectionSettings)
}
