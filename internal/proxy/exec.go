package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
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

// scriptInfoResponse is the shape returned by the script.info RPC.
type scriptInfoResponse struct {
	Programs map[string]string `json:"programs"`
}

var (
	shellPathCache   sync.Map // apiBaseURL → string
)

// resolveShellPath queries script.info to find the guest's sh path, which
// on roothide jailbreaks lives under the jbroot, not /bin/sh. The result
// is cached per API base URL.
func resolveShellPath(ctx context.Context, apiBaseURL, apiToken string) string {
	if cached, ok := shellPathCache.Load(apiBaseURL); ok {
		return cached.(string)
	}

	body, _ := json.Marshal(scriptEvalRequest{
		Method: "script.info",
		Params: scriptEvalParams{},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBaseURL+"/v1/rpc", bytes.NewReader(body))
	if err != nil {
		return "/bin/sh"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "/bin/sh"
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var info scriptInfoResponse
	if err := json.Unmarshal(respBody, &info); err != nil {
		return "/bin/sh"
	}

	for _, name := range []string{"sh", "bash", "zsh"} {
		if p, ok := info.Programs[name]; ok && p != "" {
			shellPathCache.Store(apiBaseURL, p)
			return p
		}
	}

	return "/bin/sh"
}

// ClearShellPathCache removes cached shell paths, e.g. when a VM stops.
func ClearShellPathCache(apiBaseURL string) {
	shellPathCache.Delete(apiBaseURL)
}

// Exec runs a shell command on the guest via the upstream script.eval RPC.
// The command is wrapped in a proc.run(shell, ["-c", cmd]) call. The shell
// path is resolved via script.info to handle roothide jailbreaks where
// /bin/sh doesn't exist.
func Exec(ctx context.Context, apiBaseURL, apiToken, command string, timeoutS int) (ExecResult, error) {
	if timeoutS <= 0 {
		timeoutS = int(DefaultExecTimeout.Seconds())
	}

	shellPath := resolveShellPath(ctx, apiBaseURL, apiToken)
	jsSource := fmt.Sprintf(`proc.run(%s, ["-c", %s])`, jsStringLiteral(shellPath), jsStringLiteral(command))

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
