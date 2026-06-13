package daemon

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Discord Rich Presence over the local IPC socket.
//
// Protocol: each frame is [4-byte LE opcode][4-byte LE length][JSON payload].
// Opcodes: 0=Handshake, 1=Frame, 2=Close, 3=Ping, 4=Pong.
// Handshake with the app's client_id, then SET_ACTIVITY frames. No external
// dependency and no registered art assets are required for text presence.
//
// Discord is entirely optional: if it isn't running the daemon keeps working;
// Run() retries the connection in the background.

const discordClientID = "674702968964120629"

const (
	opHandshake = 0
	opFrame     = 1
	opClose     = 2
)

type DiscordRP struct {
	clientID string
	mu       sync.Mutex
	conn     net.Conn
	start    int64 // presence "elapsed" start, unix seconds

	// last-known desired presence, re-sent after a (re)connect.
	details string
	state   string
}

func newDiscordRP() *DiscordRP {
	return &DiscordRP{clientID: discordClientID, start: time.Now().Unix()}
}

// candidateSockets lists the IPC socket paths Discord may expose, covering
// native, Flatpak and Snap installs across discord-ipc-0..9.
func candidateSockets() []string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = fmt.Sprintf("/run/user/%d", sessionUID())
	}
	dirs := []string{
		base,
		filepath.Join(base, "app/com.discordapp.Discord"),
		filepath.Join(base, "snap.discord"),
		"/tmp",
	}
	var paths []string
	for _, d := range dirs {
		for i := 0; i < 10; i++ {
			paths = append(paths, filepath.Join(d, fmt.Sprintf("discord-ipc-%d", i)))
		}
	}
	return paths
}

func (d *DiscordRP) dial() (net.Conn, error) {
	var lastErr error
	for _, p := range candidateSockets() {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		conn, err := net.DialTimeout("unix", p, 2*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		return conn, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no discord-ipc socket found")
	}
	return nil, lastErr
}

func writeFrame(conn net.Conn, op int32, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	hdr := make([]byte, 8)
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(op))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(data)))
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(append(hdr, data...)); err != nil {
		return err
	}
	return nil
}

// readFrame consumes one reply frame (used after handshake / set-activity so the
// socket buffer doesn't fill). Errors are non-fatal to the caller.
func readFrame(conn net.Conn) error {
	hdr := make([]byte, 8)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := readFull(conn, hdr); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(hdr[4:8])
	if n == 0 {
		return nil
	}
	buf := make([]byte, n)
	_, err := readFull(conn, buf)
	return err
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		n, err := conn.Read(buf[got:])
		if n > 0 {
			got += n
		}
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

// Run connects (with retry) and keeps the presence alive across reconnects.
// Safe to call once in a goroutine.
func (d *DiscordRP) Run() {
	for {
		conn, err := d.dial()
		if err != nil {
			time.Sleep(30 * time.Second)
			continue
		}
		// Handshake.
		if err := writeFrame(conn, opHandshake, map[string]interface{}{
			"v": 1, "client_id": d.clientID,
		}); err != nil {
			conn.Close()
			time.Sleep(30 * time.Second)
			continue
		}
		if err := readFrame(conn); err != nil {
			conn.Close()
			time.Sleep(30 * time.Second)
			continue
		}
		log.Printf("[discord] connected")

		d.mu.Lock()
		d.conn = conn
		d.mu.Unlock()

		// Push current presence immediately.
		d.push()

		// Keep the connection healthy; Discord closes idle sockets otherwise.
		for {
			time.Sleep(15 * time.Second)
			d.mu.Lock()
			c := d.conn
			d.mu.Unlock()
			if c == nil {
				break
			}
			if err := d.push(); err != nil {
				break
			}
		}

		d.mu.Lock()
		if d.conn != nil {
			d.conn.Close()
			d.conn = nil
		}
		d.mu.Unlock()
		log.Printf("[discord] disconnected, will retry")
		time.Sleep(10 * time.Second)
	}
}

// SetActivity updates the desired presence text and pushes it if connected.
func (d *DiscordRP) SetActivity(details, state string) {
	d.mu.Lock()
	d.details = details
	d.state = state
	d.mu.Unlock()
	d.push()
}

func (d *DiscordRP) push() error {
	d.mu.Lock()
	conn := d.conn
	details, state, start := d.details, d.state, d.start
	d.mu.Unlock()
	if conn == nil {
		return nil
	}

	activity := map[string]interface{}{
		"assets": map[string]interface{}{
			"large_image": "logo",
			"large_text":  "wepapered",
		},
		"timestamps": map[string]interface{}{"start": start},
	}
	if details != "" {
		activity["details"] = details
	}
	if state != "" {
		activity["state"] = state
	}

	payload := map[string]interface{}{
		"cmd": "SET_ACTIVITY",
		"args": map[string]interface{}{
			"pid":      os.Getpid(),
			"activity": activity,
		},
		"nonce": fmt.Sprintf("%d", time.Now().UnixNano()),
	}
	if err := writeFrame(conn, opFrame, payload); err != nil {
		d.mu.Lock()
		d.conn = nil
		d.mu.Unlock()
		return err
	}
	// Drain the reply (best-effort).
	_ = readFrame(conn)
	return nil
}

func (d *DiscordRP) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		writeFrame(d.conn, opClose, map[string]interface{}{})
		d.conn.Close()
		d.conn = nil
	}
}
