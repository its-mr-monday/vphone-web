package vm

import (
	"fmt"
	"sync"
)

// Port offsets within a VM's allocated block. The block base is stored per-VM
// in the database; individual service ports are derived by adding these fixed
// offsets. Keep these < the configured block size (default 10).
const (
	offsetVNC   = 0 // noVNC display  (forwards VM :5901)
	offsetSSH   = 1 // primary SSH    (forwards VM :22)
	offsetSSH2  = 2 // secondary SSH  (forwards VM :22222)
	offsetRPC   = 3 // RPC channel    (forwards VM :5910)
	offsetFrida = 4 // Frida server   (forwards VM :27042)
)

// PortBlock describes the concrete host ports assigned to a single VM.
type PortBlock struct {
	Base  int `json:"base"`
	VNC   int `json:"vnc"`
	SSH   int `json:"ssh"`
	SSH2  int `json:"ssh2"`
	RPC   int `json:"rpc"`
	Frida int `json:"frida"`
}

// blockFor derives the service ports for a given block base.
func blockFor(base int) PortBlock {
	return PortBlock{
		Base:  base,
		VNC:   base + offsetVNC,
		SSH:   base + offsetSSH,
		SSH2:  base + offsetSSH2,
		RPC:   base + offsetRPC,
		Frida: base + offsetFrida,
	}
}

// PortAllocator hands out non-overlapping blocks of host ports, one block per
// VM. Blocks are laid out contiguously from base in steps of blockSize. The
// allocator is safe for concurrent use.
type PortAllocator struct {
	mu        sync.Mutex
	base      int
	blockSize int
	used      map[int]bool // set of block bases currently allocated
}

// NewPortAllocator creates an allocator over the [base, 65535] range with the
// given block size. Any blocks already in use (recovered from the DB on
// startup) should be marked via Reserve before serving requests.
func NewPortAllocator(base, blockSize int) *PortAllocator {
	return &PortAllocator{
		base:      base,
		blockSize: blockSize,
		used:      make(map[int]bool),
	}
}

// Reserve marks a previously-allocated block base as in use. It is used during
// startup reconciliation to rebuild allocator state from persisted VMs. It is
// idempotent and does not fail if the base is outside the normal range (a VM
// may have been created under a different config).
func (a *PortAllocator) Reserve(base int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.used[base] = true
}

// Allocate returns the next free port block. It returns an error if the port
// space is exhausted.
func (a *PortAllocator) Allocate() (PortBlock, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for base := a.base; base+a.blockSize-1 <= 65535; base += a.blockSize {
		if !a.used[base] {
			a.used[base] = true
			return blockFor(base), nil
		}
	}
	return PortBlock{}, fmt.Errorf("port pool exhausted (base=%d, block=%d)", a.base, a.blockSize)
}

// Release returns a block to the pool so it can be reused.
func (a *PortAllocator) Release(base int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.used, base)
}

// Block derives the service ports for a stored block base without touching
// allocator state. Useful for read paths that already know the VM's base.
func (a *PortAllocator) Block(base int) PortBlock {
	return blockFor(base)
}
