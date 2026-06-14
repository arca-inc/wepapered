package daemon

import (
	"log"
	"math/rand"
	"sync"
	"time"
)

// PlaylistEngine drives per-monitor wallpaper rotation. It owns one timer per
// monitor that has an active playlist; on each tick it advances the cursor,
// resolves the current item into the daemon state, and re-renders. The renderer
// never learns about playlists — the engine just keeps mutating
// state.Monitors[label], so the existing diff/hot-swap path applies.
//
// Locking: all daemon-state access happens under WSServer.stateMu (held by the
// engine's *Locked helpers, whose callers take it). The engine's own mutex guards
// ONLY the timer table; it is never held while taking stateMu, so the two never
// nest. A per-monitor generation counter invalidates ticks from a timer that was
// replaced or stopped (a stopped timer's callback may already be in flight).
type PlaylistEngine struct {
	s      *WSServer
	mu     sync.Mutex
	timers map[string]*time.Timer
	gen    map[string]uint64
}

func newPlaylistEngine(s *WSServer) *PlaylistEngine {
	return &PlaylistEngine{
		s:      s,
		timers: make(map[string]*time.Timer),
		gen:    make(map[string]uint64),
	}
}

// SetPlaylist installs (or replaces) the active playlist on a monitor, renders its
// first item, and arms rotation. ≤1 item is not a playlist — the caller handles
// that case as a single wallpaper. Caller must NOT hold stateMu.
func (e *PlaylistEngine) SetPlaylist(monitor string, pl *MonitorPlaylist) {
	if pl == nil || len(pl.Items) == 0 {
		return
	}
	pl.Index = initialIndex(pl, false)

	e.s.stateMu.Lock()
	e.s.state.MonitorPlaylists[monitor] = pl
	e.applyCurrentLocked(monitor, pl)
	e.s.persistLocked()
	snap := e.s.state.snapshot()
	e.s.stateMu.Unlock()

	go e.s.renderer.Apply(snap)
	e.arm(monitor, pl)
	log.Printf("[WE] playlist on %s: %d items, mode=%s order=%s delay=%dm",
		monitor, len(pl.Items), pl.Settings.Mode, pl.Settings.Order, pl.Settings.Delay)
}

// StartAll arms timers and renders the current item for every saved playlist. Used
// at daemon boot. It does NOT call Apply per monitor — run.go applies the whole
// state once after this returns.
func (e *PlaylistEngine) StartAll() {
	e.s.stateMu.Lock()
	mons := make([]string, 0, len(e.s.state.MonitorPlaylists))
	for m := range e.s.state.MonitorPlaylists {
		mons = append(mons, m)
	}
	e.s.stateMu.Unlock()

	for _, m := range mons {
		e.s.stateMu.Lock()
		pl := e.s.state.MonitorPlaylists[m]
		if pl != nil {
			pl.Index = initialIndex(pl, true) // resume timer cursor; re-derive clock modes
			e.applyCurrentLocked(m, pl)
			e.s.persistLocked()
		}
		e.s.stateMu.Unlock()
		if pl != nil {
			e.arm(m, pl)
			log.Printf("[wepapered] resumed playlist on %s (%d items, mode=%s)", m, len(pl.Items), pl.Settings.Mode)
		}
	}
}

// Rearm (re)starts the rotation timer for whatever playlist a monitor currently
// has, or stops the timer if it has none. Used after a transfer re-points
// playlists between monitors. Caller must NOT hold stateMu.
func (e *PlaylistEngine) Rearm(monitor string) {
	e.s.stateMu.Lock()
	pl := e.s.state.MonitorPlaylists[monitor]
	e.s.stateMu.Unlock()
	if pl != nil {
		e.arm(monitor, pl)
	} else {
		e.stopTimer(monitor)
	}
}

// stopTimer stops and forgets a monitor's rotation timer and invalidates any
// in-flight tick. Touches only the timer table (e.mu), so it is safe to call while
// holding stateMu. Does NOT alter daemon state — the caller owns that.
func (e *PlaylistEngine) stopTimer(monitor string) {
	e.mu.Lock()
	e.gen[monitor]++
	if t := e.timers[monitor]; t != nil {
		t.Stop()
		delete(e.timers, monitor)
	}
	e.mu.Unlock()
}

// Stop halts all rotation (daemon shutdown).
func (e *PlaylistEngine) Stop() {
	e.mu.Lock()
	for m, t := range e.timers {
		t.Stop()
		e.gen[m]++
	}
	e.timers = make(map[string]*time.Timer)
	e.mu.Unlock()
}

// arm schedules the next tick for a monitor according to its playlist mode. Modes
// with no rotation (logon/never) just stop the timer.
func (e *PlaylistEngine) arm(monitor string, pl *MonitorPlaylist) {
	d, repeat := nextDelay(pl)
	e.mu.Lock()
	e.gen[monitor]++
	g := e.gen[monitor]
	if t := e.timers[monitor]; t != nil {
		t.Stop()
	}
	if !repeat {
		delete(e.timers, monitor)
		e.mu.Unlock()
		return
	}
	e.timers[monitor] = time.AfterFunc(d, func() { e.onTick(monitor, g) })
	e.mu.Unlock()
}

