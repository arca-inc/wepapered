package daemon

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

// mediaProxyName is the bus name our proxy MPRIS player owns. LWE is pointed at
// it via LWE_MEDIA_PLAYER so it reads our enriched metadata instead of the bare
// metadata exposed by the real player (which, for Firefox & co, lacks artwork).
const mediaProxyName = "org.mpris.MediaPlayer2.wepapered"
const mediaProxyIdentity = "wepapered"
const mediaProxyPath = "/org/mpris/MediaPlayer2"

// mediaProxy mirrors the active MPRIS player and fills in a missing album-art URL
// (mpris:artUrl) by resolving it from the track's YouTube URL or the iTunes Search
// API, so cover art shows even with players that don't expose artwork.
type mediaProxy struct {
	conn  *dbus.Conn
	props *prop.Properties

	// preferredPlayer mirrors the user's MediaPlayer config (playerctl --player
	// priority list); empty = playerctl's default selection.
	preferredPlayer string

	mu       sync.Mutex
	artCache map[string]string // key: title\x1fartist → resolved artUrl ("" = miss)

	httpc *http.Client
	stop  chan struct{}
}

// mprisMethods is a no-op implementation of the MPRIS method interfaces. playerctl
// (and LWE) only read properties, but the methods must exist for introspection.
type mprisMethods struct{}

func (mprisMethods) Raise() *dbus.Error                 { return nil }
func (mprisMethods) Quit() *dbus.Error                  { return nil }
func (mprisMethods) PlayPause() *dbus.Error             { return nil }
func (mprisMethods) Play() *dbus.Error                  { return nil }
func (mprisMethods) Pause() *dbus.Error                 { return nil }
func (mprisMethods) Next() *dbus.Error                  { return nil }
func (mprisMethods) Previous() *dbus.Error              { return nil }
func (mprisMethods) Stop() *dbus.Error                  { return nil }
func (mprisMethods) Seek(_ int64) *dbus.Error           { return nil }
func (mprisMethods) SetPosition(_ dbus.ObjectPath, _ int64) *dbus.Error { return nil }

// startMediaProxy connects to the session bus, owns the proxy name, exports the
// MPRIS interfaces and starts the polling loop. Returns nil (and logs) on failure;
// the daemon keeps working without album-art enrichment.
func startMediaProxy(preferredPlayer string) *mediaProxy {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Printf("[mediaproxy] cannot connect to session bus: %v", err)
		return nil
	}

	reply, err := conn.RequestName(mediaProxyName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Printf("[mediaproxy] cannot own %s (reply=%d err=%v)", mediaProxyName, reply, err)
		conn.Close()
		return nil
	}

	mp := &mediaProxy{
		conn:            conn,
		preferredPlayer: strings.TrimSpace(preferredPlayer),
		artCache:        map[string]string{},
		httpc:           &http.Client{Timeout: 6 * time.Second},
		stop:            make(chan struct{}),
	}

	methods := mprisMethods{}
	conn.Export(methods, mediaProxyPath, "org.mpris.MediaPlayer2")
	conn.Export(methods, mediaProxyPath, "org.mpris.MediaPlayer2.Player")

	propsSpec := prop.Map{
		"org.mpris.MediaPlayer2": {
			"Identity":     {Value: mediaProxyIdentity, Writable: false, Emit: prop.EmitTrue},
			"CanQuit":      {Value: false, Writable: false, Emit: prop.EmitTrue},
			"CanRaise":     {Value: false, Writable: false, Emit: prop.EmitTrue},
			"HasTrackList": {Value: false, Writable: false, Emit: prop.EmitTrue},
		},
		"org.mpris.MediaPlayer2.Player": {
			"PlaybackStatus": {Value: "Stopped", Writable: false, Emit: prop.EmitTrue},
			"Metadata":       {Value: emptyMetadata(), Writable: false, Emit: prop.EmitTrue},
			"Position":       {Value: int64(0), Writable: false, Emit: prop.EmitTrue},
			"CanControl":     {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanPlay":        {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanPause":       {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanGoNext":      {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanGoPrevious":  {Value: true, Writable: false, Emit: prop.EmitTrue},
			"CanSeek":        {Value: false, Writable: false, Emit: prop.EmitTrue},
		},
	}
	props, err := prop.Export(conn, mediaProxyPath, propsSpec)
	if err != nil {
		log.Printf("[mediaproxy] cannot export properties: %v", err)
		conn.Close()
		return nil
	}
	mp.props = props

	node := &introspect.Node{
		Name: mediaProxyPath,
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name:       "org.mpris.MediaPlayer2",
				Methods:    introspect.Methods(methods),
				Properties: props.Introspection("org.mpris.MediaPlayer2"),
			},
			{
				Name:       "org.mpris.MediaPlayer2.Player",
				Methods:    introspect.Methods(methods),
				Properties: props.Introspection("org.mpris.MediaPlayer2.Player"),
			},
		},
	}
	conn.Export(introspect.NewIntrospectable(node), mediaProxyPath, "org.freedesktop.DBus.Introspectable")

	go mp.loop()
	log.Printf("[mediaproxy] album-art proxy active as %s", mediaProxyName)
	return mp
}

func (mp *mediaProxy) close() {
	if mp == nil {
		return
	}
	close(mp.stop)
	mp.conn.Close()
}

