// Package cluster implements the controller side of vphone-web clustering: a
// registry of worker nodes (each a `vphone-web --agent`), reached over an
// authenticated control link, with periodic health checks and status tracking.
package cluster

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a node lookup fails.
var ErrNotFound = errors.New("node not found")

// Status is a node's reachability state.
type Status string

const (
	StatusOnline  Status = "ONLINE"
	StatusOffline Status = "OFFLINE"
	StatusUnknown Status = "UNKNOWN"
)

// Node is a registered worker. SystemPassword is never serialized to clients.
type Node struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"` // host:port of the agent
	TLS      bool   `json:"tls"`     // reach the agent over https
	Status   Status `json:"status"`
	Hostname string `json:"hostname,omitempty"`
	Chip     string `json:"chip,omitempty"`
	CPU      int    `json:"cpu,omitempty"`
	MemoryMB int    `json:"memory_mb,omitempty"`
	RunningVMs int  `json:"running_vms"`
	Version    string `json:"version,omitempty"`
	Error      string `json:"error,omitempty"`
	// CertFingerprint is the pinned SHA-256 of the agent's TLS certificate
	// (hex), used for trust-on-first-use verification of the control link.
	CertFingerprint string    `json:"cert_fingerprint,omitempty"`
	LastSeen        time.Time `json:"last_seen,omitempty"`
	CreatedAt       time.Time `json:"created_at"`

	SystemPassword string `json:"-"`
}

// AgentHealth is what an agent's /api/v1/agent/health returns.
type AgentHealth struct {
	OK         bool   `json:"ok"`
	Hostname   string `json:"hostname"`
	Chip       string `json:"chip"`
	CPU        int    `json:"cpu"`
	MemoryMB   int    `json:"memory_mb"`
	RunningVMs int    `json:"running_vms"`
	Version    string `json:"version"`
}

const rfc3339 = time.RFC3339Nano

type store struct{ db *sql.DB }

const nodeCols = `id, name, address, tls, system_password, status, hostname, chip,
	cpu, memory_mb, running_vms, version, error, cert_fingerprint, last_seen, created_at`

func scanNode(s interface{ Scan(...any) error }) (Node, error) {
	var (
		n                   Node
		tlsInt              int
		lastSeen, createdAt string
	)
	if err := s.Scan(&n.ID, &n.Name, &n.Address, &tlsInt, &n.SystemPassword, &n.Status,
		&n.Hostname, &n.Chip, &n.CPU, &n.MemoryMB, &n.RunningVMs, &n.Version,
		&n.Error, &n.CertFingerprint, &lastSeen, &createdAt); err != nil {
		return Node{}, err
	}
	n.TLS = tlsInt != 0
	if lastSeen != "" {
		n.LastSeen, _ = time.Parse(rfc3339, lastSeen)
	}
	n.CreatedAt, _ = time.Parse(rfc3339, createdAt)
	return n, nil
}

func (st *store) insert(n Node) error {
	_, err := st.db.Exec(`INSERT INTO nodes (`+nodeCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.Name, n.Address, boolInt(n.TLS), n.SystemPassword, n.Status, n.Hostname, n.Chip,
		n.CPU, n.MemoryMB, n.RunningVMs, n.Version, n.Error, n.CertFingerprint,
		tstr(n.LastSeen), n.CreatedAt.Format(rfc3339))
	if err != nil {
		return fmt.Errorf("insert node: %w", err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (st *store) list() ([]Node, error) {
	rows, err := st.db.Query(`SELECT ` + nodeCols + ` FROM nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Node, 0)
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (st *store) get(id string) (Node, error) {
	row := st.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE id = ?`, id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	return n, err
}

func (st *store) updateHealth(n Node) error {
	_, err := st.db.Exec(
		`UPDATE nodes SET status=?, hostname=?, chip=?, cpu=?, memory_mb=?,
		 running_vms=?, version=?, error=?, last_seen=? WHERE id=?`,
		n.Status, n.Hostname, n.Chip, n.CPU, n.MemoryMB, n.RunningVMs,
		n.Version, n.Error, tstr(n.LastSeen), n.ID)
	return err
}

func (st *store) delete(id string) error {
	res, err := st.db.Exec(`DELETE FROM nodes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return ErrNotFound
	}
	return nil
}

func tstr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(rfc3339)
}
