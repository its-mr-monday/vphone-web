package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// --- schema helpers --------------------------------------------------------

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func numProp(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func vmIDProp() map[string]any { return strProp("VM id (from vphone_list_vms)") }

// simpleOK runs a request that returns a status payload and reports it.
func (s *Server) simpleOK(method, path, okMsg string) toolResult {
	var out any
	if err := s.call(method, path, nil, &out); err != nil {
		return errResult(err)
	}
	if out == nil {
		return textResult("%s", okMsg)
	}
	return jsonResult(out)
}

// registerTools wires the MCP tool surface. Tools are grouped as: fleet
// management, device interaction (the agent's see/act loop), guest shell, and
// snapshots/jobs.
func (s *Server) registerTools() {
	s.tools = []tool{
		// --- fleet ---------------------------------------------------------
		{
			Name:        "vphone_list_vms",
			Description: "List all virtual iPhones with their id, name, status, iOS version, variant and forwarded ports. Start here to find a vm_id.",
			InputSchema: schema(map[string]any{}),
			handler: func(map[string]any) toolResult {
				var out any
				if err := s.call("GET", "/vms", nil, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_get_vm",
			Description: "Get full detail for one VM (status, resources, ports, paths).",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				var out any
				if err := s.call("GET", "/vms/"+id, nil, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_boot_vm",
			Description: "Boot a stopped VM. Booting takes time; the guest's VNC/SSH/control services come up shortly after the VM reports RUNNING.",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				return s.simpleOK("POST", "/vms/"+id+"/boot", "boot requested")
			},
		},
		{
			Name:        "vphone_stop_vm",
			Description: "Stop a running VM (graceful shutdown, then terminate).",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				return s.simpleOK("POST", "/vms/"+id+"/stop", "stop requested")
			},
		},
		{
			Name:        "vphone_restart_vm",
			Description: "Restart a VM (stop then boot).",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				return s.simpleOK("POST", "/vms/"+id+"/restart", "restart requested")
			},
		},
		{
			Name:        "vphone_system_status",
			Description: "Host status: CPU, memory, disk usage and running VM count.",
			InputSchema: schema(map[string]any{}),
			handler: func(map[string]any) toolResult {
				var out any
				if err := s.call("GET", "/system/status", nil, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_create_vm",
			Description: "Create a new virtual iPhone. Use vphone_list_ipsws first to find an ipsw_id. Variant: 'standard' (stock), 'experimental' (jailbroken with tooling), 'minimal' (stripped).",
			InputSchema: schema(map[string]any{
				"name":      strProp("VM display name"),
				"ipsw_id":   strProp("IPSW id from vphone_list_ipsws"),
				"variant":   map[string]any{"type": "string", "description": "firmware variant", "enum": []string{"standard", "experimental", "minimal"}},
				"cpu":       intProp("CPU cores (default 4)"),
				"memory":    intProp("RAM in MB (default 4096)"),
				"disk_size": intProp("disk in MB (default 32384)"),
			}, "name", "ipsw_id", "variant"),
			handler: func(a map[string]any) toolResult {
				body := map[string]any{
					"name":    argStr(a, "name"),
					"ipsw_id": argStr(a, "ipsw_id"),
					"variant": argStr(a, "variant"),
				}
				if v := argInt(a, "cpu"); v > 0 {
					body["cpu"] = v
				}
				if v := argInt(a, "memory"); v > 0 {
					body["memory"] = v
				}
				if v := argInt(a, "disk_size"); v > 0 {
					body["disk_size"] = v
				}
				var out any
				if err := s.call("POST", "/vms", body, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_list_ipsws",
			Description: "List available IPSW firmware images in the library (id, iOS version, device model, status).",
			InputSchema: schema(map[string]any{}),
			handler: func(map[string]any) toolResult {
				var out any
				if err := s.call("GET", "/ipsws", nil, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},

		// --- device interaction (see + act) ---------------------------------
		{
			Name:        "vphone_screenshot",
			Description: "Capture the guest screen and return it as an image. Use this to SEE the phone before deciding where to tap. The VM must be RUNNING.",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				data, mime, err := s.callRaw("POST", "/vms/"+id+"/screenshot", nil)
				if err != nil {
					return errResult(err)
				}
				if mime == "" {
					mime = "image/png"
				}
				return toolResult{Content: []content{{
					Type:     "image",
					Data:     base64.StdEncoding.EncodeToString(data),
					MimeType: mime,
				}}}
			},
		},
		{
			Name:        "vphone_tap",
			Description: "Tap the guest screen at absolute pixel coordinates (top-left origin). Take a screenshot first to choose coordinates; the default screen is 1290x2796.",
			InputSchema: schema(map[string]any{
				"vm_id": vmIDProp(),
				"x":     numProp("X pixel coordinate"),
				"y":     numProp("Y pixel coordinate"),
			}, "vm_id", "x", "y"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				body := map[string]any{"type": "tap", "x": argFloat(a, "x"), "y": argFloat(a, "y")}
				if err := s.call("POST", "/vms/"+id+"/touch", body, nil); err != nil {
					return errResult(err)
				}
				return textResult("tapped (%.0f, %.0f)", argFloat(a, "x"), argFloat(a, "y"))
			},
		},
		{
			Name:        "vphone_swipe",
			Description: "Swipe on the guest screen from (x1,y1) to (x2,y2). Useful for scrolling, unlocking, and navigating.",
			InputSchema: schema(map[string]any{
				"vm_id":       vmIDProp(),
				"x1":          numProp("start X"),
				"y1":          numProp("start Y"),
				"x2":          numProp("end X"),
				"y2":          numProp("end Y"),
				"duration_ms": intProp("swipe duration in milliseconds (default 300)"),
			}, "vm_id", "x1", "y1", "x2", "y2"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				ms := argInt(a, "duration_ms")
				if ms <= 0 {
					ms = 300
				}
				body := map[string]any{
					"type": "swipe",
					"x":    argFloat(a, "x1"), "y": argFloat(a, "y1"),
					"x2": argFloat(a, "x2"), "y2": argFloat(a, "y2"),
					"ms": ms,
				}
				if err := s.call("POST", "/vms/"+id+"/touch", body, nil); err != nil {
					return errResult(err)
				}
				return textResult("swiped (%.0f,%.0f) → (%.0f,%.0f) over %dms",
					argFloat(a, "x1"), argFloat(a, "y1"), argFloat(a, "x2"), argFloat(a, "y2"), ms)
			},
		},
		{
			Name:        "vphone_press_key",
			Description: "Press a hardware key on the device: home, lock (side/power button), volume_up, or volume_down.",
			InputSchema: schema(map[string]any{
				"vm_id": vmIDProp(),
				"key": map[string]any{
					"type":        "string",
					"description": "which hardware key to press",
					"enum":        []string{"home", "lock", "volume_up", "volume_down"},
				},
			}, "vm_id", "key"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				key := argStr(a, "key")
				if key == "" {
					return errResult(fmt.Errorf("key is required (home, lock, volume_up, volume_down)"))
				}
				if err := s.call("POST", "/vms/"+id+"/key", map[string]any{"key": key}, nil); err != nil {
					return errResult(err)
				}
				return textResult("pressed %s", key)
			},
		},
		{
			Name:        "vphone_device_info",
			Description: "Guest details reported over the control channel: connection state, IP address, iOS version and device name.",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				var out any
				if err := s.call("GET", "/vms/"+id+"/info", nil, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name: "vphone_scroll",
			Description: "Scroll at a screen position. Coordinates (x, y) are normalised 0–1 from the top-left corner. " +
				"dy is vertical scroll amount (positive = scroll up / content moves down, negative = scroll down). " +
				"dx is horizontal scroll amount.",
			InputSchema: schema(map[string]any{
				"vm_id": vmIDProp(),
				"x":     numProp("normalised X position 0–1"),
				"y":     numProp("normalised Y position 0–1"),
				"dx":    numProp("horizontal scroll pixels (default 0)"),
				"dy":    numProp("vertical scroll pixels (positive=up, negative=down)"),
			}, "vm_id", "x", "y", "dy"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				x, y := argFloat(a, "x"), argFloat(a, "y")
				dx, dy := argFloat(a, "dx"), argFloat(a, "dy")
				// began
				if err := s.call("POST", "/vms/"+id+"/rpc", map[string]any{
					"method": "input.scroll",
					"params": map[string]any{"phase": "began", "dx": 0, "dy": 0, "x": x, "y": y},
				}, nil); err != nil {
					return errResult(err)
				}
				// changed
				if err := s.call("POST", "/vms/"+id+"/rpc", map[string]any{
					"method": "input.scroll",
					"params": map[string]any{"phase": "changed", "dx": dx, "dy": dy},
				}, nil); err != nil {
					return errResult(err)
				}
				// ended
				if err := s.call("POST", "/vms/"+id+"/rpc", map[string]any{
					"method": "input.scroll",
					"params": map[string]any{"phase": "ended", "dx": 0, "dy": 0},
				}, nil); err != nil {
					return errResult(err)
				}
				return textResult("scrolled at (%.2f, %.2f) dx=%.0f dy=%.0f", x, y, dx, dy)
			},
		},
		{
			Name:        "vphone_type_text",
			Description: "Paste text into the currently focused text field on the guest. Sets the guest clipboard and triggers a paste action.",
			InputSchema: schema(map[string]any{
				"vm_id": vmIDProp(),
				"text":  strProp("text to type/paste"),
			}, "vm_id", "text"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				text := argStr(a, "text")
				if text == "" {
					return errResult(fmt.Errorf("text is required"))
				}
				if err := s.call("POST", "/vms/"+id+"/rpc", map[string]any{
					"method": "input.paste",
					"params": map[string]any{"text": text},
				}, nil); err != nil {
					return errResult(err)
				}
				return textResult("pasted %d chars", len(text))
			},
		},

		// --- guest shell ----------------------------------------------------
		{
			Name: "vphone_exec",
			Description: "Run a shell command on the guest over SSH and return stdout, stderr and exit code. " +
				"This is the main tool for research: read crash logs (/var/mobile/Library/Logs/CrashReporter), " +
				"inspect the filesystem, list processes, and drive on-device tooling.",
			InputSchema: schema(map[string]any{
				"vm_id":           vmIDProp(),
				"command":         strProp("shell command to run on the device"),
				"timeout_seconds": intProp("max seconds to wait (default 30)"),
			}, "vm_id", "command"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				cmd := argStr(a, "command")
				if cmd == "" {
					return errResult(fmt.Errorf("command is required"))
				}
				body := map[string]any{"command": cmd}
				if t := argInt(a, "timeout_seconds"); t > 0 {
					body["timeout_seconds"] = t
				}
				var out struct {
					Stdout   string `json:"stdout"`
					Stderr   string `json:"stderr"`
					ExitCode int    `json:"exit_code"`
				}
				if err := s.call("POST", "/vms/"+id+"/exec", body, &out); err != nil {
					return errResult(err)
				}
				text := out.Stdout
				if out.Stderr != "" {
					text += "\n--- stderr ---\n" + out.Stderr
				}
				if out.ExitCode != 0 {
					text += fmt.Sprintf("\n--- exit code: %d ---", out.ExitCode)
				}
				if text == "" {
					text = "(no output)"
				}
				return textResult("%s", text)
			},
		},
		{
			Name:        "vphone_frida_processes",
			Description: "List applications/processes on the guest via frida-ps (requires frida-server running on the device).",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				var out any
				if err := s.call("GET", "/vms/"+id+"/frida/processes", nil, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},

		// --- guest filesystem -----------------------------------------------
		{
			Name: "vphone_pull_file",
			Description: "Read a file from the guest filesystem. Returns the file contents as text (or base64 if binary=true). " +
				"Useful for pulling crash logs, app plists, binary analysis targets, etc.",
			InputSchema: schema(map[string]any{
				"vm_id":  vmIDProp(),
				"path":   strProp("absolute path on the guest (e.g. /var/mobile/Library/Logs/CrashReporter)"),
				"binary": boolProp("return base64-encoded data instead of text (for binaries)"),
			}, "vm_id", "path"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				p := argStr(a, "path")
				if p == "" {
					return errResult(fmt.Errorf("path is required"))
				}
				params := map[string]any{"path": p}
				if b, ok := a["binary"].(bool); ok && b {
					params["binary"] = true
				}
				var out any
				if err := s.call("POST", "/vms/"+id+"/rpc", map[string]any{
					"method": "files.read",
					"params": params,
				}, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_push_file",
			Description: "Write content to a file on the guest filesystem. Optionally set the file mode (e.g. '755' for executables).",
			InputSchema: schema(map[string]any{
				"vm_id":   vmIDProp(),
				"path":    strProp("absolute destination path on the guest"),
				"content": strProp("file content to write"),
				"mode":    strProp("file mode (e.g. '755', '644') — optional"),
			}, "vm_id", "path", "content"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				p := argStr(a, "path")
				if p == "" {
					return errResult(fmt.Errorf("path is required"))
				}
				content := argStr(a, "content")
				if err := s.call("POST", "/vms/"+id+"/rpc", map[string]any{
					"method": "files.write",
					"params": map[string]any{"path": p, "content": content},
				}, nil); err != nil {
					return errResult(err)
				}
				if mode := argStr(a, "mode"); mode != "" {
					_ = s.call("POST", "/vms/"+id+"/rpc", map[string]any{
						"method": "files.chmod",
						"params": map[string]any{"path": p, "mode": mode},
					}, nil)
				}
				return textResult("wrote %d bytes to %s", len(content), p)
			},
		},

		// --- guest services -------------------------------------------------
		{
			Name: "vphone_rpc",
			Description: "Call any vphoned RPC method directly — the power-user escape hatch. " +
				"Common methods: files.read, files.write, files.chmod, files.stat, files.list, " +
				"processes.list, processes.kill, services.list, services.load, services.kickstart, " +
				"clipboard.get, clipboard.set, sysctl.get, input.paste, input.scroll.",
			InputSchema: schema(map[string]any{
				"vm_id":  vmIDProp(),
				"method": strProp("RPC method name (e.g. 'files.list', 'processes.list')"),
				"params": strProp("JSON object of method params (e.g. '{\"path\":\"/var\"}') — optional"),
			}, "vm_id", "method"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				method := argStr(a, "method")
				if method == "" {
					return errResult(fmt.Errorf("method is required"))
				}
				var params any
				if raw := argStr(a, "params"); raw != "" {
					if err := json.Unmarshal([]byte(raw), &params); err != nil {
						return errResult(fmt.Errorf("params must be valid JSON: %w", err))
					}
				} else {
					params = map[string]any{}
				}
				var out any
				if err := s.call("POST", "/vms/"+id+"/rpc", map[string]any{
					"method": method,
					"params": params,
				}, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_clipboard_get",
			Description: "Read the guest device's clipboard text.",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				var out any
				if err := s.call("POST", "/vms/"+id+"/rpc", map[string]any{
					"method": "clipboard.get",
					"params": map[string]any{},
				}, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_clipboard_set",
			Description: "Set the guest device's clipboard to the given text.",
			InputSchema: schema(map[string]any{
				"vm_id": vmIDProp(),
				"text":  strProp("text to place on the clipboard"),
			}, "vm_id", "text"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				text := argStr(a, "text")
				if text == "" {
					return errResult(fmt.Errorf("text is required"))
				}
				if err := s.call("POST", "/vms/"+id+"/rpc", map[string]any{
					"method": "clipboard.set",
					"params": map[string]any{"text": text},
				}, nil); err != nil {
					return errResult(err)
				}
				return textResult("clipboard set (%d chars)", len(text))
			},
		},
		{
			Name:        "vphone_vnc_status",
			Description: "Check whether TrollVNC is installed and running on the guest. Returns {installed, running, port}.",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				var out any
				if err := s.call("GET", "/vms/"+id+"/vnc-server", nil, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_vnc_install",
			Description: "Install and start TrollVNC on the guest for native VNC display. Deploys the server binary and LaunchDaemon.",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				return s.simpleOK("POST", "/vms/"+id+"/vnc-server/install", "TrollVNC installed and started")
			},
		},
		{
			Name:        "vphone_frida_status",
			Description: "Get Frida server status: installed, running, version, and listening port.",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				var out any
				if err := s.call("GET", "/vms/"+id+"/frida", nil, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_frida_start",
			Description: "Start (or restart) the Frida server on the guest. Required before Frida-based instrumentation.",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				return s.simpleOK("POST", "/vms/"+id+"/frida/start", "Frida server started")
			},
		},
		{
			Name:        "vphone_frida_stop",
			Description: "Stop the Frida server on the guest.",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				return s.simpleOK("POST", "/vms/"+id+"/frida/stop", "Frida server stopped")
			},
		},

		// --- snapshots & jobs ------------------------------------------------
		{
			Name:        "vphone_list_snapshots",
			Description: "List snapshots for a VM. Snapshot before risky testing so you can roll back a corrupted device state.",
			InputSchema: schema(map[string]any{"vm_id": vmIDProp()}, "vm_id"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				var out any
				if err := s.call("GET", "/vms/"+id+"/snapshots", nil, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_create_snapshot",
			Description: "Create a named snapshot of a VM. The VM must be STOPPED.",
			InputSchema: schema(map[string]any{
				"vm_id": vmIDProp(),
				"name":  strProp("snapshot name"),
			}, "vm_id", "name"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				name := argStr(a, "name")
				if name == "" {
					return errResult(fmt.Errorf("name is required"))
				}
				var out any
				if err := s.call("POST", "/vms/"+id+"/snapshots", map[string]string{"name": name}, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_restore_snapshot",
			Description: "Restore a VM to a named snapshot, discarding current device state. The VM must be STOPPED.",
			InputSchema: schema(map[string]any{
				"vm_id": vmIDProp(),
				"name":  strProp("snapshot name to restore"),
			}, "vm_id", "name"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				name := argStr(a, "name")
				if name == "" {
					return errResult(fmt.Errorf("name is required"))
				}
				return s.simpleOK("POST", "/vms/"+id+"/snapshots/"+name+"/restore", "snapshot restored")
			},
		},
		{
			Name:        "vphone_delete_snapshot",
			Description: "Delete a named snapshot. The VM must be STOPPED.",
			InputSchema: schema(map[string]any{
				"vm_id": vmIDProp(),
				"name":  strProp("snapshot name to delete"),
			}, "vm_id", "name"),
			handler: func(a map[string]any) toolResult {
				id, err := requireVM(a)
				if err != nil {
					return errResult(err)
				}
				name := argStr(a, "name")
				if name == "" {
					return errResult(fmt.Errorf("name is required"))
				}
				return s.simpleOK("DELETE", "/vms/"+id+"/snapshots/"+name, "snapshot deleted")
			},
		},
		{
			Name:        "vphone_list_jobs",
			Description: "List background jobs (provisioning, downloads, installs) with status.",
			InputSchema: schema(map[string]any{}),
			handler: func(map[string]any) toolResult {
				var out any
				if err := s.call("GET", "/jobs", nil, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "vphone_get_job",
			Description: "Get one job's status and captured output — use this to see why provisioning failed.",
			InputSchema: schema(map[string]any{"job_id": strProp("job id")}, "job_id"),
			handler: func(a map[string]any) toolResult {
				id := argStr(a, "job_id")
				if id == "" {
					return errResult(fmt.Errorf("job_id is required"))
				}
				var out any
				if err := s.call("GET", "/jobs/"+id, nil, &out); err != nil {
					return errResult(err)
				}
				return jsonResult(out)
			},
		},
	}
}
