package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// trollVNCResources caches the extracted TrollVNC binary and plist from the
// vphone-cli scripts/resources/cfw_input.tar.zst archive.
var trollVNCResources struct {
	once     sync.Once
	binary   []byte // trollvncserver arm64 Mach-O
	plist    []byte // trollvnc.plist LaunchDaemon
	err      error
}

func loadTrollVNCResources(vphoneCLIPath string) (binary, plist []byte, err error) {
	trollVNCResources.once.Do(func() {
		archive := filepath.Join(vphoneCLIPath, "scripts", "resources", "cfw_input.tar.zst")
		if _, statErr := os.Stat(archive); statErr != nil {
			trollVNCResources.err = fmt.Errorf("cfw_input.tar.zst not found at %s", archive)
			return
		}

		trollVNCResources.binary, trollVNCResources.err = extractFromTarZst(archive,
			"cfw_input/jb/iosbinpack64.tar", "iosbinpack64/bin/trollvncserver")
		if trollVNCResources.err != nil {
			return
		}

		trollVNCResources.plist, trollVNCResources.err = extractFileFromTarZst(archive,
			"cfw_input/jb/LaunchDaemons/trollvnc.plist")
	})
	return trollVNCResources.binary, trollVNCResources.plist, trollVNCResources.err
}

// extractFileFromTarZst extracts a single file from a .tar.zst archive.
func extractFileFromTarZst(archivePath, filePath string) ([]byte, error) {
	cmd := exec.Command("sh", "-c",
		fmt.Sprintf("zstd -d %q -c | tar xf - -O %q",
			archivePath, filePath))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("extract %s: %w", filePath, err)
	}
	return out, nil
}

// extractFromTarZst extracts a file from a nested tar inside a .tar.zst.
// First extracts the inner tar, then extracts the target file from it.
func extractFromTarZst(archivePath, innerTar, filePath string) ([]byte, error) {
	// Extract inner tar to stdout, then pipe to tar to extract the file.
	cmd := exec.Command("sh", "-c",
		fmt.Sprintf("zstd -d %q -c | tar xf - -O %q | tar xf - -O %q",
			archivePath, innerTar, filePath))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("extract %s from %s: %w", filePath, innerTar, err)
	}
	return out, nil
}

// trollVNCStatusResponse is returned by GET /api/v1/vms/:id/vnc-server.
type trollVNCStatusResponse struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Port      int    `json:"port"`
	Error     string `json:"error,omitempty"`
}

const (
	trollVNCBinPath   = "/private/var/trollvnc/trollvncserver"
	trollVNCPlistPath = "/private/var/trollvnc/trollvnc.plist"
	trollVNCLabel     = "com.vphone.trollvnc"
)

// trollVNCStatus handles GET /api/v1/vms/:id/vnc-server — checks whether
// TrollVNC is installed and running on the guest.
func (s *Server) trollVNCStatus(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	resp := trollVNCStatusResponse{Port: 5901}

	// Check if the binary exists on the guest.
	result, err := client.RPC(r.Context(), "files.read", map[string]any{
		"path":   trollVNCBinPath,
		"binary": true,
		"limit":  1,
	})
	if err == nil && result != nil {
		resp.Installed = true
	}

	// Check if the service is running via process list.
	if resp.Installed {
		result, err = client.RPC(r.Context(), "processes.list", map[string]string{
			"filter": "trollvncserver",
		})
		if err == nil && result != nil {
			var procResult struct {
				Count int `json:"count"`
			}
			if json.Unmarshal(result, &procResult) == nil && procResult.Count > 0 {
				resp.Running = true
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// trollVNCPlistXML generates a LaunchDaemon plist pointing to the writable
// binary path, so it works on both JB and experimental variants.
const trollVNCPlistXML = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.vphone.trollvnc</string>
	<key>ProgramArguments</key>
	<array>
		<string>/private/var/trollvnc/trollvncserver</string>
		<string>-daemon</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>UserName</key>
	<string>mobile</string>
	<key>GroupName</key>
	<string>mobile</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>DISABLE_TWEAKS</key>
		<string>1</string>
		<key>TROLLVNC_PASSWORD</key>
		<string>alpine</string>
	</dict>
	<key>StandardOutPath</key>
	<string>/tmp/trollvnc-stdout.log</string>
	<key>StandardErrorPath</key>
	<string>/tmp/trollvnc-stderr.log</string>
	<key>ThrottleInterval</key>
	<integer>5</integer>
	<key>ExitTimeOut</key>
	<integer>3</integer>
	<key>HighPriorityIO</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>POSIXSpawnType</key>
	<string>App</string>
	<key>EnableTransactions</key>
	<false/>
	<key>EnablePressuredExit</key>
	<false/>
</dict>
</plist>`

// trollVNCInstall handles POST /api/v1/vms/:id/vnc-server/install — installs
// and starts TrollVNC on the guest.
func (s *Server) trollVNCInstall(w http.ResponseWriter, r *http.Request) {
	v, ok := s.runningVM(w, r)
	if !ok {
		return
	}

	client := s.vms.APIClient(v)
	if client == nil {
		writeError(w, http.StatusServiceUnavailable, "VM API not available")
		return
	}

	binary, _, err := loadTrollVNCResources(s.cfg.Paths.VphoneCLI)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load TrollVNC resources: "+err.Error())
		return
	}

	ctx := r.Context()

	// Ensure /private/var/trollvnc/ exists.
	_, _ = client.RPC(ctx, "files.write", map[string]any{
		"path":    "/private/var/trollvnc/.keep",
		"content": "",
	})

	// Upload the binary to a writable location.
	if err := client.UploadFile(ctx, trollVNCBinPath, "755", bytes.NewReader(binary), int64(len(binary))); err != nil {
		writeError(w, http.StatusBadGateway, "failed to upload trollvncserver: "+err.Error())
		return
	}

	// Set executable permission.
	_, _ = client.RPC(ctx, "files.chmod", map[string]string{
		"path": trollVNCBinPath,
		"mode": "755",
	})

	// Write the plist with the correct binary path.
	_, err = client.RPC(ctx, "files.write", map[string]any{
		"path":    trollVNCPlistPath,
		"content": trollVNCPlistXML,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to write trollvnc.plist: "+err.Error())
		return
	}

	// Load and start the daemon.
	_, err = client.RPC(ctx, "services.load", map[string]any{
		"paths": []string{trollVNCPlistPath},
	})
	if err != nil {
		s.log.Warn("services.load failed, trying kickstart", "err", err)
		_, err = client.RPC(ctx, "services.kickstart", map[string]any{
			"label": trollVNCLabel,
			"force": true,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to start TrollVNC: "+err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"port":    5901,
		"message": "TrollVNC installed and started",
	})
}
