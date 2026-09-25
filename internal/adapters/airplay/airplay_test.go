package airplay

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/liveaudio"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/artworkcache"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

// TestMain doubles as a fake shairport-sync: launched with "-c <conf>",
// the test binary reads the metadata port from the generated config,
// announces a track over UDP the way shairport-sync does, and idles.
func TestMain(m *testing.M) {
	if i := slices.Index(os.Args[1:], "-c"); i >= 0 && i+2 < len(os.Args) {
		fakeShairport(os.Args[i+2])
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(m.Run())
}

var socketPortRE = regexp.MustCompile(`socket_port = (\d+);`)

func fakeShairport(confPath string) {
	conf, err := os.ReadFile(confPath)
	if err != nil {
		os.Exit(2)
	}
	match := socketPortRE.FindSubmatch(conf)
	if match == nil {
		os.Exit(3)
	}
	conn, err := net.Dial("udp", "127.0.0.1:"+string(match[1]))
	if err != nil {
		os.Exit(4)
	}
	for _, pkt := range [][]byte{
		item("ssnc", "pbeg", nil),
		item("ssnc", "mdst", nil),
		item("core", "minm", []byte("Blue Monday")),
		item("core", "asar", []byte("New Order")),
		item("ssnc", "mden", nil),
	} {
		_, _ = conn.Write(pkt)
		time.Sleep(10 * time.Millisecond)
	}
}

// item builds a metadata datagram: type[4] code[4] data.
func item(typ, code string, data []byte) []byte {
	return append([]byte(typ+code), data...)
}

// chunks splits data the way shairport-sync does: "ssncchnk" index[4]
// total[4] type[4] code[4] data, payloads of at most size bytes.
func chunks(typ, code string, data []byte, size int) [][]byte {
	total := (len(data) + size - 1) / size
	var out [][]byte
	for i := 0; i < total; i++ {
		end := min((i+1)*size, len(data))
		pkt := []byte("ssncchnk")
		pkt = binary.BigEndian.AppendUint32(pkt, uint32(i))
		pkt = binary.BigEndian.AppendUint32(pkt, uint32(total))
		pkt = append(pkt, typ+code...)
		pkt = append(pkt, data[i*size:end]...)
		out = append(out, pkt)
	}
	return out
}

func kinds(evs []liveaudio.Event) []liveaudio.EventKind {
	var out []liveaudio.EventKind
	for _, ev := range evs {
		out = append(out, ev.Kind)
	}
	return out
}

func TestParserPlaybackEvents(t *testing.T) {
	var p metadataParser
	cases := []struct {
		code string
		want liveaudio.EventKind
	}{
		{"pbeg", liveaudio.EventPlay},
		{"pfls", liveaudio.EventPause},
		{"prsm", liveaudio.EventResume},
		{"pend", liveaudio.EventStop},
	}
	for _, tc := range cases {
		got := p.packet(item("ssnc", tc.code, nil))
		if len(got) != 1 || got[0].Kind != tc.want {
			t.Errorf("ssnc/%s → %v, want [%v]", tc.code, kinds(got), tc.want)
		}
	}
	for _, ignored := range [][]byte{item("ssnc", "pvol", []byte("-15.0,0,0,0")), item("ssnc", "snua", []byte("iOS")), []byte("short"), nil} {
		if got := p.packet(ignored); len(got) != 0 {
			t.Errorf("packet %q → %v, want nothing", ignored, kinds(got))
		}
	}
}

func TestParserBundleEmitsOneTrack(t *testing.T) {
	var p metadataParser
	var got []liveaudio.Event
	for _, pkt := range [][]byte{
		item("ssnc", "mdst", []byte("1234")),
		item("core", "minm", []byte("Age of Consent")),
		item("core", "asar", []byte("New Order")),
		item("core", "asal", []byte("Power, Corruption & Lies")),
		item("core", "asgn", []byte("Alternative")), // ignored field
		item("ssnc", "mden", []byte("1234")),
	} {
		got = append(got, p.packet(pkt)...)
	}
	if len(got) != 1 || got[0].Kind != liveaudio.EventTrack {
		t.Fatalf("bundle → %v, want one track event", kinds(got))
	}
	tr := got[0].Track
	if tr.Title != "Age of Consent" || tr.Artist != "New Order" || tr.Album != "Power, Corruption & Lies" {
		t.Fatalf("track = %+v", tr)
	}
	// A lone field outside a bundle updates the current track immediately.
	got = p.packet(item("core", "minm", []byte("Temptation")))
	if len(got) != 1 || got[0].Track.Title != "Temptation" || got[0].Track.Artist != "New Order" {
		t.Fatalf("lone title → %+v", got)
	}
	// A stop forgets the track.
	p.packet(item("ssnc", "pend", nil))
	got = p.packet(item("core", "asar", []byte("Joy Division")))
	if got[0].Track.Title != "" {
		t.Fatalf("track survived pend: %+v", got[0].Track)
	}
}

func TestParserPictureSingleAndChunked(t *testing.T) {
	var p metadataParser
	art := bytes.Repeat([]byte{0xAB}, 1000)
	pkt := item("ssnc", "PICT", art)
	got := p.packet(pkt)
	if len(got) != 1 || got[0].Kind != liveaudio.EventArtwork || !bytes.Equal(got[0].Track.ArtworkBytes, art) {
		t.Fatalf("single PICT → %v", kinds(got))
	}
	pkt[8] = 0 // the listener reuses its read buffer
	if got[0].Track.ArtworkBytes[0] != 0xAB {
		t.Fatal("artwork aliases the packet buffer")
	}

	big := make([]byte, 150_000)
	for i := range big {
		big[i] = byte(i)
	}
	parts := chunks("ssnc", "PICT", big, 64976)
	if len(parts) != 3 {
		t.Fatalf("test setup: %d chunks", len(parts))
	}
	// Out of order, with a duplicate, still reassembles exactly once.
	var events []liveaudio.Event
	for _, i := range []int{2, 0, 0, 1} {
		events = append(events, p.packet(parts[i])...)
	}
	if len(events) != 1 || !bytes.Equal(events[0].Track.ArtworkBytes, big) {
		t.Fatalf("chunked PICT → %v (len %d)", kinds(events), len(events))
	}

	if got := p.packet(item("ssnc", "PICT", nil)); len(got) != 1 || len(got[0].Track.ArtworkBytes) != 0 {
		t.Fatal("empty PICT must clear the artwork")
	}
}

func TestParserDropsOversizedAndMalformedChunks(t *testing.T) {
	var p metadataParser
	huge := make([]byte, artworkcache.MaxBytes+1)
	var events []liveaudio.Event
	for _, pkt := range chunks("ssnc", "PICT", huge, 64976) {
		events = append(events, p.packet(pkt)...)
	}
	if len(events) != 0 {
		t.Fatal("oversized artwork was delivered")
	}
	bad := chunks("ssnc", "PICT", []byte("x"), 10)[0]
	binary.BigEndian.PutUint32(bad[8:12], 5) // index beyond total
	if got := p.packet(bad); len(got) != 0 {
		t.Fatal("chunk with index >= total accepted")
	}
}

func TestConfigValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	bad := Config{Name: "tab\there", AudioOutput: "x", PauseGraceSeconds: -1, Port: 0, BinaryPath: "a\nb"}
	var fe adapters.FieldErrors
	if !errors.As(bad.Validate(), &fe) {
		t.Fatal("expected field errors")
	}
	got := map[string]bool{}
	for _, e := range fe {
		got[e.Key] = true
	}
	for _, key := range []string{"name", "audio_output", "pause_grace_seconds", "port", "binary_path"} {
		if !got[key] {
			t.Errorf("no error for %s", key)
		}
	}
}

