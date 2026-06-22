package maxperbank

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultDir              = "/tmp/max_per_bank_state"
	defaultCheckpointEvery  = 100
	checkpointFileExtension = ".checkpoint.json"
	walFileExtension        = ".wal"
)

type State struct {
	BankID        string  `json:"bank_id"`
	BankName      string  `json:"bank_name"`
	SourceAccount string  `json:"source_account"`
	MaxAmountUSD  float64 `json:"max_amount_usd"`
	HasValue      bool    `json:"has_value"`
}

type Store struct {
	dir             string
	checkpointEvery int
	batchesSinceCP  map[string]int
}

type walEntry struct {
	Updates []State `json:"updates"`
}

func NewFromEnv() (*Store, error) {
	dir := envOrDefault("MAX_PER_BANK_STATE_DIR", defaultDir)
	checkpointEvery := defaultCheckpointEvery
	if raw := os.Getenv("MAX_PER_BANK_CHECKPOINT_EVERY"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return nil, fmt.Errorf("MAX_PER_BANK_CHECKPOINT_EVERY must be a positive integer")
		}
		checkpointEvery = value
	}
	return New(dir, checkpointEvery)
}

func New(dir string, checkpointEvery int) (*Store, error) {
	if checkpointEvery < 1 {
		return nil, fmt.Errorf("checkpointEvery must be positive")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create max_per_bank state dir: %w", err)
	}
	return &Store{
		dir:             dir,
		checkpointEvery: checkpointEvery,
		batchesSinceCP:  make(map[string]int),
	}, nil
}

func (s *Store) Recover() (map[string]map[string]State, error) {
	state := make(map[string]map[string]State)

	clients, err := s.clients()
	if err != nil {
		return nil, err
	}
	for _, clientID := range clients {
		clientState, err := s.recoverClient(clientID)
		if err != nil {
			return nil, err
		}
		state[clientID] = clientState
	}
	return state, nil
}

func (s *Store) AppendBatch(clientID string, updates []State) error {
	if err := s.appendWAL(clientID, walEntry{Updates: updates}); err != nil {
		return err
	}
	s.batchesSinceCP[clientID]++
	return nil
}

func (s *Store) MaybeCheckpoint(clientID string, state map[string]State) error {
	if s.batchesSinceCP[clientID] < s.checkpointEvery {
		return nil
	}
	if err := s.WriteCheckpoint(clientID, state); err != nil {
		return err
	}
	if err := s.truncateWAL(clientID); err != nil {
		return err
	}
	s.batchesSinceCP[clientID] = 0
	return nil
}

func (s *Store) RemoveClient(clientID string) error {
	var firstErr error
	for _, path := range []string{s.checkpointPath(clientID), s.walPath(clientID)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	if err := syncDir(s.dir); err != nil && firstErr == nil {
		firstErr = err
	}
	delete(s.batchesSinceCP, clientID)
	if firstErr != nil {
		return fmt.Errorf("remove persisted max_per_bank state for client %s: %w", clientID, firstErr)
	}
	return nil
}

func (s *Store) WriteCheckpoint(clientID string, state map[string]State) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create max_per_bank state dir: %w", err)
	}

	tmp, err := os.CreateTemp(s.dir, clientFilePrefix(clientID)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create checkpoint temp file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write checkpoint newline: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync checkpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close checkpoint: %w", err)
	}
	if err := os.Rename(tmpPath, s.checkpointPath(clientID)); err != nil {
		return fmt.Errorf("rename checkpoint: %w", err)
	}
	removeTmp = false
	if err := syncDir(s.dir); err != nil {
		return fmt.Errorf("sync checkpoint directory: %w", err)
	}
	return nil
}

func (s *Store) recoverClient(clientID string) (map[string]State, error) {
	state := make(map[string]State)

	checkpoint, err := os.Open(s.checkpointPath(clientID))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("open checkpoint for client %s: %w", clientID, err)
	}
	if err == nil {
		defer checkpoint.Close()
		if err := json.NewDecoder(checkpoint).Decode(&state); err != nil && err != io.EOF {
			return nil, fmt.Errorf("decode checkpoint for client %s: %w", clientID, err)
		}
	}

	wal, err := os.Open(s.walPath(clientID))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("open WAL for client %s: %w", clientID, err)
	}
	if err == nil {
		defer wal.Close()
		reader := bufio.NewReader(wal)
		line := 0
		for {
			raw, err := reader.ReadString('\n')
			if err != nil && err != io.EOF {
				return nil, fmt.Errorf("read WAL for client %s: %w", clientID, err)
			}
			if err == io.EOF && raw == "" {
				break
			}
			line++
			raw = strings.TrimSpace(raw)
			if raw != "" {
				var entry walEntry
				if err := json.Unmarshal([]byte(raw), &entry); err != nil {
					return nil, fmt.Errorf("decode WAL for client %s line %d: %w", clientID, line, err)
				}
				for _, update := range entry.Updates {
					if update.HasValue {
						state[update.BankID] = update
					}
				}
			}
			if err == io.EOF {
				break
			}
		}
	}

	return state, nil
}

func (s *Store) appendWAL(clientID string, entry walEntry) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create max_per_bank state dir: %w", err)
	}
	file, err := os.OpenFile(s.walPath(clientID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open WAL: %w", err)
	}
	defer file.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal WAL entry: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append WAL: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync WAL: %w", err)
	}
	return nil
}

func (s *Store) truncateWAL(clientID string) error {
	file, err := os.OpenFile(s.walPath(clientID), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("truncate WAL: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync truncated WAL: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close truncated WAL: %w", err)
	}
	return syncDir(s.dir)
}

func (s *Store) clients() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read max_per_bank state dir: %w", err)
	}

	seen := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, checkpointFileExtension):
			seen[strings.TrimSuffix(name, checkpointFileExtension)] = struct{}{}
		case strings.HasSuffix(name, walFileExtension):
			seen[strings.TrimSuffix(name, walFileExtension)] = struct{}{}
		}
	}

	clients := make([]string, 0, len(seen))
	for encoded := range seen {
		clientID, err := decodeClientID(encoded)
		if err != nil {
			return nil, err
		}
		clients = append(clients, clientID)
	}
	sort.Strings(clients)
	return clients, nil
}

func (s *Store) checkpointPath(clientID string) string {
	return filepath.Join(s.dir, clientFilePrefix(clientID)+checkpointFileExtension)
}

func (s *Store) walPath(clientID string) string {
	return filepath.Join(s.dir, clientFilePrefix(clientID)+walFileExtension)
}

func clientFilePrefix(clientID string) string {
	return strings.NewReplacer("%", "%25", "/", "%2F", "\\", "%5C", "\x00", "%00").Replace(clientID)
}

func decodeClientID(encoded string) (string, error) {
	replacer := strings.NewReplacer("%00", "\x00", "%5C", "\\", "%2F", "/", "%25", "%")
	return replacer.Replace(encoded), nil
}

func syncDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func envOrDefault(key, def string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return def
}
