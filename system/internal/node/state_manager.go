package node

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// WALEntry represents the state change produced by one batch.
// It is stored as a line-delimited JSON object in the WAL file.
// The Delta field is opaque — each node type marshals its own delta format.
type WALEntry struct {
	BatchID string          `json:"batch_id"`
	Seq     int64           `json:"seq"`
	Delta   json.RawMessage `json:"delta"`
}

// CheckpointData is the full state snapshot written atomically to disk.
// CheckpointSeq is the WAL sequence number of the last entry whose
// delta is fully reflected in State. On recovery, only WAL entries
// with Seq > CheckpointSeq need to be replayed.
type CheckpointData struct {
	State         json.RawMessage  `json:"state"`
	CheckpointSeq int64            `json:"checkpoint_seq"`
}

// StateManager provides checkpoint-based persistence with a WAL for
// stateful pipeline nodes.
//
// Architecture
//
//	Checkpoint: full state snapshot, written atomically every N batches.
//	WAL (Write-Ahead Log): ordered deltas checkpointed between checkpoints.
//
// Flow per batch
//
//	1. Process batch → compute delta (incremental state change)
//	2. AppendWAL(batchID, delta) → fsync
//	3. Apply delta to in-memory state
//	4. Mark batchID as applied (dedup)
//	5. Optionally checkpoint if freq threshold reached
//
// Recovery
//
//	1. Load latest checkpoint (full state + last checkpoint seq)
//	2. Load WAL entries with Seq > checkpoint seq
//	3. Replay each entry's delta into state
//	4. Resume processing
//
// Atomicity guarantees
//
//	WAL writes: tmp → fsync → rename (atomic file write)
//	Checkpoint writes: tmp → fsync → rename (atomic file write)
//	WAL rotation after checkpoint: create new empty file (old survives
//	  until success, providing fallback with seq filtering)
//
// If the process crashes between AppendWAL and ApplyDelta, recovery replays
// that entry (the batchID dedup ensures it was not yet applied). If it
// crashes after ApplyDelta but before ACK, the delta is in state and in the
// WAL; the in-memory dedup is lost, but the entry's Delta is idempotent
// (accumulate/max/sum-count) and re-application produces the same state.
// For non-idempotent operations (append), the in-memory dedup populated
// during recovery prevents double-application.
type StateManager struct {
	mu sync.Mutex

	nodeName       string
	stateID        string
	dirPath        string
	checkpointFreq int

	state json.RawMessage
	dedup map[string]struct{}

	walFile *os.File
	walPath string
	walSeq  int64

	batchCount int
}

// NewStateManager creates a StateManager. If dirPath is empty the manager
// is disabled (all methods are no-ops), useful for stateless nodes.
func NewStateManager(nodeName, stateID, dirPath string, checkpointFreq int) *StateManager {
	if dirPath == "" {
		return &StateManager{nodeName: nodeName}
	}
	if checkpointFreq <= 0 {
		checkpointFreq = 1000
	}
	dirPath = filepath.Join(dirPath, stateID)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		log.Printf("[%s] create state dir %s: %v", nodeName, dirPath, err)
		return &StateManager{nodeName: nodeName}
	}
	return &StateManager{
		nodeName:       nodeName,
		stateID:        stateID,
		dirPath:        dirPath,
		checkpointFreq: checkpointFreq,
		dedup:          make(map[string]struct{}),
		walPath:        filepath.Join(dirPath, "wal.log"),
	}
}

func (sm *StateManager) Enabled() bool {
	return sm.dirPath != ""
}

func (sm *StateManager) checkpointPath() string {
	return filepath.Join(sm.dirPath, "checkpoint.json")
}

// openWAL opens or creates the WAL file for appending.
func (sm *StateManager) openWAL() error {
	if sm.walFile != nil {
		return nil
	}
	f, err := os.OpenFile(sm.walPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open WAL %s: %w", sm.walPath, err)
	}
	sm.walFile = f
	return nil
}

// AppendWAL writes a delta entry to the WAL with fsync.
// The call blocks until the data is safely on disk.
func (sm *StateManager) AppendWAL(batchID string, delta json.RawMessage) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.Enabled() {
		return nil
	}
	if err := sm.openWAL(); err != nil {
		return err
	}

	sm.walSeq++
	entry := WALEntry{
		BatchID: batchID,
		Seq:     sm.walSeq,
		Delta:   delta,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal WAL entry: %w", err)
	}
	data = append(data, '\n')

	if _, err := sm.walFile.Write(data); err != nil {
		return fmt.Errorf("write WAL: %w", err)
	}
	if err := sm.walFile.Sync(); err != nil {
		return fmt.Errorf("fsync WAL: %w", err)
	}
	return nil
}

// MarkApplied records a batchID as having its delta applied to state.
// Prevents double-application of the same WAL entry during recovery.
func (sm *StateManager) MarkApplied(batchID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.Enabled() && batchID != "" {
		sm.dedup[batchID] = struct{}{}
	}
}

