package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type eofStore struct {
	mu   sync.Mutex
	path string
	seen map[string]struct{}
}

func newEOFStore(path string) (*eofStore, error) {
	store := &eofStore{
		path: path,
		seen: make(map[string]struct{}),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *eofStore) load() error {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open EOF store %s: %w", s.path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		id := strings.TrimSpace(scanner.Text())
		if id != "" {
			s.seen[id] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read EOF store %s: %w", s.path, err)
	}
	return nil
}

func (s *eofStore) withUnseen(id string, fn func() error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.seen[id]; ok {
		return false, nil
	}

	if err := fn(); err != nil {
		return false, err
	}
	if err := s.append(id); err != nil {
		return false, err
	}
	s.seen[id] = struct{}{}
	return true, nil
}

func (s *eofStore) append(id string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create EOF store dir: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open EOF store for append %s: %w", s.path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(id + "\n"); err != nil {
		return fmt.Errorf("write EOF store %s: %w", s.path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync EOF store %s: %w", s.path, err)
	}
	return nil
}
