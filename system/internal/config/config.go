package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
)

// MustEnv retorna el valor de la variable de entorno key.
// Termina el proceso con error si la variable no está definida o está vacía.
func MustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[config] env var %s is required", key)
	}
	return v
}

// MustEnvInt retorna el valor entero de la variable de entorno key.
// Termina el proceso con error si la variable no es un entero válido.
func MustEnvInt(key string) int {
	v := MustEnv(key)
	value, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("[config] env var %s must be a number: %v", key, err)
	}
	return value
}

// ConnSettings lee RABBITMQ_HOST y RABBITMQ_PORT del entorno
// y retorna el struct de conexión listo para usar.
func ConnSettings() middleware.ConnSettings {
	port, err := strconv.Atoi(MustEnv("RABBITMQ_PORT"))
	if err != nil {
		log.Fatalf("[config] RABBITMQ_PORT must be a number: %v", err)
	}
	return middleware.ConnSettings{
		Hostname: MustEnv("RABBITMQ_HOST"),
		Port:     port,
	}
}

// UpstreamCount lee UPSTREAM_INSTANCES del entorno.
// Termina el proceso si el valor no es un entero positivo.
func UpstreamCount() int {
	n, err := strconv.Atoi(MustEnv("UPSTREAM_INSTANCES"))
	if err != nil || n < 1 {
		log.Fatalf("[config] UPSTREAM_INSTANCES must be a positive integer: %v", err)
	}
	return n
}

// Queue retorna un QueueMiddleware usando el nombre leído de la env var key.
func Queue(key string, conn middleware.ConnSettings) middleware.Middleware {
	mw, err := middleware.NewQueueMiddleware(MustEnv(key), conn)
	if err != nil {
		log.Fatalf("[config] queue %s (%s): %v", key, MustEnv(key), err)
	}
	return mw
}

// Exchange retorna un ExchangeMiddleware usando nombre y keys leídos del entorno.
//   - nameKey:  env var con el nombre del exchange
//   - routingKeys: keys de binding fijas (vacío para direct con keys dinámicas,
//     []string{""} para fanout)
func Exchange(nameKey string, routingKeys []string, conn middleware.ConnSettings) middleware.Middleware {
	name := MustEnv(nameKey)
	mw, err := middleware.NewExchangeMiddleware(name, routingKeys, conn)
	if err != nil {
		log.Fatalf("[config] exchange %s (%s): %v", nameKey, name, err)
	}
	return mw
}

// ExchangeWithKey retorna un ExchangeMiddleware cuya única routing key
// también se lee del entorno. Útil para exchanges de EOF con key fija.
func ExchangeWithKey(nameKey string, routingKeyEnv string, conn middleware.ConnSettings) middleware.Middleware {
	return Exchange(nameKey, []string{MustEnv(routingKeyEnv)}, conn)
}

// ExchangeWithKeyList retorna un ExchangeMiddleware con múltiples routing keys
// leídas de una env var con valores separados por coma.
// Útil para exchanges donde el producer debe fanout a múltiples routing keys.
func ExchangeWithKeyList(nameKey string, keysEnv string, conn middleware.ConnSettings) middleware.Middleware {
	keys := strings.Split(MustEnv(keysEnv), ",")
	return Exchange(nameKey, keys, conn)
}

// SharedQueue declara una named queue durable bindeada a un exchange con las
// routing keys provistas. N consumers que llamen a esta función con el mismo
// queueName quedan compitiendo en la misma queue (load balancing).
//
//   - nameKey:     env var con el nombre de la queue compartida
//   - exchangeKey: env var con el nombre del exchange al que se bindea
//   - routingKeys: routing keys del binding (usar [""] para exchanges
//     que se publican con key vacía, e.g. fanout-style)
func SharedQueue(nameKey, exchangeKey string, routingKeys []string, conn middleware.ConnSettings) middleware.Middleware {
	name := MustEnv(nameKey)
	exchange := MustEnv(exchangeKey)
	mw, err := middleware.NewSharedQueueMiddleware(name, exchange, routingKeys, conn)
	if err != nil {
		log.Fatalf("[config] shared queue %s on exchange %s: %v", name, exchange, err)
	}
	return mw
}

// SharedQueueWithKey es como SharedQueue pero lee la routing key de una env var.
func SharedQueueWithKey(nameKey, exchangeKey, routingKeyEnv string, conn middleware.ConnSettings) middleware.Middleware {
	return SharedQueue(nameKey, exchangeKey, []string{MustEnv(routingKeyEnv)}, conn)
}