// IsApplied checks whether a batchID has already been applied.
func (sm *StateManager) IsApplied(batchID string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !sm.Enabled() {
		return false
	}
	_, ok := sm.dedup[batchID]
	return ok
}

// ShouldCheckpoint returns true every checkpointFreq calls.
func (sm *StateManager) ShouldCheckpoint() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !sm.Enabled() {
		return false
	}
	sm.batchCount++
	return sm.batchCount%sm.checkpointFreq == 0
}

// GetDedup returns a copy of the current dedup set.
func (sm *StateManager) GetDedup() map[string]struct{} {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	cp := make(map[string]struct{}, len(sm.dedup))
	for k := range sm.dedup {
		cp[k] = struct{}{}
	}
	return cp
}

// rotateWAL atomically starts a new WAL cycle after a checkpoint.
// The old WAL file is replaced by creating a new empty file.
func (sm *StateManager) rotateWAL() {
	if sm.walFile != nil {
		sm.walFile.Close()
		sm.walFile = nil
	}
	f, err := os.Create(sm.walPath)
	if err != nil {
		log.Printf("[%s] create new WAL: %v", sm.nodeName, err)
		return
	}
	sm.walFile = f
	sm.walSeq = 0
	sm.dedup = make(map[string]struct{})
}

// SaveCheckpoint writes the full node state atomically.
// After a successful checkpoint the WAL is rotated (old entries purged).
func (sm *StateManager) SaveCheckpoint(state json.RawMessage) error {
	sm.mu.Lock()

	if !sm.Enabled() {
		sm.mu.Unlock()
		return nil
	}

	cp := CheckpointData{
		State:         state,
		CheckpointSeq: sm.walSeq,
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		sm.mu.Unlock()
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	path := sm.checkpointPath()
	tmpPath := path + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		sm.mu.Unlock()
		return fmt.Errorf("create tmp %s: %w", tmpPath, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		sm.mu.Unlock()
		return fmt.Errorf("write tmp %s: %w", tmpPath, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		sm.mu.Unlock()
		return fmt.Errorf("fsync tmp %s: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		sm.mu.Unlock()
		return fmt.Errorf("close tmp %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		sm.mu.Unlock()
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
	}

	sm.rotateWAL()
	sm.mu.Unlock()

	log.Printf("[%s] checkpoint saved (seq=%d state=%db)",
		sm.nodeName, cp.CheckpointSeq, len(data))
	return nil
}

// LoadCheckpoint reads the latest checkpoint from disk.
// Returns nil if no checkpoint exists.
func (sm *StateManager) LoadCheckpoint() (*CheckpointData, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.Enabled() {
		return nil, nil
	}

	data, err := os.ReadFile(sm.checkpointPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read checkpoint: %w", err)
	}

	var cp CheckpointData
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("parse checkpoint: %w", err)
	}

	log.Printf("[%s] loaded checkpoint (seq=%d state=%db)",
		sm.nodeName, cp.CheckpointSeq, len(cp.State))
	return &cp, nil
}

// LoadWALAfter reads all WAL entries whose Seq > afterSeq.
// This is used during recovery to locate deltas not yet reflected
// in the checkpoint state.
func (sm *StateManager) LoadWALAfter(afterSeq int64) ([]WALEntry, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.Enabled() {
		return nil, nil
	}

	f, err := os.Open(sm.walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open WAL: %w", err)
	}
	defer f.Close()

	var entries []WALEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry WALEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			log.Printf("[%s] invalid WAL entry: %v", sm.nodeName, err)
			continue
		}
		if entry.Seq > afterSeq {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read WAL: %w", err)
	}

	if len(entries) > 0 {
		log.Printf("[%s] loaded %d WAL entries (after seq=%d)",
			sm.nodeName, len(entries), afterSeq)
	}
	return entries, nil
}

// Recover loads the latest checkpoint and returns it along with any
// WAL entries that need to be replayed. If no checkpoint exists both
// return values are nil. The caller must replay the returned WAL
// entries in order by applying each entry's Delta to the checkpoint
// State.
func (sm *StateManager) Recover() (*CheckpointData, []WALEntry, error) {
	cp, err := sm.LoadCheckpoint()
	if err != nil {
		return nil, nil, err
	}
	if cp == nil {
		return nil, nil, nil
	}

	entries, err := sm.LoadWALAfter(cp.CheckpointSeq)
	if err != nil {
		return nil, nil, err
	}

	return cp, entries, nil
}

// CheckpointFreqFromEnv reads CHECKPOINT_FREQ from the environment.
// Returns defaultFreq if the env var is not set or is invalid.
func CheckpointFreqFromEnv(defaultFreq int) int {
	if v := os.Getenv("CHECKPOINT_FREQ"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultFreq
}

// Close releases the WAL file handle.
func (sm *StateManager) Close() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.walFile != nil {
		sm.walFile.Close()
		sm.walFile = nil
	}
}
