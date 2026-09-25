package spotify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/liveaudio"
)

// HookFlag is the argument that turns the bridge binary into librespot's
// --onevent program (see RunEventHook).
const HookFlag = "--librespot-event"

// eventURLEnv carries the loopback event URL from the bridge, through
// librespot's inherited environment, to the hook process.
const eventURLEnv = "MISTER_GROOVY_LIBRESPOT_EVENT_URL"

// hookVars are the librespot event variables the hook forwards.
var hookVars = []string{
	"PLAYER_EVENT", "ITEM_TYPE", "NAME", "ARTISTS", "ALBUM", "SHOW_NAME", "COVERS", "DURATION_MS",
}

// RunEventHook is the bridge binary's --onevent mode: it forwards the
// librespot event in its environment to the running bridge and returns an
// exit code. librespot blocks its event thread on this process, so it
// never retries and gives up quickly; it always exits 0 so a bridge
// hiccup is not reported as a librespot error.
func RunEventHook(getenv func(string) string, stderr io.Writer) int {
	target := getenv(eventURLEnv)
	if target == "" {
		fmt.Fprintln(stderr, "librespot event hook: "+eventURLEnv+" not set; run only by librespot under the bridge")
		return 0
	}
	form := url.Values{}
	for _, k := range hookVars {
		if v := getenv(k); v != "" {
			form.Set(k, v)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		fmt.Fprintln(stderr, "librespot event hook:", err)
		return 0
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "librespot event hook:", err)
		return 0
	}
	_ = resp.Body.Close()
	return 0
}

// eventFromForm maps a forwarded librespot event to a liveaudio event.
// ok is false for events the session does not act on.
func eventFromForm(form url.Values) (ev liveaudio.Event, ok bool) {
	switch form.Get("PLAYER_EVENT") {
	case "playing":
		return liveaudio.Event{Kind: liveaudio.EventPlay}, true
	case "paused":
		return liveaudio.Event{Kind: liveaudio.EventPause}, true
	case "stopped", "session_disconnected":
		return liveaudio.Event{Kind: liveaudio.EventStop}, true
	case "track_changed":
		return liveaudio.Event{Kind: liveaudio.EventTrack, Track: trackFromForm(form)}, true
	default:
		return liveaudio.Event{}, false
	}
}

func trackFromForm(form url.Values) liveaudio.TrackMeta {
	t := liveaudio.TrackMeta{
		Title:  form.Get("NAME"),
		Artist: joinLines(form.Get("ARTISTS")),
		Album:  form.Get("ALBUM"),
	}
	if form.Get("ITEM_TYPE") == "Episode" {
		t.Artist = form.Get("SHOW_NAME")
	}
	if ms, err := strconv.ParseInt(form.Get("DURATION_MS"), 10, 64); err == nil && ms > 0 {
		t.Duration = time.Duration(ms) * time.Millisecond
	}
	// COVERS lists one URL per line, largest first in practice; the
	// artwork cache downsizes nothing, so any of them works.
	for _, line := range strings.Split(form.Get("COVERS"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			t.ArtworkURL = line
			break
		}
	}
	return t
}

// joinLines turns librespot's newline-separated list into "A, B".
func joinLines(s string) string {
	var parts []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, ", ")
}
