package main

/*
#include <stdlib.h>
*/
import "C"

import "wepapered/internal/core"

// wepDaemonUp reports (to the C UI loop) whether the daemon's control socket is
// reachable.
//
//export wepDaemonUp
func wepDaemonUp() C.int {
	if core.DaemonReachable() {
		return 1
	}
	return 0
}

// wepDaemonURL returns the current browse-UI URL, asking the daemon for its random
// port over the control socket each call (so it stays correct across daemon
// restarts that pick a new port). Returns an empty string if the daemon is
// unreachable. The C caller owns the returned buffer and must free() it.
//
//export wepDaemonURL
func wepDaemonURL() *C.char {
	port, err := core.DaemonPort()
	if err != nil {
		return C.CString("")
	}
	cfg, _ := core.LoadConfig()
	return C.CString(core.GUIURL(cfg, port))
}
