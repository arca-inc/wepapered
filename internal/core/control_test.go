package core

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeDaemon serves the control protocol on ln until it's closed, mimicking
// WSServer.handleControlConn (PORT → port, RELOAD → OK).
func fakeDaemon(ln net.Listener, port int) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			line, _ := bufio.NewReader(c).ReadString('\n')
			switch strings.TrimSpace(line) {
			case "PORT":
				fmt.Fprintf(c, "%d\n", port)
			case "RELOAD":
				fmt.Fprintln(c, "OK")
			default:
				fmt.Fprintln(c, "ERR unknown command")
			}
		}(c)
	}
}

func TestControlRendezvous(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	if DaemonReachable() {
		t.Fatal("no daemon yet, but DaemonReachable() == true")
	}
	if _, err := DaemonPort(); err == nil {
		t.Fatal("DaemonPort() should error with no daemon")
	}

	ln, err := AcquireControlSocket()
	if err != nil {
		t.Fatalf("AcquireControlSocket: %v", err)
	}
	go fakeDaemon(ln, 54321)

	if !DaemonReachable() {
		t.Fatal("daemon up, but DaemonReachable() == false")
	}
	if p, err := DaemonPort(); err != nil || p != 54321 {
		t.Fatalf("DaemonPort() = %d, %v; want 54321, nil", p, err)
	}
	if err := ReloadDaemon(); err != nil {
		t.Fatalf("ReloadDaemon: %v", err)
	}

	// Single-instance gate: a second acquire must be refused while the first is live.
	if _, err := AcquireControlSocket(); err != ErrDaemonRunning {
		t.Fatalf("second AcquireControlSocket = %v; want ErrDaemonRunning", err)
	}

	// After the daemon dies, the (stale) socket file must not block a fresh bind.
	ln.Close()
	if DaemonReachable() {
		t.Fatal("daemon closed, but DaemonReachable() == true")
	}
	ln2, err := AcquireControlSocket()
	if err != nil {
		t.Fatalf("re-acquire after stale socket: %v", err)
	}
	ln2.Close()
}

// TestAcquireControlSocketConcurrent is the regression guard for the single-
// instance gate's atomicity: even with a stale leftover at the socket path and
// many daemons racing to acquire at once, exactly one must win. (The pre-flock
// remove+listen logic could let several win.)
func TestAcquireControlSocketConcurrent(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	// Simulate a crashed daemon's leftover occupying the socket path.
	if f, err := os.Create(ControlSocketPath()); err == nil {
		f.Close()
	}

	const N = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners []net.Listener
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if ln, err := AcquireControlSocket(); err == nil {
				mu.Lock()
				winners = append(winners, ln)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(winners) != 1 {
		for _, ln := range winners {
			ln.Close()
		}
		t.Fatalf("got %d gate winners; want exactly 1", len(winners))
	}
	winners[0].Close()
}

func TestListenRandomPort(t *testing.T) {
	ln, port, err := ListenRandomPort()
	if err != nil {
		t.Fatalf("ListenRandomPort: %v", err)
	}
	defer ln.Close()
	if port < PortRangeLo || port >= PortRangeHi {
		t.Fatalf("port %d out of range [%d,%d)", port, PortRangeLo, PortRangeHi)
	}
	if _, p, _ := net.SplitHostPort(ln.Addr().String()); p != fmt.Sprint(port) {
		t.Fatalf("listener port %s != returned port %d", p, port)
	}
}