func TestRenderShairportConf(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Name = `Den "CRT" \ 1`
	cfg.Port = 5100
	conf := renderShairportConf(cfg, 45678)
	for _, want := range []string{
		`name = "Den \"CRT\" \\ 1";`,
		"port = 5100;",
		`output_backend = "stdout";`,
		`allow_session_interruption = "yes";`,
		`include_cover_art = "yes";`,
		`cover_art_cache_directory = "";`,
		`socket_address = "127.0.0.1";`,
		"socket_port = 45678;",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("config missing %q:\n%s", want, conf)
		}
	}
}

func TestStartRefusesWindows(t *testing.T) {
	a, err := New(AdapterConfig{Core: &fakeCore{}, goos: "windows"})
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.Enabled = true
	if err := a.Start(t.Context()); !errors.Is(err, errUnsupportedPlatform) {
		t.Fatalf("Start = %v, want unsupported platform", err)
	}
	if st := a.Status(); st.State != adapters.StateError || !strings.Contains(st.LastError, "not supported on Windows") {
		t.Fatalf("status = %+v", st)
	}
}

// fakeCore is a minimal liveaudio.Core.
type fakeCore struct {
	mu     sync.Mutex
	active *core.SessionRequest
	gen    uint64
	titles []string
}

func (f *fakeCore) StartSession(req core.SessionRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active = &req
	f.gen++
	return nil
}

