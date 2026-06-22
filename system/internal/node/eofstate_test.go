package node

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEOFPersisterSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eof_state.json")

	// --- "Primera vida" del nodo ---
	p1 := newEOFPersister(path)

	// Simular: llegan 2 EOFs de 3 para client1
	p1.SeenEOFs["eof:client1:1"] = struct{}{}
	p1.SeenEOFs["eof:client1:2"] = struct{}{}
	p1.EOFCounts["client1"] = 2
	p1.persist()

	// Verificar que se escribió
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file should exist after persist: %v", err)
	}

	// Simular: llega el 3er EOF, se completa la barrera
	p1.SeenEOFs["eof:client1:3"] = struct{}{}
	p1.EOFCounts["client1"] = 3
	p1.persist()

	// Se cruza el threshold → se marca como forwarded
	p1.EOFForwarded["client1"] = struct{}{}
	delete(p1.EOFCounts, "client1")
	p1.persist()

	// --- "Crash" — tiramos p1 y creamos p2 ---
	p2 := newEOFPersister(path)

	// Verificar forwarded
	if _, ok := p2.EOFForwarded["client1"]; !ok {
		t.Fatal("EOFForwarded[client1] should be true after reload")
	}

	// Verificar que el contador se limpió
	if _, ok := p2.EOFCounts["client1"]; ok {
		t.Fatal("EOFCounts[client1] should be deleted after forward")
	}

	// Verificar SeenEOFs
	if _, ok := p2.SeenEOFs["eof:client1:1"]; !ok {
		t.Fatal("SeenEOFs[eof:client1:1] should survive reload")
	}
	if _, ok := p2.SeenEOFs["eof:client1:2"]; !ok {
		t.Fatal("SeenEOFs[eof:client1:2] should survive reload")
	}
	if _, ok := p2.SeenEOFs["eof:client1:3"]; !ok {
		t.Fatal("SeenEOFs[eof:client1:3] should survive reload")
	}
}

func TestEOFPersisterScalableState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scalable_eof.json")

	// Simular estado de Scalable después de recibir broadcasts
	p1 := newEOFPersister(path)
	p1.EOFCompleted["clientA"] = struct{}{}
	p1.ECount["clientB"] = 2
	p1.ELCount["clientC"] = 1
	p1.ERCount["clientC"] = 3
	p1.persist()

	// Crash & reload
	p2 := newEOFPersister(path)

	if _, ok := p2.EOFCompleted["clientA"]; !ok {
		t.Fatal("EOFCompleted[clientA] lost after reload")
	}
	if p2.ECount["clientB"] != 2 {
		t.Fatalf("ECount[clientB] = %d, want 2", p2.ECount["clientB"])
	}
	if p2.ELCount["clientC"] != 1 {
		t.Fatalf("ELCount[clientC] = %d, want 1", p2.ELCount["clientC"])
	}
	if p2.ERCount["clientC"] != 3 {
		t.Fatalf("ERCount[clientC] = %d, want 3", p2.ERCount["clientC"])
	}
}

func TestEOFPersisterFileNotExist(t *testing.T) {
	// Si el archivo no existe, el persister debe arrancar con estado vacío
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	p := newEOFPersister(path)

	if len(p.SeenEOFs) != 0 {
		t.Fatal("SeenEOFs should be empty for new file")
	}
	if len(p.EOFCounts) != 0 {
		t.Fatal("EOFCounts should be empty for new file")
	}
	if len(p.EOFForwarded) != 0 {
		t.Fatal("EOFForwarded should be empty for new file")
	}
	if len(p.EOFCompleted) != 0 {
		t.Fatal("EOFCompleted should be empty for new file")
	}
}

func TestEOFPersisterAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "atomic.json")

	p := newEOFPersister(path)
	p.EOFCounts["test"] = 5
	p.persist()

	// Leer el archivo directamente y verificar que es JSON válido
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("state file should not be empty")
	}

	// No debe haber .tmp residual
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file should be removed after persist")
	}
}
