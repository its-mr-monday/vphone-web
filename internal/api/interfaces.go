package api

import (
	"net"
	"net/http"
	"os/exec"
	"strings"
)

// hostInterface describes a host network interface for the bridged-networking
// picker in the UI.
type hostInterface struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"`  // "Wi-Fi" | "Ethernet" | "Thunderbolt" | ...
	Wired bool     `json:"wired"` // bridged networking only works over wired links
	Addrs []string `json:"addrs"`
}

// systemInterfacesHandler handles GET /api/v1/system/interfaces — the list of
// host interfaces (name, type, IPv4 addresses) so the create wizard can offer a
// bridge-interface picker and flag Wi-Fi (which can't be bridged).
func (s *Server) systemInterfacesHandler(w http.ResponseWriter, r *http.Request) {
	types := hardwarePortTypes()

	ifaces, _ := net.Interfaces()
	out := make([]hostInterface, 0)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		var v4 []string
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				v4 = append(v4, ipnet.IP.String())
			}
		}
		if len(v4) == 0 {
			continue // only offer interfaces with an IPv4 address
		}
		portType := types[iface.Name]
		if portType == "" {
			portType = "Unknown"
		}
		out = append(out, hostInterface{
			Name:  iface.Name,
			Type:  portType,
			Wired: portType != "Wi-Fi" && portType != "Unknown",
			Addrs: v4,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// hardwarePortTypes maps a BSD device name (en0) to its macOS hardware-port
// type via `networksetup -listallhardwareports`.
func hardwarePortTypes() map[string]string {
	m := map[string]string{}
	out, err := exec.Command("networksetup", "-listallhardwareports").Output()
	if err != nil {
		return m
	}
	var currentPort string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Hardware Port:") {
			currentPort = strings.TrimSpace(strings.TrimPrefix(line, "Hardware Port:"))
		} else if strings.HasPrefix(line, "Device:") {
			dev := strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
			if dev != "" {
				m[dev] = normalizePortType(currentPort)
			}
		}
	}
	return m
}

func normalizePortType(p string) string {
	switch {
	case strings.Contains(p, "Wi-Fi"), strings.Contains(p, "AirPort"):
		return "Wi-Fi"
	case strings.Contains(p, "Thunderbolt"):
		return "Thunderbolt"
	case strings.Contains(p, "Ethernet"), strings.Contains(p, "LAN"):
		return "Ethernet"
	default:
		return p
	}
}