func (f *fakeCore) StartSessionIfSession(core.SessionRequest, string, uint64) (bool, error) {
	return false, nil
}

func (f *fakeCore) StopIfSession(ref string, gen uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == nil || f.active.AdapterRef != ref {
		return false, nil
	}
	f.active = nil
	return true, nil
}

func (f *fakeCore) Status() core.SessionStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == nil {
		return core.SessionStatus{}
	}
	return core.SessionStatus{AdapterRef: f.active.AdapterRef, Generation: f.gen}
}

func (f *fakeCore) UpdateNowPlayingIfSession(ref string, gen uint64, title string, d core.DisplayMetadata) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.titles = append(f.titles, title)
	return true
}

func (f *fakeCore) VisualizerMode() string { return "retro_analyzer" }

func (f *fakeCore) snapshot() (ref string, titles []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active != nil {
		ref = f.active.AdapterRef
	}
	return ref, append([]string(nil), f.titles...)
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func decode(t *testing.T, doc string) (toml.Primitive, toml.MetaData) {
	t.Helper()
	var wrapper struct {
		Section toml.Primitive `toml:"airplay"`
	}
	meta, err := toml.Decode(doc, &wrapper)
	if err != nil {
		t.Fatal(err)
	}
	return wrapper.Section, meta
}

// TestMetadataReachesSessionEndToEnd runs the fake shairport-sync: the
// generated config carries the metadata port, its UDP packets flow
// through the parser, and the session starts with the announced track.
func TestMetadataReachesSessionEndToEnd(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeCore{}
	a, err := New(AdapterConfig{Core: fc, HTTPPort: 32500, DataDir: t.TempDir(), goos: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	prim, meta := decode(t, "[airplay]\nenabled = true\nbinary_path = '"+exe+"'\n")
	if err := a.DecodeConfig(prim, meta); err != nil {
		t.Fatal(err)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "airplay session with the announced track", func() bool {
		ref, titles := fc.snapshot()
		return ref == "airplay:1" && slices.Contains(titles, "Blue Monday")
	})
	if st := a.Status(); st.State != adapters.StateRunning {
		t.Fatalf("status = %+v", st)
	}

	// Renaming restarts the helper with a rewritten config.
	prim, meta = decode(t, "[airplay]\nenabled = true\nbinary_path = '"+exe+"'\nname = \"Den\"\n")
	scope, err := a.ApplyConfig(prim, meta)
	if err != nil || scope != adapters.ScopeRestartCast {
		t.Fatalf("rename: scope %v err %v", scope, err)
	}
	conf, err := os.ReadFile(filepath.Join(a.dataDir, "airplay", "shairport-sync.conf"))
	if err != nil || !strings.Contains(string(conf), `name = "Den";`) {
		t.Fatalf("config after rename: %v\n%s", err, conf)
	}
	if st := a.Status(); st.State != adapters.StateRunning {
		t.Fatalf("status after restart = %+v", st)
	}
}
