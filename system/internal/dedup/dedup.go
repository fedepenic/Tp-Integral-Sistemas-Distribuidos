package dedup

import (
	"os"
	"strconv"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/node"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

const defaultMaxBatchIDs = 100000

type BatchDeduplicator struct {
	mu      sync.Mutex
	maxSize int
	seen    map[string]struct{}
	ordered []string
}

func New() *BatchDeduplicator {
	maxSize := defaultMaxBatchIDs
	if v := os.Getenv("MAX_BATCH_IDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxSize = n
		}
	}
	return &BatchDeduplicator{
		maxSize: maxSize,
		seen:    make(map[string]struct{}),
		ordered: make([]string, 0, maxSize),
	}
}

func (d *BatchDeduplicator) Seen(batchID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.seen[batchID]
	return ok
}

func (d *BatchDeduplicator) Mark(batchID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[batchID]; ok {
		return
	}
	d.seen[batchID] = struct{}{}
	d.ordered = append(d.ordered, batchID)
	if len(d.ordered) >= d.maxSize {
		d.evict()
	}
}

func (d *BatchDeduplicator) CheckAndMark(batchID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[batchID]; ok {
		return true
	}
	d.seen[batchID] = struct{}{}
	d.ordered = append(d.ordered, batchID)
	if len(d.ordered) >= d.maxSize {
		d.evict()
	}
	return false
}

func (d *BatchDeduplicator) evict() {
	evict := d.maxSize / 10
	if evict < 1 {
		evict = 1
	}
	for _, id := range d.ordered[:evict] {
		delete(d.seen, id)
	}
	d.ordered = d.ordered[evict:]
}

func Wrap(fn node.ProcessFunc, d *BatchDeduplicator) node.ProcessFunc {
	return func(batch protocol.Batch) (protocol.Batch, bool) {
		if batch.BatchID == "" {
			return fn(batch)
		}
		if d.CheckAndMark(batch.BatchID) {
			return protocol.Batch{}, false
		}
		return fn(batch)
	}
}
