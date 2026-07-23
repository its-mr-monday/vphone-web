package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Manager is the controller-side node registry with health monitoring.
type Manager struct {
	store     *store
	log       *slog.Logger
	client    *http.Client
	heartbeat time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewManager constructs the cluster manager.
func NewManager(db *sql.DB, heartbeat time.Duration, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if heartbeat <= 0 {
		heartbeat = 10 * time.Second
	}
	return &Manager{
		store:     &store{db: db},
		log:       log,
		client:    &http.Client{Timeout: 5 * time.Second},
		heartbeat: heartbeat,
	}
}

// Start launches the background health-poll loop. Call Stop on shutdown.
func (m *Manager) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()
	go m.healthLoop(ctx)
}

// Stop halts the health loop.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
}

// List returns all registered nodes with current status.
func (m *Manager) List() ([]Node, error) { return m.store.list() }

// Get returns one node.
func (m *Manager) Get(id string) (Node, error) { return m.store.get(id) }

// Register validates connectivity to an agent (address + system password), then
// stores it. Registration fails if the agent is unreachable or the password is
// wrong, so a node only lands in the registry once the control link is proven.
func (m *Manager) Register(name, address, systemPassword string) (Node, error) {
	name = strings.TrimSpace(name)
	address = normalizeAddress(address)
	if name == "" || address == "" {
		return Node{}, fmt.Errorf("name and address are required")
	}
	health, err := m.probe(context.Background(), address, systemPassword)
	if err != nil {
		return Node{}, fmt.Errorf("cannot reach agent at %s: %w", address, err)
	}

	now := time.Now()
	n := Node{
		ID: uuid.NewString(), Name: name, Address: address,
		SystemPassword: systemPassword, Status: StatusOnline,
		Hostname: health.Hostname, Chip: health.Chip, CPU: health.CPU,
		MemoryMB: health.MemoryMB, RunningVMs: health.RunningVMs, Version: health.Version,
		LastSeen: now, CreatedAt: now,
	}
	if err := m.store.insert(n); err != nil {
		return Node{}, err
	}
	m.log.Info("registered node", "name", name, "address", address, "chip", health.Chip)
	return n, nil
}

// Delete removes a node from the registry.
func (m *Manager) Delete(id string) error { return m.store.delete(id) }

// healthLoop polls every node on the heartbeat interval and updates status.
func (m *Manager) healthLoop(ctx context.Context) {
	// Initial sweep shortly after start.
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			m.sweep(ctx)
			timer.Reset(m.heartbeat)
		}
	}
}

// sweep health-checks all nodes concurrently.
func (m *Manager) sweep(ctx context.Context) {
	nodes, err := m.store.list()
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func(n Node) {
			defer wg.Done()
			m.checkNode(ctx, n)
		}(n)
	}
	wg.Wait()
}

// checkNode probes one node and persists its new status.
func (m *Manager) checkNode(ctx context.Context, n Node) {
	health, err := m.probe(ctx, n.Address, n.SystemPassword)
	if err != nil {
		if n.Status != StatusOffline {
			m.log.Warn("node offline", "name", n.Name, "address", n.Address, "err", err)
		}
		n.Status = StatusOffline
		n.Error = err.Error()
		_ = m.store.updateHealth(n)
		return
	}
	n.Status = StatusOnline
	n.Error = ""
	n.Hostname, n.Chip, n.CPU = health.Hostname, health.Chip, health.CPU
	n.MemoryMB, n.RunningVMs, n.Version = health.MemoryMB, health.RunningVMs, health.Version
	n.LastSeen = time.Now()
	_ = m.store.updateHealth(n)
}

// probe calls an agent's health endpoint with the system password.
func (m *Manager) probe(ctx context.Context, address, systemPassword string) (AgentHealth, error) {
	url := "http://" + address + "/api/v1/agent/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return AgentHealth{}, err
	}
	req.Header.Set(SystemPasswordHeader, systemPassword)
	resp, err := m.client.Do(req)
	if err != nil {
		return AgentHealth{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return AgentHealth{}, fmt.Errorf("invalid system password")
	}
	if resp.StatusCode != http.StatusOK {
		return AgentHealth{}, fmt.Errorf("agent returned %d", resp.StatusCode)
	}
	var h AgentHealth
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return AgentHealth{}, fmt.Errorf("decode health: %w", err)
	}
	return h, nil
}

// SystemPasswordHeader carries the cluster shared secret on the control link.
const SystemPasswordHeader = "X-System-Password"

// normalizeAddress trims a scheme and whitespace, leaving host:port.
func normalizeAddress(a string) string {
	a = strings.TrimSpace(a)
	a = strings.TrimPrefix(a, "http://")
	a = strings.TrimPrefix(a, "https://")
	return strings.TrimRight(a, "/")
}
