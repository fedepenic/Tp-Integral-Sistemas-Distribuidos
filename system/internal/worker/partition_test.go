package worker

import "testing"

func TestPartitionForKeyStable(t *testing.T) {
	p1 := PartitionForKey("bank-A", 4)
	p2 := PartitionForKey("bank-A", 4)
	if p1 != p2 {
		t.Fatalf("expected stable partition, got %d and %d", p1, p2)
	}
	if p1 < 0 || p1 >= 4 {
		t.Fatalf("expected partition in range, got %d", p1)
	}
}
