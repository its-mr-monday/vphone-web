package mcp

import (
	"encoding/base64"
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
				return s.simpleOK("POST", "/vms/"+id+"/snapshots", "snapshot created")
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
