package node

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

type eofPersistedState struct {
	SeenEOFs     map[string]struct{} `json:"seen_eofs"`
	EOFCounts    map[string]int      `json:"eof_counts"`
	EOFForwarded map[string]struct{} `json:"eof_forwarded"`
	EOFCompleted map[string]struct{} `json:"eof_completed"`
	ECount       map[string]int      `json:"e_count"`
	ELCount      map[string]int      `json:"el_count"`
	ERCount      map[string]int      `json:"er_count"`
}

func newPersistedState() *eofPersistedState {
	return &eofPersistedState{
		SeenEOFs:     make(map[string]struct{}),
		EOFCounts:    make(map[string]int),
		EOFForwarded: make(map[string]struct{}),
		EOFCompleted: make(map[string]struct{}),
		ECount:       make(map[string]int),
		ELCount:      make(map[string]int),
		ERCount:      make(map[string]int),
	}
}

type eofPersister struct {
	mu   sync.Mutex
	path string
	*eofPersistedState
}

func newEOFPersister(path string) *eofPersister {
	p := &eofPersister{
		path:              path,
		eofPersistedState: newPersistedState(),
	}
	p.load()
	return p
}

func (p *eofPersister) load() {
	data, err := os.ReadFile(p.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[eofstate] error reading %s: %v", p.path, err)
		}
		return
	}
	var s eofPersistedState
	if err := json.Unmarshal(data, &s); err != nil {
		log.Printf("[eofstate] error decoding %s: %v", p.path, err)
		return
	}
	if s.SeenEOFs == nil {
		s.SeenEOFs = make(map[string]struct{})
	}
	if s.EOFCounts == nil {
		s.EOFCounts = make(map[string]int)
	}
	if s.EOFForwarded == nil {
		s.EOFForwarded = make(map[string]struct{})
	}
	if s.EOFCompleted == nil {
		s.EOFCompleted = make(map[string]struct{})
	}
	if s.ECount == nil {
		s.ECount = make(map[string]int)
	}
	if s.ELCount == nil {
		s.ELCount = make(map[string]int)
	}
	if s.ERCount == nil {
		s.ERCount = make(map[string]int)
	}
	p.eofPersistedState = &s
	log.Printf("[eofstate] loaded state from %s (seen=%d, counts=%d, forwarded=%d, completed=%d)",
		p.path, len(s.SeenEOFs), len(s.EOFCounts), len(s.EOFForwarded), len(s.EOFCompleted))
}

func (p *eofPersister) persist() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		log.Printf("[eofstate] mkdir %s: %v", filepath.Dir(p.path), err)
		return
	}

	tmpPath := p.path + ".tmp"
	data, err := json.Marshal(p.eofPersistedState)
	if err != nil {
		log.Printf("[eofstate] marshal: %v", err)
		return
	}

	f, err := os.Create(tmpPath)
	if err != nil {
		log.Printf("[eofstate] create tmp %s: %v", tmpPath, err)
		return
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		log.Printf("[eofstate] write tmp %s: %v", tmpPath, err)
		return
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		log.Printf("[eofstate] sync tmp %s: %v", tmpPath, err)
		return
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		log.Printf("[eofstate] close tmp %s: %v", tmpPath, err)
		return
	}
	if err := os.Rename(tmpPath, p.path); err != nil {
		log.Printf("[eofstate] rename %s -> %s: %v", tmpPath, p.path, err)
		return
	}
}
