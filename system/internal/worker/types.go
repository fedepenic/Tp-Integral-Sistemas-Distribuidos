package worker

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

type AggregatorConfig struct {
	// ID unico de esta instancia del aggregator
	InstanceID int

	// Configuracion de conexion a RabbitMQ.
	ConnSettings middleware.ConnSettings

	// Exchange desde donde este worker consume datos.
	//
	// Normalmente es un exchange DIRECT.
	//
	// Ejemplo:
	//   avg_payments_exchange
	InputExchange string

	// Routing key que consume ESTA instancia.
	//
	// si yo soy id 1 entonces escucho de
	// worker_1
	// que es donde esta enviando los datos el nodo enterior
	//
	// Cada instancia escucha UNA sola key.
	InputKey string

	// Cola de salida cuando NO se usa exchange.
	//
	// Caso simple:
	//   producer -> queue
	//
	// No suele usarse en joiners particionados.
	OutputQueue string

	// Exchange donde el aggregator publica resultados.
	//
	// Generalmente DIRECT.
	//
	// Ejemplo:
	//   payment_format
	OutputExchange string

	// Routing key fija de salida.
	//
	// SOLO se usa cuando NO hay particionado.
	//
	// Ejemplo:
	//   "results"
	//
	// Si hay particionado:
	//   NO se usa.
	//
	// Si no hay particionado entonce hay fanout o una outputQueue
	// por lo tanto esta key no se usa para nada
	OutputKey string

	// Prefijo usado para construir routing keys dinámicas.
	//
	// Ejemplo:
	//   OutputKeyPrefix = "joiner"
	//
	// genera:
	//   joiner_0
	//   joiner_1
	//   joiner_2
	//
	// Esto permite:
	//   MUCHAS KEYS
	//   POCAS COLAS
	//   UNA cola por instancia
	OutputKeyPrefix string

	// Cantidad de particiones/instancias destino.
	//
	// IMPORTANTE:
	//   NO es cantidad de keys lógicas.
	//
	// Es cantidad de COLAS reales.
	//
	// Ejemplo:
	//   OutputPartitions = 3
	//
	// Entonces existen:
	//   queue_joiner_0
	//   queue_joiner_1
	//   queue_joiner_2
	//
	// Todas las keys del negocio se hashean
	// hacia una de esas 3 particiones.
	//
	// Ejemplo:
	//   VISA   -> joiner_1
	//   CASH   -> joiner_0
	//   CRYPTO -> joiner_1
	//   DEBIT  -> joiner_2
	//
	// Así evitas:
	//   1 cola por PaymentFormat
	OutputPartitions int

	// Exchange FANOUT interno usado para coordinar EOFs
	// entre instancias del aggregator.
	//
	// NO transporta datos.
	//
	// SOLO señales de control:
	//   "yo ya terminé"
	//
	// Ejemplo:
	//   eof_internal_exchange
	ControlExchange string

	// Routing key del exchange de control.
	//
	// En FANOUT Rabbit ignora la routing key,
	// pero middleware la exige.
	//
	// Por eso existe aunque conceptualmente no haga falta.
	//
	// Se suele poner:
	//   ""
	// o
	//   "control"
	//
	// lo mismo q output key si no hay direct entonces no se usa esta key
	ControlKey string

	// Cantidad de instancias atras arriba.
	//
	// Se usa para saber:
	//   cuantos EOF necesito recibir
	//   antes de hacer flush.
	//
	// Ejemplo:
	//   hay 3 filters upstream
	//
	// entonces:
	//   UpstreamInstances = 3
	//
	// El aggregator recien flushea cuando recibio:
	//   3 EOFs
	UpstreamInstances int

	// DataType identifica el contenido de Records en el batch de salida.
	DataType string
}

type BatchExtractor[T any] func(batch protocol.Batch) ([]T, bool)

type ResultKeyFunc[O any] func(result O) string

type AggregatorLogic[T any, K comparable, S any, O any] interface {
	Key(item T) K
	Zero() S
	Accumulate(state S, item T) S
	Finalize(key K, state S) []O
}
