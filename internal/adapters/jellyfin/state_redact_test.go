package jellyfin

import (
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// probeSystemInfo and the websocket dialer both carry the access token
// as ?api_key=; a failed request's url.Error embeds that URL. setState
// is the single funnel into Status().LastError (UI lamps, SSE, logs),
// so it must strip the token.
func TestSetState_RedactsTokenFromErrorMessage(t *testing.T) {
	a := newSnapshotTestAdapter(t, "")
	a.setState(adapters.StateError,
		`jellyfin: probe: Get "http://jf:8096/System/Info?api_key=SECRET123": dial tcp: connection refused`)

	got := a.Status().LastError
	if strings.Contains(got, "SECRET123") {
		t.Fatalf("Status().LastError leaks token: %q", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("Status().LastError lost diagnostic detail: %q", got)
	}
}
