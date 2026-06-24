package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStateManagerDisabled(t *testing.T) {
	sm := NewStateManager("test", "test", "", 100)
	if sm.Enabled() {
		t.Fatal("expected disabled StateManager when dirPath is empty")
	}

	if err := sm.AppendWAL("b1", json.RawMessage(`{"x":1}`)); err != nil {
		t.Fatalf("AppendWAL on disabled manager: %v", err)
	}
	sm.MarkApplied("b1")
	if sm.IsApplied("b1") {
		t.Fatal("IsApplied should return false on disabled manager")
	}
	if sm.ShouldCheckpoint() {
		t.Fatal("ShouldCheckpoint should return false on disabled manager")
	}
	if err := sm.SaveCheckpoint(json.RawMessage(`{}`)); err != nil {
		t.Fatalf("SaveCheckpoint on disabled manager: %v", err)
	}
	cp, entries, err := sm.Recover()
	if err != nil {
		t.Fatalf("Recover on disabled manager: %v", err)
	}
	if cp != nil || entries != nil {
		t.Fatal("Recover on disabled manager should return nil, nil")
	}
}

func TestStateManagerCheckpointAndRecovery(t *testing.T) {
	dir := t.TempDir()

	sm := NewStateManager("test", "testnode", dir, 3)
	if !sm.Enabled() {
		t.Fatal("expected enabled StateManager")
	}
	defer sm.Close()

	// Append WAL entries
	state := json.RawMessage(`{"counter":0}`)

	if err := sm.AppendWAL("b1", json.RawMessage(`{"inc":1}`)); err != nil {
		t.Fatalf("AppendWAL b1: %v", err)
	}
	sm.MarkApplied("b1")

	if err := sm.AppendWAL("b2", json.RawMessage(`{"inc":2}`)); err != nil {
		t.Fatalf("AppendWAL b2: %v", err)
	}
	sm.MarkApplied("b2")

	// Checkpoint after 2 entries (freq=3, so batchCount=2, not due yet)
	if sm.ShouldCheckpoint() {
		t.Fatal("checkpoint should not be due yet")
	}
	if sm.ShouldCheckpoint() {
		t.Fatal("checkpoint should not be due yet at count 2")
	}

	// Third call triggers checkpoint
	if !sm.ShouldCheckpoint() {
		t.Fatal("checkpoint should be due at count 3")
	}

	if err := sm.SaveCheckpoint(state); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	// Append more entries after checkpoint
	if err := sm.AppendWAL("b3", json.RawMessage(`{"inc":3}`)); err != nil {
		t.Fatalf("AppendWAL b3: %v", err)
	}
	sm.MarkApplied("b3")

	// Verify dedup tracking
	if !sm.IsApplied("b1") {
		t.Fatal("b1 should be marked as applied")
	}
	if !sm.IsApplied("b3") {
		t.Fatal("b3 should be marked as applied")
	}

	// Create a new StateManager pointing to same dir (simulating recovery)
	sm2 := NewStateManager("test", "testnode", dir, 3)
	defer sm2.Close()

	cp, entries, err := sm2.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if cp == nil {
		t.Fatal("expected checkpoint data")
	}

	// Verify checkpoint state
	var result struct {
		Counter int `json:"counter"`
	}
	if err := json.Unmarshal(cp.State, &result); err != nil {
		t.Fatalf("unmarshal checkpoint state: %v", err)
	}
	if result.Counter != 0 {
		t.Fatalf("expected counter=0, got %d", result.Counter)
	}

	// Verify WAL entries (only entries after checkpoint seq)
	if len(entries) != 1 {
		t.Fatalf("expected 1 WAL entry after checkpoint, got %d", len(entries))
	}
	if entries[0].BatchID != "b3" {
		t.Fatalf("expected batch b3, got %s", entries[0].BatchID)
	}
}

func TestStateManagerAtomicCheckpoint(t *testing.T) {
	dir := t.TempDir()

	sm := NewStateManager("test", "atomic", dir, 1)
	defer sm.Close()

	// Write checkpoint with some state
	if err := sm.SaveCheckpoint(json.RawMessage(`{"v":"original"}`)); err != nil {
		t.Fatalf("first checkpoint: %v", err)
	}

	// Verify checkpoint file exists
	cpPath := sm.checkpointPath()
	if _, err := os.Stat(cpPath); os.IsNotExist(err) {
		t.Fatal("checkpoint file should exist")
	}

	// Verify no tmp file left behind
	tmpPath := cpPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatal("tmp file should have been cleaned up")
	}

	// Recover
	cp, _, err := sm.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if cp == nil {
		t.Fatal("expected checkpoint")
	}
	if string(cp.State) != `{"v":"original"}` {
		t.Fatalf("unexpected state: %s", string(cp.State))
	}
}

