package worker

import (
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

type AggregatorConfig struct {
	InstanceID        int
	ConnSettings      middleware.ConnSettings
	InputExchange     string
	InputKey          string
	OutputQueue       string
	ControlExchange   string
	ControlKey        string
	UpstreamInstances int
}

type BatchExtractor[T any] func(batch protocol.Batch) ([]T, bool)

type AggregatorLogic[T any, K comparable, S any, O any] interface {
	Key(item T) K
	Zero() S
	Accumulate(state S, item T) S
	Finalize(key K, state S) []O
}
