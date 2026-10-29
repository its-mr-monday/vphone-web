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

// scriptEvalRequest is the JSON body sent to POST /v1/rpc for script.eval.
type scriptEvalRequest struct {
	Method string          `json:"method"`
	Params scriptEvalParams `json:"params"`
}

type scriptEvalParams struct {
	Source  string `json:"source"`
	Timeout int    `json:"timeout"`
}

// scriptEvalResponse is the JSON body returned by the upstream script.eval RPC.
type scriptEvalResponse struct {
	OK      bool            `json:"ok"`
	Result  json.RawMessage `json:"result"`
	Output  string          `json:"output"`
	Error   string          `json:"error"`
	Crashed bool            `json:"crashed"`
}

// procRunResult is the shape returned by proc.run() inside the JSC session.
type procRunResult struct {
	Status int    `json:"status"`
	Signal *int   `json:"signal"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// Exec runs a shell command on the guest via the upstream script.eval RPC.
// The command is wrapped in a proc.run("/bin/sh", ["-c", cmd]) call so it
// executes as a real shell command, not as JavaScript.
func Exec(ctx context.Context, apiBaseURL, apiToken, command string, timeoutS int) (ExecResult, error) {
	if timeoutS <= 0 {
		timeoutS = int(DefaultExecTimeout.Seconds())
	}

	jsSource := fmt.Sprintf(`proc.run("/bin/sh", ["-c", %s])`, jsStringLiteral(command))

	body, err := json.Marshal(scriptEvalRequest{
		Method: "script.eval",
		Params: scriptEvalParams{
			Source:  jsSource,
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

	var rpcResp scriptEvalResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return ExecResult{}, fmt.Errorf("unmarshal rpc response: %w", err)
	}

	if rpcResp.Crashed {
		return ExecResult{ExitCode: -1, Stderr: rpcResp.Error}, fmt.Errorf("script crashed")
	}

	if !rpcResp.OK {
		return ExecResult{ExitCode: 1, Stderr: rpcResp.Error}, nil
	}

	var pr procRunResult
	if err := json.Unmarshal(rpcResp.Result, &pr); err != nil {
		return ExecResult{Stdout: string(rpcResp.Result)}, nil
	}

	return ExecResult{
		Stdout:   pr.Stdout,
		Stderr:   pr.Stderr,
		ExitCode: pr.Status,
	}, nil
}

// jsStringLiteral JSON-encodes s as a JavaScript string literal safe for
// embedding in a JS source template. It reuses JSON encoding since JSON
// string syntax is a subset of JS string syntax.
func jsStringLiteral(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