// onTick advances and re-renders a monitor's playlist, then re-arms. g guards
// against a stale timer (one stopped/replaced after its callback was queued).
func (e *PlaylistEngine) onTick(monitor string, g uint64) {
	e.mu.Lock()
	stale := e.gen[monitor] != g
	e.mu.Unlock()
	if stale {
		return
	}

	e.s.stateMu.Lock()
	pl := e.s.state.MonitorPlaylists[monitor]
	if pl == nil {
		e.s.stateMu.Unlock()
		return
	}
	pl.Index = advanceIndex(pl)
	e.applyCurrentLocked(monitor, pl)
	e.s.persistLocked()
	snap := e.s.state.snapshot()
	e.s.stateMu.Unlock()

	go e.s.renderer.Apply(snap)
	e.arm(monitor, pl)
}

// applyCurrentLocked resolves the playlist's current item into the monitor's
// wallpaper slot. Caller holds stateMu.
func (e *PlaylistEngine) applyCurrentLocked(monitor string, pl *MonitorPlaylist) {
	if pl.Index < 0 || pl.Index >= len(pl.Items) {
		pl.Index = 0
	}
	item := pl.Items[pl.Index]
	mw := e.s.resolveWallpaper(item.File, monitor)
	if mw == nil {
		log.Printf("[wepapered] playlist %s: item %d (%s) unresolved, skipping", monitor, pl.Index, item.File)
		return
	}
	if e.s.state.Layout == layoutClone {
		e.s.cloneToAllOutputs(mw)
	} else {
		e.s.state.Monitors[monitor] = mw
	}
}

// ── index / timing helpers (pure) ──────────────────────────────────────────────

// initialIndex picks the starting item for a playlist. resume=true keeps the saved
// timer cursor (used on boot); resume=false starts fresh (used when the UI sets a
// new playlist). Clock-driven modes always re-derive from the current time.
func initialIndex(pl *MonitorPlaylist, resume bool) int {
	n := len(pl.Items)
	if n == 0 {
		return 0
	}
	switch pl.Settings.Mode {
	case "daytime":
		return daytimeIndex(pl.Items)
	case "dayofweek":
		return int(time.Now().Weekday()) % n
	case "logon":
		if pl.Settings.Order == "random" {
			return rand.Intn(n)
		}
		return 0
	case "never":
		return clampIndex(pl.Index, n)
	default: // timer
		if resume {
			return clampIndex(pl.Index, n)
		}
		return 0
	}
}

// advanceIndex returns the next item index for a tick.
func advanceIndex(pl *MonitorPlaylist) int {
	n := len(pl.Items)
	if n <= 1 {
		return 0
	}
	switch pl.Settings.Mode {
	case "daytime":
		return daytimeIndex(pl.Items)
	case "dayofweek":
		return int(time.Now().Weekday()) % n
	default: // timer
		if pl.Settings.Order == "random" {
			// uniform over the other n-1 items (avoid showing the same one twice)
			j := rand.Intn(n - 1)
			if j >= pl.Index {
				j++
			}
			return j
		}
		return (pl.Index + 1) % n
	}
}

// nextDelay returns the wait until the next tick and whether to keep rotating.
func nextDelay(pl *MonitorPlaylist) (time.Duration, bool) {
	switch pl.Settings.Mode {
	case "daytime":
		return untilNextDaytimeBoundary(pl.Items), true
	case "dayofweek":
		return untilNextMidnight(), true
	case "logon", "never":
		return 0, false
	default: // timer
		m := pl.Settings.Delay
		if m < 1 {
			m = 60 // WE's default; guards a 0/garbage delay against a busy-loop
		}
		return time.Duration(m) * time.Minute, true
	}
}

// dayFraction is the current local time as a fraction of the day (0..1).
func dayFraction(now time.Time) float64 {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return now.Sub(midnight).Seconds() / 86400.0
}

// daytimeIndex returns the active slot for "daytime" mode. Items[0..n-2] carry an
// ascending daytimeend boundary; the last item is open-ended (covers the wrap from
// the final boundary to the first boundary the next day).
func daytimeIndex(items []PlaylistItem) int {
	f := dayFraction(time.Now())
	for i, it := range items {
		if it.DaytimeEnd > 0 && f < it.DaytimeEnd {
			return i
		}
	}
	return len(items) - 1
}

func untilNextDaytimeBoundary(items []PlaylistItem) time.Duration {
	f := dayFraction(time.Now())
	next := -1.0
	for _, it := range items {
		if it.DaytimeEnd > 0 && it.DaytimeEnd > f {
			if next < 0 || it.DaytimeEnd < next {
				next = it.DaytimeEnd
			}
		}
	}
	var frac float64
	if next < 0 {
		// past the last boundary today → first boundary tomorrow
		first := 1.0
		for _, it := range items {
			if it.DaytimeEnd > 0 && it.DaytimeEnd < first {
				first = it.DaytimeEnd
			}
		}
		frac = (1.0 - f) + first
	} else {
		frac = next - f
	}
	d := time.Duration(frac * 86400 * float64(time.Second))
	if d < time.Minute {
		d = time.Minute // never busy-loop on a boundary rounding error
	}
	return d
}

func untilNextMidnight() time.Duration {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	d := next.Sub(now)
	if d < time.Minute {
		d = time.Minute
	}
	return d
}

func clampIndex(i, n int) int {
	if i < 0 || i >= n {
		return 0
	}
	return i
}
