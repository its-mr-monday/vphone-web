package proxy

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
)

// TCPForward listens on a local TCP port and bridges each connection to a
// guest port via the vphoned WebSocket port tunnel (/v1/ports/<guestPort>).
// This lets host tools like frida-ps that need raw TCP reach guest services.
type TCPForward struct {
	listener   net.Listener
	apiBase    string
	apiToken   string
	guestPort  int
	log        *slog.Logger
	closeOnce  sync.Once
	done       chan struct{}
}

// NewTCPForward starts a TCP listener on listenAddr (e.g. "127.0.0.1:10004")
// that forwards each connection to guestPort on the VM accessible at apiBase.
func NewTCPForward(listenAddr, apiBase, apiToken string, guestPort int, log *slog.Logger) (*TCPForward, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", listenAddr, err)
	}
	f := &TCPForward{
		listener:  ln,
		apiBase:   apiBase,
		apiToken:  apiToken,
		guestPort: guestPort,
		log:       log,
		done:      make(chan struct{}),
	}
	go f.acceptLoop()
	log.Info("tcp forward started", "listen", listenAddr, "guest_port", guestPort)
	return f, nil
}

// Addr returns the listener's address.
func (f *TCPForward) Addr() net.Addr { return f.listener.Addr() }

// Close stops the forwarder.
func (f *TCPForward) Close() error {
	f.closeOnce.Do(func() {
		close(f.done)
		f.listener.Close()
	})
	return nil
}

func (f *TCPForward) acceptLoop() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			select {
			case <-f.done:
				return
			default:
				f.log.Debug("tcp forward accept error", "err", err)
				return
			}
		}
		go f.handle(conn)
	}
}

func (f *TCPForward) handle(tcp net.Conn) {
	defer tcp.Close()

	path := fmt.Sprintf("/v1/ports/%d?token=%s", f.guestPort, url.QueryEscape(f.apiToken))
	ws, err := dialUpstreamWS(f.apiBase, path, f.apiToken, f.log)
	if err != nil {
		f.log.Warn("tcp forward: upstream ws dial failed", "guest_port", f.guestPort, "err", err)
		return
	}
	defer ws.Close()

	f.log.Debug("tcp forward connected", "client", tcp.RemoteAddr(), "guest_port", f.guestPort)

	var wg sync.WaitGroup
	wg.Add(2)

	// TCP → WebSocket
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := tcp.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// WebSocket → TCP
	go func() {
		defer wg.Done()
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if _, werr := tcp.Write(data); werr != nil {
				return
			}
		}
	}()

	// When either direction ends, close both.
	go func() {
		wg.Wait()
		tcp.Close()
		ws.Close()
	}()

	// Wait for done signal or connection end.
	select {
	case <-f.done:
		tcp.Close()
		ws.Close()
	case <-waitGroup(&wg):
	}
}

func waitGroup(wg *sync.WaitGroup) <-chan struct{} {
	ch := make(chan struct{})
	go func() { wg.Wait(); close(ch) }()
	return ch
}

// PortForwarder manages multiple TCP forwards for a VM, starting and stopping
// them as services come up.
type PortForwarder struct {
	mu       sync.Mutex
	forwards map[string]*TCPForward // keyed by "host:port"
	log      *slog.Logger
}

// NewPortForwarder creates a forwarder manager.
func NewPortForwarder(log *slog.Logger) *PortForwarder {
	return &PortForwarder{
		forwards: make(map[string]*TCPForward),
		log:      log,
	}
}

// Start begins forwarding from listenAddr to guestPort through the VM's API.
// If a forward already exists for listenAddr, it is stopped first.
func (pf *PortForwarder) Start(listenAddr, apiBase, apiToken string, guestPort int) error {
	pf.mu.Lock()
	defer pf.mu.Unlock()

	if old, ok := pf.forwards[listenAddr]; ok {
		old.Close()
		delete(pf.forwards, listenAddr)
	}

	fwd, err := NewTCPForward(listenAddr, apiBase, apiToken, guestPort, pf.log)
	if err != nil {
		return err
	}
	pf.forwards[listenAddr] = fwd
	return nil
}

// Stop closes a specific forward.
func (pf *PortForwarder) Stop(listenAddr string) {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	if fwd, ok := pf.forwards[listenAddr]; ok {
		fwd.Close()
		delete(pf.forwards, listenAddr)
	}
}

// StopAll closes all forwards.
func (pf *PortForwarder) StopAll() {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	for addr, fwd := range pf.forwards {
		fwd.Close()
		delete(pf.forwards, addr)
	}
}

// Has reports whether a forward is active for listenAddr.
func (pf *PortForwarder) Has(listenAddr string) bool {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	_, ok := pf.forwards[listenAddr]
	return ok
}

// ignore unused import warning
var _ = io.EOF