func TestStateManagerWALRotation(t *testing.T) {
	dir := t.TempDir()

	sm := NewStateManager("test", "rotation", dir, 2)
	defer sm.Close()

	// Write a batch
	if err := sm.AppendWAL("b1", json.RawMessage(`{"x":1}`)); err != nil {
		t.Fatalf("AppendWAL: %v", err)
	}
	sm.MarkApplied("b1")

	// Checkpoint (also rotates WAL)
	if sm.ShouldCheckpoint() {
		if err := sm.SaveCheckpoint(json.RawMessage(`{"v":1}`)); err != nil {
			t.Fatalf("SaveCheckpoint: %v", err)
		}
	}

	// After rotation, new WAL is empty
	// No entries should load after CheckpointSeq = last seq
	cp, entries, err := sm.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if cp == nil {
		t.Fatal("expected checkpoint")
	}
	if len(entries) > 0 {
		t.Fatalf("expected 0 entries after checkpoint with empty WAL, got %d", len(entries))
	}
}

func TestStateManagerMultipleWALEntries(t *testing.T) {
	dir := t.TempDir()

	sm := NewStateManager("test", "multiwal", dir, 100)
	defer sm.Close()

	// Write 5 WAL entries
	for i := 1; i <= 5; i++ {
		batchID := ""
		switch i {
		case 1:
			batchID = "b1"
		case 2:
			batchID = "b2"
		case 3:
			batchID = "b3"
		case 4:
			batchID = "b4"
		case 5:
			batchID = "b5"
		}
		delta, _ := json.Marshal(map[string]int{"idx": i})
		if err := sm.AppendWAL(batchID, delta); err != nil {
			t.Fatalf("AppendWAL %s: %v", batchID, err)
		}
		sm.MarkApplied(batchID)
	}

	// Checkpoint at batch 3
	if err := sm.SaveCheckpoint(json.RawMessage(`{"checkpointed":true}`)); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	// After checkpoint + rotation: WAL is empty, no entries should be found
	cp, entries, err := sm.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if cp == nil {
		t.Fatal("expected checkpoint")
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after WAL rotation, got %d", len(entries))
	}
}

func TestStateManagerRecoverFromPartialWAL(t *testing.T) {
	dir := t.TempDir()

	sm1 := NewStateManager("test", "partial", dir, 3)
	defer sm1.Close()

	// Simulate: checkpoint at seq=2, then write one more entry
	if err := sm1.AppendWAL("b1", json.RawMessage(`{"v":1}`)); err != nil {
		t.Fatalf("AppendWAL b1: %v", err)
	}
	sm1.MarkApplied("b1")

	if err := sm1.AppendWAL("b2", json.RawMessage(`{"v":2}`)); err != nil {
		t.Fatalf("AppendWAL b2: %v", err)
	}
	sm1.MarkApplied("b2")

	if err := sm1.SaveCheckpoint(json.RawMessage(`{"state":"after_b2"}`)); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	// After checkpoint, WAL is rotated. Write new entry.
	if err := sm1.AppendWAL("b3", json.RawMessage(`{"v":3}`)); err != nil {
		t.Fatalf("AppendWAL b3: %v", err)
	}
	sm1.MarkApplied("b3")
	sm1.Close()

	// Recovery from fresh manager
	sm2 := NewStateManager("test", "partial", dir, 3)
	defer sm2.Close()

	cp, entries, err := sm2.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if cp == nil {
		t.Fatal("expected checkpoint")
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 WAL entry, got %d", len(entries))
	}
	if entries[0].BatchID != "b3" {
		t.Fatalf("expected b3, got %s", entries[0].BatchID)
	}
	var delta map[string]int
	if err := json.Unmarshal(entries[0].Delta, &delta); err != nil {
		t.Fatalf("unmarshal delta: %v", err)
	}
	if delta["v"] != 3 {
		t.Fatalf("expected delta.v=3, got %d", delta["v"])
	}
}
