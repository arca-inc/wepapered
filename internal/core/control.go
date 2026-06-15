package core

import (
	"bufio"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The browse UI / WebSocket server binds a random loopback TCP port in this range
// each time the daemon starts (instead of a fixed, guessable port). Clients never
// hardcode it — they ask the daemon over the control socket (DaemonPort).
const (
	PortRangeLo = 50000
	PortRangeHi = 60000
)

// ErrDaemonRunning is returned by AcquireControlSocket when another daemon already
// owns the control socket (used as the single-instance gate).
var ErrDaemonRunning = errors.New("daemon already running")

// ControlSocketPath is the daemon's Unix control socket: the stable rendezvous
// every client uses to find the (random) UI port and to request reloads. Prefers
// $XDG_RUNTIME_DIR (per-user, auto-cleaned on logout); falls back to a uid-scoped
// path in the temp dir.
func ControlSocketPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "wepapered.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("wepapered-%d.sock", os.Getuid()))
}

// AcquireControlSocket binds the control socket, acting as the single-instance
// gate. The returned listener releases the lock and unlinks the socket on Close.
//
// The gate itself is a kernel-atomic advisory lock (flock) on a sidecar .lock
// file, NOT the socket bind: flock LOCK_EX|LOCK_NB has exactly one winner even
// under a simultaneous double-launch, and the kernel releases it automatically
// when the holder dies (including SIGKILL/OOM) — so there is no stale-lock
// problem. Only the lock winner then clears a possibly-stale socket file and
// binds it. This closes the TOCTOU where two daemons racing the stale-file
// os.Remove+Listen could both end up bound.
func AcquireControlSocket() (net.Listener, error) {
	path := ControlSocketPath()
	lf, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lf.Close()
		return nil, ErrDaemonRunning // another daemon holds the lease
	}
	// We hold the lease: safe to drop a stale socket left by a crash and bind.
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		lf.Close()
		return nil, err
	}
	return &lockedListener{Listener: ln, lock: lf}, nil
}

// lockedListener ties the flock lease's lifetime to the control listener, so the
// single-instance gate reopens only when the listener is closed (or the process
// dies and the kernel drops the lock).
type lockedListener struct {
	net.Listener
	lock *os.File
}

func (l *lockedListener) Close() error {
	err := l.Listener.Close() // unlinks the .sock
	syscall.Flock(int(l.lock.Fd()), syscall.LOCK_UN)
	l.lock.Close()
	return err
}

// ListenRandomPort binds a loopback TCP listener on a random free port in
// [PortRangeLo, PortRangeHi). Retries on collision; returns the listener and the
// chosen port so the caller can advertise it over the control socket.
func ListenRandomPort() (net.Listener, int, error) {
	span := PortRangeHi - PortRangeLo
	for i := 0; i < 200; i++ {
		p := PortRangeLo + rand.Intn(span)
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			return ln, p, nil
		}
	}
	return nil, 0, fmt.Errorf("no free TCP port found in [%d,%d)", PortRangeLo, PortRangeHi)
}

// controlRequest sends a one-line command to the daemon's control socket and
// returns the trimmed one-line reply. Returns an error if no daemon is reachable.
func controlRequest(cmd string) (string, error) {
	c, err := net.DialTimeout("unix", ControlSocketPath(), 2*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(c, "%s\n", cmd); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// DaemonReachable reports whether a daemon is listening on the control socket.
func DaemonReachable() bool {
	c, err := net.DialTimeout("unix", ControlSocketPath(), 2*time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// DaemonPort asks the running daemon which TCP port it serves the browse UI /
// WebSocket on. Returns an error if no daemon is reachable or the reply is invalid.
func DaemonPort() (int, error) {
	reply, err := controlRequest("PORT")
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(reply)
	if err != nil {
		return 0, fmt.Errorf("daemon returned invalid port %q", reply)
	}
	return port, nil
}

// ReloadDaemon asks a running daemon to re-read its config and relaunch the renderers
// so changes take effect immediately. Returns an error if no daemon is reachable
// (e.g. it isn't running) or the reload failed — callers may treat that as benign.
func ReloadDaemon() error {
	reply, err := controlRequest("RELOAD")
	if err != nil {
		return err
	}
	if reply != "OK" {
		return fmt.Errorf("daemon reload failed: %s", reply)
	}
	return nil
}
