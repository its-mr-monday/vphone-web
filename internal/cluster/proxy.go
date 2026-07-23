package cluster

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// This file adds the controller-side pieces needed to manage worker VMs from one
// UI: a per-node cache of the node's VMs (so the controller can aggregate a
// unified list and route per-VM operations), plus a pinned-TLS transport/client
// used to reach a worker's API over the control link.

// Transport returns an http.RoundTripper for reaching a node's API. For TLS
// nodes it pins the node's trusted certificate fingerprint (trust-on-first-use);
// plain nodes use the default transport.
func (m *Manager) Transport(n Node) http.RoundTripper {
	if !n.TLS {
		return http.DefaultTransport
	}
	pinned := n.CertFingerprint
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // pinned below
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return fmt.Errorf("agent presented no certificate")
				}
				if pinned != "" && !strings.EqualFold(certFingerprint(cs.PeerCertificates[0]), pinned) {
					return errCertChanged
				}
				return nil
			},
		},
	}
}

// BaseURL returns the scheme+host base for a node's API.
func (n Node) BaseURL() string {
	scheme := "http"
	if n.TLS {
		scheme = "https"
	}
	return scheme + "://" + n.Address
}

// clientFor returns an HTTP client for one node (pinned TLS + timeout).
func (m *Manager) clientFor(n Node) *http.Client {
	return &http.Client{Timeout: 8 * time.Second, Transport: m.Transport(n)}
}

// fetchVMs pulls a node's VM list over the control link and caches it. The
// worker treats a valid system password as an admin, so /api/v1/vms returns the
// full list. Each VM is stored as a decoded object for annotation + routing.
func (m *Manager) fetchVMs(ctx context.Context, n Node) {
	url := n.BaseURL() + "/api/v1/vms"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		m.setVMCache(n.ID, nil)
		return
	}
	req.Header.Set(SystemPasswordHeader, n.SystemPassword)
	resp, err := m.clientFor(n).Do(req)
	if err != nil {
		m.setVMCache(n.ID, nil)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		m.setVMCache(n.ID, nil)
		return
	}
	var vms []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&vms); err != nil {
		m.setVMCache(n.ID, nil)
		return
	}
	m.setVMCache(n.ID, vms)
}

func (m *Manager) setVMCache(nodeID string, vms []map[string]any) {
	m.vmMu.Lock()
	defer m.vmMu.Unlock()
	if m.vmCache == nil {
		m.vmCache = map[string][]map[string]any{}
	}
	if vms == nil {
		delete(m.vmCache, nodeID)
		return
	}
	m.vmCache[nodeID] = vms
}

// RemoteVMs returns every worker VM, each annotated with its node so the
// controller can present one unified list.
func (m *Manager) RemoteVMs() []map[string]any {
	nodes, err := m.store.list()
	if err != nil {
		return nil
	}
	byID := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	m.vmMu.Lock()
	defer m.vmMu.Unlock()
	var out []map[string]any
	for nodeID, vms := range m.vmCache {
		n := byID[nodeID]
		for _, vm := range vms {
			clone := make(map[string]any, len(vm)+3)
			for k, v := range vm {
				clone[k] = v
			}
			clone["node_id"] = nodeID
			clone["node_name"] = n.Name
			clone["node_address"] = n.Address
			out = append(out, clone)
		}
	}
	return out
}

// FindJobNode locates the worker node that owns a job id by querying each online
// node's job endpoint (cached after the first hit). Used to route a remote job's
// detail/logs/cancel through the controller.
func (m *Manager) FindJobNode(ctx context.Context, jobID string) (Node, bool) {
	m.jobMu.Lock()
	nid, cached := m.jobNodeCache[jobID]
	m.jobMu.Unlock()
	if cached {
		if n, err := m.store.get(nid); err == nil {
			return n, true
		}
	}
	nodes, err := m.store.list()
	if err != nil {
		return Node{}, false
	}
	for _, n := range nodes {
		if n.Status != StatusOnline {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.BaseURL()+"/api/v1/jobs/"+jobID, nil)
		if err != nil {
			continue
		}
		req.Header.Set(SystemPasswordHeader, n.SystemPassword)
		resp, err := m.clientFor(n).Do(req)
		if err != nil {
			continue
		}
		found := resp.StatusCode == http.StatusOK
		resp.Body.Close()
		if found {
			m.jobMu.Lock()
			if m.jobNodeCache == nil {
				m.jobNodeCache = map[string]string{}
			}
			m.jobNodeCache[jobID] = n.ID
			m.jobMu.Unlock()
			return n, true
		}
	}
	return Node{}, false
}

// NodeForVM returns the worker node that owns a VM id (with credentials), if the
// VM lives on a remote node. Local VMs return ok=false.
func (m *Manager) NodeForVM(vmID string) (Node, bool) {
	m.vmMu.Lock()
	var owner string
	for nodeID, vms := range m.vmCache {
		for _, vm := range vms {
			if id, _ := vm["id"].(string); id == vmID {
				owner = nodeID
				break
			}
		}
		if owner != "" {
			break
		}
	}
	m.vmMu.Unlock()
	if owner == "" {
		return Node{}, false
	}
	n, err := m.store.get(owner)
	if err != nil {
		return Node{}, false
	}
	return n, true
}