func emptyMetadata() map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/wepapered/none")),
	}
}

// loop polls the active real player ~once a second and republishes its metadata
// with a resolved artUrl when one is missing.
func (mp *mediaProxy) loop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastSig string
	for {
		select {
		case <-mp.stop:
			return
		case <-ticker.C:
		}
		st := mp.readActivePlayer()
		sig := st.status + "\x1f" + st.title + "\x1f" + st.artist + "\x1f" + st.artURL
		if sig == lastSig {
			continue
		}
		lastSig = sig
		mp.publish(st)
	}
}

type playerState struct {
	status  string
	trackid string
	title   string
	artist  string
	album   string
	length  int64
	artURL  string
	trackURL string
}

// readActivePlayer asks playerctl for the active player's metadata, ignoring our
// own proxy so we never mirror ourselves.
func (mp *mediaProxy) readActivePlayer() playerState {
	const fmtStr = "{{status}}\x1f{{mpris:trackid}}\x1f{{xesam:title}}\x1f{{xesam:artist}}\x1f" +
		"{{xesam:album}}\x1f{{mpris:length}}\x1f{{mpris:artUrl}}\x1f{{xesam:url}}"
	args := []string{"--ignore-player=" + mediaProxyIdentity}
	if mp.preferredPlayer != "" {
		args = append(args, "--player="+mp.preferredPlayer)
	}
	args = append(args, "metadata", "--format", fmtStr)
	out, err := exec.Command("playerctl", args...).Output()
	if err != nil {
		return playerState{status: "Stopped"}
	}
	parts := strings.Split(strings.TrimRight(string(out), "\n"), "\x1f")
	for len(parts) < 8 {
		parts = append(parts, "")
	}
	st := playerState{
		status: strings.TrimSpace(parts[0]), trackid: parts[1], title: parts[2],
		artist: parts[3], album: parts[4], artURL: parts[6], trackURL: parts[7],
	}
	if st.status == "" {
		st.status = "Stopped"
	}
	return st
}

// publish resolves the art (if missing) and updates the exposed properties.
func (mp *mediaProxy) publish(st playerState) {
	if st.artURL == "" && st.title != "" {
		st.artURL = mp.resolveArt(st)
	}

	meta := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath(sanitizeTrackID(st.trackid))),
		"xesam:title":   dbus.MakeVariant(st.title),
		"xesam:artist":  dbus.MakeVariant([]string{st.artist}),
		"xesam:album":   dbus.MakeVariant(st.album),
	}
	if st.artURL != "" {
		meta["mpris:artUrl"] = dbus.MakeVariant(st.artURL)
	}

	mp.props.SetMust("org.mpris.MediaPlayer2.Player", "PlaybackStatus", st.status)
	mp.props.SetMust("org.mpris.MediaPlayer2.Player", "Metadata", meta)
}

func sanitizeTrackID(id string) string {
	if strings.HasPrefix(id, "/") {
		return id
	}
	return "/org/mpris/MediaPlayer2/wepapered/track"
}

// resolveArt returns an album-art URL for the track, trying the YouTube thumbnail
// (when the track URL is a YouTube link) then the iTunes Search API. Results are
// cached per title+artist (including misses) so we only hit the network once.
func (mp *mediaProxy) resolveArt(st playerState) string {
	key := st.title + "\x1f" + st.artist
	mp.mu.Lock()
	if v, ok := mp.artCache[key]; ok {
		mp.mu.Unlock()
		return v
	}
	mp.mu.Unlock()

	art := ""
	if id := youtubeID(st.trackURL); id != "" {
		art = "https://i.ytimg.com/vi/" + id + "/maxresdefault.jpg"
	} else {
		art = mp.itunesArt(st.title, st.artist)
	}

	mp.mu.Lock()
	mp.artCache[key] = art
	mp.mu.Unlock()
	if art != "" {
		log.Printf("[mediaproxy] resolved art for %q — %q", st.title, art)
	}
	return art
}

var ytIDRe = regexp.MustCompile(`(?:v=|youtu\.be/|/watch\?v=)([A-Za-z0-9_-]{11})`)

func youtubeID(trackURL string) string {
	if !strings.Contains(trackURL, "youtu") {
		return ""
	}
	if m := ytIDRe.FindStringSubmatch(trackURL); m != nil {
		return m[1]
	}
	return ""
}

// itunesArt queries the keyless iTunes Search API and returns a 600x600 artwork
// URL for the best match, or "" if none.
func (mp *mediaProxy) itunesArt(title, artist string) string {
	term := strings.TrimSpace(artist + " " + title)
	if term == "" {
		return ""
	}
	endpoint := "https://itunes.apple.com/search?entity=song&limit=1&term=" + url.QueryEscape(term)
	resp, err := mp.httpc.Get(endpoint)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	var parsed struct {
		Results []struct {
			ArtworkURL100 string `json:"artworkUrl100"`
		} `json:"results"`
	}
	if json.Unmarshal(body, &parsed) != nil || len(parsed.Results) == 0 {
		return ""
	}
	art := parsed.Results[0].ArtworkURL100
	if art == "" {
		return ""
	}
	return strings.ReplaceAll(art, "100x100bb", "600x600bb")
}
