package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ExecResult holds the output of a remote command execution.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// DefaultExecTimeout bounds a one-shot command so a hung process can't pin the
// request forever.
const DefaultExecTimeout = 30 * time.Second

// rpcRequest is the JSON body sent to POST /v1/rpc.
type rpcRequest struct {
	Method string    `json:"method"`
	Params rpcParams `json:"params"`
}

type rpcParams struct {
	Source  string `json:"source"`
	Timeout int    `json:"timeout"`
}

// rpcResponse is the JSON body returned by the upstream script.eval RPC.
type rpcResponse struct {
	OK      bool   `json:"ok"`
	Result  string `json:"result"`
	Output  string `json:"output"`
	Crashed bool   `json:"crashed"`
}

// Exec runs a command on the guest via the upstream script.eval RPC. The source
// parameter is the shell command (or script) to evaluate. TimeoutS is the
// per-command timeout in seconds passed to the upstream; if zero, DefaultExecTimeout
// is used to derive a value.
func Exec(ctx context.Context, apiBaseURL, apiToken, source string, timeoutS int) (ExecResult, error) {
	if timeoutS <= 0 {
		timeoutS = int(DefaultExecTimeout.Seconds())
	}

	body, err := json.Marshal(rpcRequest{
		Method: "script.eval",
		Params: rpcParams{
			Source:  source,
			Timeout: timeoutS,
		},
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("marshal rpc request: %w", err)
	}

	url := apiBaseURL + "/v1/rpc"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ExecResult{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ExecResult{}, fmt.Errorf("rpc call %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ExecResult{}, fmt.Errorf("read rpc response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return ExecResult{}, fmt.Errorf("rpc returned %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return ExecResult{}, fmt.Errorf("unmarshal rpc response: %w", err)
	}

	res := ExecResult{
		Stdout: rpcResp.Output,
	}
	if rpcResp.Output == "" && rpcResp.Result != "" {
		res.Stdout = rpcResp.Result
	}

	if rpcResp.Crashed {
		res.ExitCode = -1
		return res, fmt.Errorf("script crashed")
	}
	if !rpcResp.OK {
		res.ExitCode = 1
	}

	return res, nil
}
