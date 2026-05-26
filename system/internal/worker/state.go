package worker

type taskState[K comparable, S any] struct {
	State map[K]S
}

func newTaskState[K comparable, S any]() *taskState[K, S] {
	return &taskState[K, S]{
		State: map[K]S{},
	}
}
