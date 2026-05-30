package localfiles

import (
	"context"
	"errors"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestAdapterImplementsInterfaces(t *testing.T) {
	var _ adapters.Adapter = (*Adapter)(nil)
	var _ adapters.Validator = (*Adapter)(nil)
}

func TestNewInitializesAdapter(t *testing.T) {
	a, err := New(AdapterConfig{
		Bridge:  config.BridgeConfig{DataDir: t.TempDir()},
		Core:    &recordingCore{},
		FFprobe: staticResolver{path: "/bin/ffprobe"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.Name() != "localfiles" {
		t.Fatalf("Name = %q, want localfiles", a.Name())
	}
	if a.DisplayName() != "Local Files" {
		t.Fatalf("DisplayName = %q, want Local Files", a.DisplayName())
	}
	if a.IsEnabled() {
		t.Fatalf("IsEnabled = true, want false default")
	}
	if st := a.Status(); st.State != adapters.StateStopped || st.Since.IsZero() {
		t.Fatalf("initial Status = %+v, want stopped with Since", st)
	}
}

func TestStartStopStatus(t *testing.T) {
	a := newTestAdapter(t, &recordingCore{})
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := a.Status().State; got != adapters.StateRunning {
		t.Fatalf("state after Start = %v, want running", got)
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := a.Status().State; got != adapters.StateStopped {
		t.Fatalf("state after Stop = %v, want stopped", got)
	}
}

func TestFieldsReturnsEnabledBool(t *testing.T) {
	a := newTestAdapter(t, nil)
	fields := a.Fields()
	if len(fields) != 1 {
		t.Fatalf("len(Fields) = %d, want 1", len(fields))
	}
	f := fields[0]
	if f.Key != "enabled" || f.Kind != adapters.KindBool || f.ApplyScope != adapters.ScopeHotSwap {
		t.Fatalf("enabled field = %+v, want bool hot-swap", f)
	}
}

func TestCurrentValuesAndLibrariesReturnCopies(t *testing.T) {
	root := t.TempDir()
	a := newTestAdapter(t, nil)
	a.mu.Lock()
	a.cfg = Config{Enabled: true, Libraries: []Library{{Name: "Movies", Root: root}}}
	a.mu.Unlock()

	values := a.CurrentValues()
	if values["enabled"] != true {
		t.Fatalf("CurrentValues enabled = %v, want true", values["enabled"])
	}
	libs := a.CurrentLibraries()
	if len(libs) != 1 || libs[0].Name != "Movies" {
		t.Fatalf("CurrentLibraries = %+v", libs)
	}
	libs[0].Name = "Mutated"
	if got := a.CurrentLibraries()[0].Name; got != "Movies" {
		t.Fatalf("CurrentLibraries returned aliased slice; got %q", got)
	}
}

func TestDecodeConfigWithTwoLibrariesPopulatesConfigAndEnablesAdapter(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	raw, meta := localfilesPrimitive(t, `
enabled = true
[[adapters.localfiles.library]]
name = "Movies"
root = "`+tomlEscape(root1)+`"

[[adapters.localfiles.library]]
name = "Music"
root = "`+tomlEscape(root2)+`"
`)

	a := newTestAdapter(t, nil)
	if err := a.DecodeConfig(raw, meta); err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if !a.IsEnabled() {
		t.Fatalf("IsEnabled = false, want true")
	}
	got := a.configSnapshot()
	if len(got.Libraries) != 2 {
		t.Fatalf("len(Libraries) = %d, want 2", len(got.Libraries))
	}
	if got.Libraries[0] != (Library{Name: "Movies", Root: root1}) {
		t.Fatalf("library[0] = %+v, want Movies at %q", got.Libraries[0], root1)
	}
	if got.Libraries[1] != (Library{Name: "Music", Root: root2}) {
		t.Fatalf("library[1] = %+v, want Music at %q", got.Libraries[1], root2)
	}
}

func TestApplyConfigStoresConfigAndReturnsHotSwap(t *testing.T) {
	a := newTestAdapter(t, nil)
	raw2, meta2 := localfilesPrimitive(t, `enabled = false`)
	scope, err := a.ApplyConfig(raw2, meta2)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if scope != adapters.ScopeHotSwap {
		t.Fatalf("scope = %v, want hot-swap", scope)
	}
	if a.IsEnabled() {
		t.Fatalf("IsEnabled = true after disabled apply")
	}
}

func TestValidateBadTOMLReturnsErrorWithoutMutatingConfig(t *testing.T) {
	root := t.TempDir()
	a := newTestAdapter(t, nil)
	good, goodMeta := localfilesPrimitive(t, `
enabled = true
[[adapters.localfiles.library]]
name = "Movies"
root = "`+tomlEscape(root)+`"
`)
	if err := a.DecodeConfig(good, goodMeta); err != nil {
		t.Fatalf("DecodeConfig good: %v", err)
	}

	bad, badMeta := localfilesPrimitive(t, `
enabled = false
[[adapters.localfiles.library]]
name = ""
root = "`+tomlEscape(root)+`"
`)
	if err := a.Validate(bad, badMeta); err == nil {
		t.Fatalf("Validate bad = nil, want error")
	}
	if !a.IsEnabled() {
		t.Fatalf("Validate mutated cfg; IsEnabled = false, want true")
	}
}

func newTestAdapter(t *testing.T, c SessionManager) *Adapter {
	t.Helper()
	a, err := New(AdapterConfig{
		Bridge:  config.BridgeConfig{DataDir: t.TempDir()},
		Core:    c,
		FFprobe: staticResolver{path: "/bin/ffprobe"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

type staticResolver struct {
	path string
	err  error
}

func (r staticResolver) Resolve() (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.path, nil
}

type recordingCore struct {
	reqs []core.SessionRequest
	err  error
}

func (c *recordingCore) StartSession(req core.SessionRequest) error {
	if c.err != nil {
		return c.err
	}
	c.reqs = append(c.reqs, req)
	return nil
}

func (c *recordingCore) lastReq(t *testing.T) core.SessionRequest {
	t.Helper()
	if len(c.reqs) == 0 {
		t.Fatal("no StartSession calls")
	}
	return c.reqs[len(c.reqs)-1]
}

func errResolver() BinaryResolver {
	return staticResolver{err: errors.New("no ffprobe")}
}

func localfilesPrimitive(t *testing.T, body string) (toml.Primitive, toml.MetaData) {
	t.Helper()
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode("[adapters.localfiles]\n"+body, &envelope)
	if err != nil {
		t.Fatalf("decode test TOML: %v", err)
	}
	return envelope.Adapters["localfiles"], meta
}

func tomlEscape(s string) string {
	var out []rune
	for _, r := range s {
		if r == '\\' || r == '"' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
