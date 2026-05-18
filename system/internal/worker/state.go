package worker

type taskState[K comparable, S any] struct {
	State           map[K]S
	PendingMessages int
	ExpectedEOFs    int
	ReceivedEOFs    int
	FlushDone       bool
	Flushing        bool
	LastControlSeq  map[int]int
	NextControlSeq  int
}

func newTaskState[K comparable, S any](expectedEOFs int) *taskState[K, S] {
	return &taskState[K, S]{
		State:          map[K]S{},
		ExpectedEOFs:   expectedEOFs,
		LastControlSeq: map[int]int{},
	}
}

func (t *taskState[K, S]) canFlush() bool {
	return t.ReceivedEOFs == t.ExpectedEOFs && t.PendingMessages == 0 && !t.FlushDone && !t.Flushing
}
