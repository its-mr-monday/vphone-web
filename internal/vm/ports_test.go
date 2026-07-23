package vm

import "testing"

func TestAllocateSequential(t *testing.T) {
	a := NewPortAllocator(10000, 10)

	b1, err := a.Allocate()
	if err != nil {
		t.Fatalf("allocate 1: %v", err)
	}
	if b1.Base != 10000 || b1.VNC != 10000 || b1.SSH != 10001 || b1.RPC != 10003 {
		t.Fatalf("unexpected block: %+v", b1)
	}

	b2, err := a.Allocate()
	if err != nil {
		t.Fatalf("allocate 2: %v", err)
	}
	if b2.Base != 10010 {
		t.Fatalf("expected second base 10010, got %d", b2.Base)
	}
}

func TestReleaseReuse(t *testing.T) {
	a := NewPortAllocator(10000, 10)
	b1, _ := a.Allocate()
	_, _ = a.Allocate()
	a.Release(b1.Base)

	b3, err := a.Allocate()
	if err != nil {
		t.Fatalf("allocate after release: %v", err)
	}
	if b3.Base != b1.Base {
		t.Fatalf("expected released base %d reused, got %d", b1.Base, b3.Base)
	}
}

func TestReserveSkips(t *testing.T) {
	a := NewPortAllocator(10000, 10)
	a.Reserve(10000) // pretend 10000 is already taken (e.g. recovered from DB)

	b, err := a.Allocate()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if b.Base == 10000 {
		t.Fatalf("allocator handed out a reserved base")
	}
	if b.Base != 10010 {
		t.Fatalf("expected 10010, got %d", b.Base)
	}
}

func TestExhaustion(t *testing.T) {
	// Small window: base near the top so only a couple of blocks fit.
	a := NewPortAllocator(65520, 10)
	if _, err := a.Allocate(); err != nil {
		t.Fatalf("first allocate should succeed: %v", err)
	}
	// 65520..65529 used; next would be 65530..65539 (<=65535? no, 65539>65535) -> exhausted.
	if _, err := a.Allocate(); err == nil {
		t.Fatalf("expected exhaustion error")
	}
}
