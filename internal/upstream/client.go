package upstream

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// Client is a typed HTTP client for the vphoned API running inside a VM.
// The API is exposed on a TCP port on the host (proxied from VSOCK 1339).
type Client struct {
	BaseURL    string       // e.g. "http://127.0.0.1:8765"
	Token      string       // bearer token (32-byte hex string)
	HTTPClient *http.Client // with reasonable timeouts
}

// NewClient creates a new vphoned API client.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// RPCError represents an error returned by the vphoned RPC API.
type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %s: %s", e.Code, e.Message)
}

// HealthResponse is the response from GET /v1/health.
type HealthResponse struct {
	Capabilities []string `json:"capabilities"`
	SetupPending bool     `json:"setup_pending"`
	Instance     string   `json:"instance"`
}

// DeviceInfo holds the result of the device.info RPC call.
// The upstream schema is large, so we keep it as raw JSON for now.
type DeviceInfo struct {
	Raw json.RawMessage
}

// rpcRequest is the JSON body sent to POST /v1/rpc.
type rpcRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// rpcResponse is the JSON response from POST /v1/rpc.
type rpcResponse struct {
	Type   string           `json:"type"`
	ID     string           `json:"id"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *RPCError        `json:"error,omitempty"`
}

// screenshotResult is the decoded result from screen.screenshot.
type screenshotResult struct {
	MimeType string `json:"mime_type"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Data     string `json:"data"` // base64
}

// RPC calls POST /v1/rpc with a JSON-RPC style request and returns the result field.
func (c *Client) RPC(ctx context.Context, method string, params any) (json.RawMessage, error) {
	req := rpcRequest{
		ID:     uuid.New().String(),
		Method: method,
		Params: params,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal rpc request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/rpc", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("rpc %s: %w", method, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB limit
	if err != nil {
		return nil, fmt.Errorf("read rpc response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rpc %s: HTTP %d: %s", method, resp.StatusCode, string(respBody))
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal rpc response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}

	return rpcResp.Result, nil
}

// Health calls GET /v1/health (not RPC).
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/health", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("health: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("health: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("decode health response: %w", err)
	}
	return &health, nil
}

// DeviceInfo calls RPC device.info and returns device information.
func (c *Client) DeviceInfo(ctx context.Context) (*DeviceInfo, error) {
	result, err := c.RPC(ctx, "device.info", nil)
	if err != nil {
		return nil, err
	}
	return &DeviceInfo{Raw: result}, nil
}

// Screenshot calls RPC screen.screenshot and returns the decoded image bytes and MIME type.
func (c *Client) Screenshot(ctx context.Context) (data []byte, mimeType string, err error) {
	result, err := c.RPC(ctx, "screen.screenshot", nil)
	if err != nil {
		return nil, "", err
	}

	var ss screenshotResult
	if err := json.Unmarshal(result, &ss); err != nil {
		return nil, "", fmt.Errorf("unmarshal screenshot result: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(ss.Data)
	if err != nil {
		return nil, "", fmt.Errorf("decode screenshot data: %w", err)
	}

	return decoded, ss.MimeType, nil
}

// Tap sends a single tap at the given coordinates.
func (c *Client) Tap(ctx context.Context, x, y float64) error {
	_, err := c.RPC(ctx, "input.tap", map[string]float64{"x": x, "y": y})
	return err
}

// DoubleTap sends a double tap at the given coordinates.
func (c *Client) DoubleTap(ctx context.Context, x, y float64) error {
	_, err := c.RPC(ctx, "input.double_tap", map[string]float64{"x": x, "y": y})
	return err
}

// LongPress sends a long press at the given coordinates for the specified duration.
func (c *Client) LongPress(ctx context.Context, x, y float64, durationMS int) error {
	_, err := c.RPC(ctx, "input.long_press", map[string]any{
		"x":           x,
		"y":           y,
		"duration_ms": durationMS,
	})
	return err
}

// Swipe sends a swipe gesture from one point to another over the specified duration.
func (c *Client) Swipe(ctx context.Context, fromX, fromY, toX, toY float64, durationMS int) error {
	_, err := c.RPC(ctx, "input.swipe", map[string]any{
		"x1":          fromX,
		"y1":          fromY,
		"x2":          toX,
		"y2":          toY,
		"duration_ms": durationMS,
	})
	return err
}

// Button presses a hardware button by name (home, lock, volume_up, volume_down).
func (c *Client) Button(ctx context.Context, name string) error {
	_, err := c.RPC(ctx, "input.button", map[string]string{"name": name})
	return err
}

// Key sends a named key press.
func (c *Client) Key(ctx context.Context, name string) error {
	_, err := c.RPC(ctx, "input.key", map[string]string{"name": name})
	return err
}

// TypeText types text character by character.
func (c *Client) TypeText(ctx context.Context, text string) error {
	_, err := c.RPC(ctx, "input.type", map[string]string{"text": text})
	return err
}

// Paste pastes text via the clipboard.
func (c *Client) Paste(ctx context.Context, text string) error {
	_, err := c.RPC(ctx, "input.paste", map[string]string{"text": text})
	return err
}

// Unlock attempts to unlock the screen with the given passcode.
func (c *Client) Unlock(ctx context.Context, passcode string, timeoutS int) error {
	_, err := c.RPC(ctx, "screen.unlock", map[string]any{
		"passcode": passcode,
		"timeout":  timeoutS,
	})
	return err
}

// ProcessesList returns the list of running processes, optionally filtered.
func (c *Client) ProcessesList(ctx context.Context, filter string) (json.RawMessage, error) {
	params := map[string]string{}
	if filter != "" {
		params["filter"] = filter
	}
	return c.RPC(ctx, "processes.list", params)
}

// AppsList returns the list of installed apps.
func (c *Client) AppsList(ctx context.Context) (json.RawMessage, error) {
	return c.RPC(ctx, "apps.list", nil)
}

// Respring triggers a respring (SpringBoard restart) with force.
func (c *Client) Respring(ctx context.Context) error {
	_, err := c.RPC(ctx, "system.respring", map[string]bool{"force": true})
	return err
}

// Reboot triggers a system reboot. If userspace is true, performs a userspace reboot only.
func (c *Client) Reboot(ctx context.Context, userspace bool) error {
	_, err := c.RPC(ctx, "system.reboot", map[string]any{
		"force":     true,
		"userspace": userspace,
	})
	return err
}

// UploadFile streams a file to the guest via PUT /v1/files/content.
func (c *Client) UploadFile(ctx context.Context, guestPath string, mode string, body io.Reader, size int64) error {
	u := c.BaseURL + "/v1/files/content?path=" + url.QueryEscape(guestPath)
	if mode != "" {
		u += "&mode=" + url.QueryEscape(mode)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, u, body)
	if err != nil {
		return fmt.Errorf("create upload request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	httpReq.Header.Set("Content-Type", "application/octet-stream")
	if size > 0 {
		httpReq.ContentLength = size
	}

	longClient := &http.Client{Timeout: 120 * time.Second}
	resp, err := longClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("upload file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("upload file: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
