// Package mcp exposes vphone-web over the Model Context Protocol (MCP) so an AI
// agent can manage VMs and drive the guest devices directly — list and boot VMs,
// see the screen, tap/swipe, press hardware keys, and run shell commands on the
// guest.
//
// The server speaks MCP over stdio (newline-delimited JSON-RPC 2.0) and is a
// *client* of a running vphone-web instance's REST API. It never opens the
// database or supervises VM processes itself, so it is safe to run alongside a
// live server — including one on another host.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// defaultProtocolVersion is advertised when a client does not request one.
const defaultProtocolVersion = "2024-11-05"

// --- JSON-RPC wire types ---------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// --- MCP content types -----------------------------------------------------

type content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"` // base64 payload for images
	MimeType string `json:"mimeType,omitempty"`
}

type toolResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

func textResult(format string, args ...any) toolResult {
	return toolResult{Content: []content{{Type: "text", Text: fmt.Sprintf(format, args...)}}}
}

func errResult(err error) toolResult {
	return toolResult{Content: []content{{Type: "text", Text: err.Error()}}, IsError: true}
}

// jsonResult renders any API payload as pretty JSON text.
func jsonResult(v any) toolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(err)
	}
	return toolResult{Content: []content{{Type: "text", Text: string(b)}}}
}

// tool is one callable MCP tool. handler is unexported so it is never marshaled
// into the tools/list response.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	handler     func(args map[string]any) toolResult
}

// Server is an MCP server backed by a vphone-web REST API.
type Server struct {
	base    string
	token   string
	version string
	client  *http.Client
	tools   []tool
	log     *slog.Logger
}

// New builds an MCP server targeting the vphone-web instance at baseURL (e.g.
// "http://127.0.0.1:8099"). token, when non-empty, is sent as a bearer token for
// servers that have access control enabled.
func New(baseURL, token, version string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		base:    strings.TrimRight(baseURL, "/"),
		token:   token,
		version: version,
		client:  &http.Client{Timeout: 120 * time.Second},
		log:     log,
	}
	s.registerTools()
	return s
}

// --- REST plumbing ---------------------------------------------------------

// call performs a request against the vphone-web API, decoding a JSON response
// into out when out is non-nil.
func (s *Server) call(method, path string, body, out any) error {
	data, _, err := s.callRaw(method, path, body)
	if err != nil {
		return err
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// callRaw performs a request and returns the raw body plus its content type, so
// binary endpoints (screenshots) work alongside JSON ones.
func (s *Server) callRaw(method, path string, body any) ([]byte, string, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, "", err
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, s.base+"/api/v1"+path, rdr)
	if err != nil {
		return nil, "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("vphone-web unreachable at %s (is the server running?): %w", s.base, err)
	}
	defer resp.Body.Close()

	data, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("%s %s → %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	if readErr != nil {
		return nil, "", readErr
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// --- argument helpers ------------------------------------------------------

func argStr(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func argFloat(args map[string]any, key string) float64 {
	switch v := args[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

func argInt(args map[string]any, key string) int {
	return int(argFloat(args, key))
}

// requireVM pulls the vm_id argument, which nearly every tool needs.
func requireVM(args map[string]any) (string, error) {
	id := argStr(args, "vm_id")
	if id == "" {
		return "", fmt.Errorf("vm_id is required (use vphone_list_vms to find one)")
	}
	return id, nil
}

// --- transport -------------------------------------------------------------

// Serve runs the MCP stdio loop until in is exhausted or ctx is cancelled.
// Messages are newline-delimited JSON-RPC 2.0 frames.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// Tool arguments are small, but be generous so a long line never truncates.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.log.Debug("mcp: malformed frame", "err", err)
			continue // nothing addressable to reply to
		}

		resp, ok := s.handle(&req)
		if !ok {
			continue // notification — no response
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// handle dispatches one JSON-RPC message. The bool reports whether a response
// should be written (false for notifications).
func (s *Server) handle(req *rpcRequest) (*rpcResponse, bool) {
	isNotification := len(req.ID) == 0

	reply := func(result any, e *rpcError) (*rpcResponse, bool) {
		if isNotification {
			return nil, false
		}
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: e}, true
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := p.ProtocolVersion
		if version == "" {
			version = defaultProtocolVersion
		}
		return reply(map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "vphone-web", "version": s.version},
		}, nil)

	case "notifications/initialized", "initialized":
		return nil, false

	case "ping":
		return reply(map[string]any{}, nil)

	case "tools/list":
		return reply(map[string]any{"tools": s.tools}, nil)

	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return reply(nil, &rpcError{Code: -32602, Message: "invalid params"})
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		for _, t := range s.tools {
			if t.Name == p.Name {
				return reply(t.handler(p.Arguments), nil)
			}
		}
		return reply(nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name})

	default:
		return reply(nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method})
	}
}
