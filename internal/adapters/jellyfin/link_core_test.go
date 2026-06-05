package jellyfin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// newSnapshotTestAdapter constructs a minimal Adapter for linkSnapshot tests.
// tokenPath() derives from a.dataDir via filepath.Join(a.dataDir, "jellyfin", "token.json"),
// so we pass t.TempDir() to New() for per-test isolation.
func newSnapshotTestAdapter(t *testing.T, serverURL string) *Adapter {
	t.Helper()
	a := New(nil, t.TempDir(), "dev-1", "", nil)
	a.cfg.ServerURL = serverURL
	return a
}

// writeRawToken writes raw bytes directly to the token file path,
// creating the parent directory as needed. Used to inject corrupt JSON
// that SaveToken's atomicity would prevent.
func writeRawToken(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o600)
}

func TestJFSnapshot_NoToken(t *testing.T) {
	a := newSnapshotTestAdapter(t, "")
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseUnlinked {
		t.Errorf("Phase = %q, want unlinked", got.Phase)
	}
	if !got.NeedsServerURL {
		t.Errorf("NeedsServerURL = false, want true (blank server_url)")
	}
}

func TestJFSnapshot_NoTokenWithURL(t *testing.T) {
	a := newSnapshotTestAdapter(t, "http://jf.local:8096")
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseUnlinked || got.NeedsServerURL {
		t.Errorf("got %+v, want unlinked + NeedsServerURL=false", got)
	}
}

func TestJFSnapshot_Linked(t *testing.T) {
	a := newSnapshotTestAdapter(t, "http://jf.local:8096")
	if err := SaveToken(a.tokenPath(), Token{
		AccessToken: "tok", UserName: "jake", ServerID: "srv-9", ServerURL: "http://jf.local:8096",
	}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseLinked {
		t.Fatalf("Phase = %q, want linked", got.Phase)
	}
	if got.LinkedAs != "jake on srv-9" {
		t.Errorf("LinkedAs = %q, want 'jake on srv-9'", got.LinkedAs)
	}
}

func TestJFSnapshot_ParseError(t *testing.T) {
	a := newSnapshotTestAdapter(t, "http://jf.local:8096")
	// linkSnapshot reads the token file directly (not via LoadToken) so it can
	// distinguish a JSON parse failure from a missing file. LoadToken silently
	// swallows corrupt JSON; linkSnapshot must not.
	if err := writeRawToken(a.tokenPath(), "{not json"); err != nil {
		t.Fatalf("write corrupt token: %v", err)
	}
	got := a.linkSnapshot()
	if got.Phase != adapters.LinkPhaseError {
		t.Errorf("Phase = %q, want error", got.Phase)
	}
}

func TestJFController_StartMissingURL(t *testing.T) {
	a := newSnapshotTestAdapter(t, "")
	got, _ := a.StartLink(contextTODO(), map[string]string{"username": "x", "password": "y"})
	if got.Phase != adapters.LinkPhaseError {
		t.Errorf("Phase = %q, want error (no server_url)", got.Phase)
	}
}

func TestJFController_StartBlankCreds(t *testing.T) {
	a := newSnapshotTestAdapter(t, "http://jf.local:8096")
	got, _ := a.StartLink(contextTODO(), map[string]string{"username": "", "password": ""})
	if got.Phase != adapters.LinkPhaseError {
		t.Errorf("Phase = %q, want error (blank creds)", got.Phase)
	}
}

func TestJFController_Conformance(t *testing.T) {
	var _ adapters.LinkController = (*Adapter)(nil)
}

func contextTODO() context.Context { return context.TODO() }
