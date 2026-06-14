package core

import (
	"fmt"
	"net/http"
	"time"
)

// ControlAddr is the daemon's local control + browse-UI server address. The daemon
// binds it as a single-instance gate; clients (wepaperedctl, settings) post to it.
const ControlAddr = "127.0.0.1:9001"

// ReloadDaemon asks a running daemon to re-read its config and relaunch the renderers
// so changes take effect immediately. Returns an error if no daemon is reachable
// (e.g. it isn't running) or the reload failed — callers may treat that as benign.
func ReloadDaemon() error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://"+ControlAddr+"/reload", "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon reload failed: %s", resp.Status)
	}
	return nil
}
