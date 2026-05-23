package config

import (
	"log"
	"os"
	"strconv"

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
