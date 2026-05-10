package dlna

import (
	"log/slog"
	"math/rand"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// validDiscoveryConfig builds a DiscoveryConfig that satisfies all
// constructor invariants. Tests can override fields after the call.
func validDiscoveryConfig() DiscoveryConfig {
	return DiscoveryConfig{
		DeviceUUID: "abcdef01-2345-6789-abcd-ef0123456789",
		DeviceName: "MiSTer-Test",
		HostIP:     "192.168.1.50",
		HTTPPort:   32500,
	}
}

// newTestDiscovery builds a Discovery without binding any sockets so
// pure-function tests don't pay the price of a real multicast join.
// listen and sender stay nil; tests that exercise Run must NOT call
// it on the result. Targets / RNG match what NewDiscovery would set.
// ServerToken honors cfg.ServerToken when set so tests for the version
// threading work; otherwise the default is used.
func newTestDiscovery(cfg DiscoveryConfig) *Discovery {
	token := strings.TrimSpace(cfg.ServerToken)
	if token == "" {
		token = ssdpServerTokenDefault
	}
	return &Discovery{
		cfg:         cfg,
		logger:      slog.Default(),
		targets:     newTargets(cfg.DeviceUUID),
		serverToken: token,
	}
}

func TestNewDiscovery_RequiresValidHostIP(t *testing.T) {
	for name, hostIP := range map[string]string{
		"empty":      "",
		"whitespace": "   ",
		"non-ip":     "not-an-ip",
		"garbage":    "192.168.1.999",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validDiscoveryConfig()
			cfg.HostIP = hostIP
			if _, err := NewDiscovery(cfg); err == nil {
				t.Errorf("NewDiscovery with HostIP=%q: want error, got nil", hostIP)
			}
		})
	}
}

func TestNewDiscovery_RequiresDeviceUUID(t *testing.T) {
	cfg := validDiscoveryConfig()
	cfg.DeviceUUID = ""
	if _, err := NewDiscovery(cfg); err == nil {
		t.Error("NewDiscovery with empty DeviceUUID: want error, got nil")
	}
	cfg.DeviceUUID = "   "
	if _, err := NewDiscovery(cfg); err == nil {
		t.Error("NewDiscovery with whitespace DeviceUUID: want error, got nil")
	}
}

func TestNewDiscovery_RequiresValidPort(t *testing.T) {
	for _, port := range []int{0, -1, 70000, 65536} {
		cfg := validDiscoveryConfig()
		cfg.HTTPPort = port
		if _, err := NewDiscovery(cfg); err == nil {
			t.Errorf("NewDiscovery with HTTPPort=%d: want error, got nil", port)
		}
	}
}

func TestBuildNotify_AliveHasAllRequiredHeaders(t *testing.T) {
	d := newTestDiscovery(validDiscoveryConfig())
	body := string(d.buildNotify("ssdp:alive", "upnp:rootdevice",
		"uuid:abcdef01-2345-6789-abcd-ef0123456789::upnp:rootdevice"))

	mustContain := []string{
		"NOTIFY * HTTP/1.1\r\n",
		"HOST: 239.255.255.250:1900\r\n",
		"CACHE-CONTROL: max-age=1800\r\n",
		"LOCATION: http://192.168.1.50:32500/dlna/device.xml\r\n",
		"NT: upnp:rootdevice\r\n",
		"NTS: ssdp:alive\r\n",
		"SERVER: ",
		"USN: uuid:abcdef01-2345-6789-abcd-ef0123456789::upnp:rootdevice\r\n",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("alive NOTIFY missing %q\n--- body ---\n%s", want, body)
		}
	}
	// Trailing blank line at end of headers.
	if !strings.HasSuffix(body, "\r\n\r\n") {
		t.Error("alive NOTIFY must end with CRLF CRLF")
	}
}

func TestBuildNotify_ByebyeOmitsLocation(t *testing.T) {
	d := newTestDiscovery(validDiscoveryConfig())
	body := string(d.buildNotify("ssdp:byebye", "upnp:rootdevice",
		"uuid:abcdef01-2345-6789-abcd-ef0123456789::upnp:rootdevice"))

	mustContain := []string{
		"NOTIFY * HTTP/1.1\r\n",
		"HOST: 239.255.255.250:1900\r\n",
		"NT: upnp:rootdevice\r\n",
		"NTS: ssdp:byebye\r\n",
		"USN: uuid:abcdef01-2345-6789-abcd-ef0123456789::upnp:rootdevice\r\n",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("byebye NOTIFY missing %q\n--- body ---\n%s", want, body)
		}
	}
	mustNotContain := []string{
		"CACHE-CONTROL",
		"LOCATION",
		"SERVER",
	}
	for _, deny := range mustNotContain {
		if strings.Contains(body, deny) {
			t.Errorf("byebye NOTIFY must not contain %q\n--- body ---\n%s", deny, body)
		}
	}
}

func TestBuildSearchResponse_HasAllRequiredHeaders(t *testing.T) {
	d := newTestDiscovery(validDiscoveryConfig())
	body := string(d.buildSearchResponse("upnp:rootdevice",
		"uuid:abcdef01-2345-6789-abcd-ef0123456789::upnp:rootdevice"))

	mustContain := []string{
		"HTTP/1.1 200 OK\r\n",
		"CACHE-CONTROL: max-age=1800\r\n",
		"DATE: ",
		"EXT:\r\n",
		"LOCATION: http://192.168.1.50:32500/dlna/device.xml\r\n",
		"SERVER: ",
		"ST: upnp:rootdevice\r\n",
		"USN: uuid:abcdef01-2345-6789-abcd-ef0123456789::upnp:rootdevice\r\n",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("search response missing %q\n--- body ---\n%s", want, body)
		}
	}
	if !strings.HasSuffix(body, "\r\n\r\n") {
		t.Error("search response must end with CRLF CRLF")
	}
}

func TestLocationURL_BracketsIPv6(t *testing.T) {
	cfg := validDiscoveryConfig()
	cfg.HostIP = "fe80::1"
	d := newTestDiscovery(cfg)
	got := d.locationURL()
	want := "http://[fe80::1]:32500/dlna/device.xml"
	if got != want {
		t.Errorf("IPv6 LOCATION = %q, want %q", got, want)
	}
}

func TestLocationURL_PlainIPv4(t *testing.T) {
	d := newTestDiscovery(validDiscoveryConfig())
	got := d.locationURL()
	want := "http://192.168.1.50:32500/dlna/device.xml"
	if got != want {
		t.Errorf("IPv4 LOCATION = %q, want %q", got, want)
	}
}

func TestNTUSNTable_HasSixEntries(t *testing.T) {
	uuid := "abcdef01-2345-6789-abcd-ef0123456789"
	targets := newTargets(uuid)
	if len(targets) != 6 {
		t.Fatalf("targets = %d, want 6", len(targets))
	}
	wantNTs := []string{
		"upnp:rootdevice",
		"uuid:" + uuid,
		"urn:schemas-upnp-org:device:MediaRenderer:1",
		"urn:schemas-upnp-org:service:AVTransport:1",
		"urn:schemas-upnp-org:service:ConnectionManager:1",
		"urn:schemas-upnp-org:service:RenderingControl:1",
	}
	for i, want := range wantNTs {
		if targets[i].nt != want {
			t.Errorf("targets[%d].nt = %q, want %q", i, targets[i].nt, want)
		}
		// USN must include "uuid:<UUID>" — either as the entire USN
		// (the bare-UUID entry) or as the prefix before "::".
		if !strings.Contains(targets[i].usn, "uuid:"+uuid) {
			t.Errorf("targets[%d].usn = %q does not contain uuid:%s", i, targets[i].usn, uuid)
		}
	}
	// The bare-UUID entry must have USN == NT (UPnP § 1.1.4: the
	// device UUID announces with USN == "uuid:<UUID>", not
	// "uuid:<UUID>::uuid:<UUID>").
	if targets[1].usn != targets[1].nt {
		t.Errorf("device-UUID entry: usn = %q, want USN == NT (%q)", targets[1].usn, targets[1].nt)
	}
}

func TestParseMSearch_Match_All(t *testing.T) {
	uuid := "abcdef01-2345-6789-abcd-ef0123456789"
	targets := newTargets(uuid)
	matched := 0
	for _, tg := range targets {
		if tg.matches("ssdp:all") {
			matched++
		}
	}
	if matched != len(targets) {
		t.Errorf("ssdp:all matched %d/%d entries; want all", matched, len(targets))
	}
}

func TestParseMSearch_Match_RootDevice(t *testing.T) {
	targets := newTargets("u")
	got := matchedNTs(targets, "upnp:rootdevice")
	want := []string{"upnp:rootdevice"}
	assertEqualStringSet(t, got, want)
}

func TestParseMSearch_Match_DeviceUUID(t *testing.T) {
	targets := newTargets("u")
	got := matchedNTs(targets, "uuid:u")
	want := []string{"uuid:u"}
	assertEqualStringSet(t, got, want)
}

func TestParseMSearch_Match_MediaRenderer(t *testing.T) {
	targets := newTargets("u")
	got := matchedNTs(targets, "urn:schemas-upnp-org:device:MediaRenderer:1")
	want := []string{"urn:schemas-upnp-org:device:MediaRenderer:1"}
	assertEqualStringSet(t, got, want)
}

func TestParseMSearch_Match_Service(t *testing.T) {
	targets := newTargets("u")
	for _, urn := range []string{
		"urn:schemas-upnp-org:service:AVTransport:1",
		"urn:schemas-upnp-org:service:ConnectionManager:1",
		"urn:schemas-upnp-org:service:RenderingControl:1",
	} {
		got := matchedNTs(targets, urn)
		want := []string{urn}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("ST=%q matched NTs %v; want %v", urn, got, want)
		}
	}
}

func TestParseMSearch_NoMatch(t *testing.T) {
	targets := newTargets("u")
	got := matchedNTs(targets, "urn:schemas-upnp-org:service:DoesNotExist:1")
	if len(got) != 0 {
		t.Errorf("unknown ST matched %v; want []", got)
	}
}

func TestParseMSearch_RequiresMSearchPrefix(t *testing.T) {
	if _, _, ok := parseMSearch([]byte("NOTIFY * HTTP/1.1\r\n\r\n")); ok {
		t.Error("parseMSearch accepted a NOTIFY datagram; want ok=false")
	}
}

func TestParseMSearch_RequiresST(t *testing.T) {
	req := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\n\r\n"
	if _, _, ok := parseMSearch([]byte(req)); ok {
		t.Error("parseMSearch accepted M-SEARCH with no ST; want ok=false")
	}
}

func TestParseMSearch_ExtractsSTAndMX(t *testing.T) {
	req := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 3\r\n" +
		"ST: ssdp:all\r\n\r\n"
	st, mx, ok := parseMSearch([]byte(req))
	if !ok {
		t.Fatal("parseMSearch returned ok=false on a well-formed request")
	}
	if st != "ssdp:all" {
		t.Errorf("st = %q, want ssdp:all", st)
	}
	if mx != "3" {
		t.Errorf("mx = %q, want 3", mx)
	}
}

func TestParseMSearch_HeaderCaseInsensitive(t *testing.T) {
	// Real-world controllers use mixed casing on header names.
	req := "M-SEARCH * HTTP/1.1\r\n" +
		"Host: 239.255.255.250:1900\r\n" +
		"st: upnp:rootdevice\r\n" +
		"mx: 2\r\n\r\n"
	st, mx, ok := parseMSearch([]byte(req))
	if !ok {
		t.Fatal("parseMSearch returned ok=false on lowercase headers")
	}
	if st != "upnp:rootdevice" {
		t.Errorf("st = %q, want upnp:rootdevice", st)
	}
	if mx != "2" {
		t.Errorf("mx = %q, want 2", mx)
	}
}

func TestParseMX_ParsesValidSeconds(t *testing.T) {
	if got := parseMX("3"); got != 3*time.Second {
		t.Errorf("parseMX(3) = %v, want 3s", got)
	}
}

func TestParseMX_CapsAtFiveSeconds(t *testing.T) {
	for _, raw := range []string{"5", "6", "100", "999999"} {
		got := parseMX(raw)
		want := ssdpMXCapMax
		if raw == "5" {
			want = 5 * time.Second
		}
		if got != want {
			t.Errorf("parseMX(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestParseMX_MissingFallsBackToOneSecond(t *testing.T) {
	if got := parseMX(""); got != ssdpMXCapDefault {
		t.Errorf("parseMX(empty) = %v, want %v", got, ssdpMXCapDefault)
	}
}

func TestParseMX_MalformedFallsBackToOneSecond(t *testing.T) {
	for _, raw := range []string{"foo", "1.5", "abc", "  "} {
		if got := parseMX(raw); got != ssdpMXCapDefault {
			t.Errorf("parseMX(%q) = %v, want %v", raw, got, ssdpMXCapDefault)
		}
	}
}

func TestParseMX_NegativeFallsBackToOneSecond(t *testing.T) {
	if got := parseMX("-3"); got != ssdpMXCapDefault {
		t.Errorf("parseMX(-3) = %v, want %v", got, ssdpMXCapDefault)
	}
}

func TestParseMX_ZeroIsZero(t *testing.T) {
	// MX=0 is well-formed and means "reply ASAP". Distinct from
	// missing-MX (which clamps to 1 s) so callers can request immediate
	// replies. Documenting the distinction here so a future spec update
	// can change it intentionally.
	if got := parseMX("0"); got != 0 {
		t.Errorf("parseMX(0) = %v, want 0", got)
	}
}

func TestRefreshIntervalWithinJitter(t *testing.T) {
	d, err := NewDiscovery(validDiscoveryConfig())
	if err != nil {
		// Multicast bind unavailable on this host (sandboxed CI, port
		// 1900 in use, etc.). Fall back to a struct-built Discovery —
		// nextRefreshDelay only touches d.rng/d.rngMu so the
		// listen/sender sockets aren't required.
		d = newTestDiscovery(validDiscoveryConfig())
		// nextRefreshDelay needs an RNG; replicate NewDiscovery's seed.
		d.rng = newSeededRand()
	} else {
		t.Cleanup(func() { _ = d.Close() })
	}

	for i := 0; i < 100; i++ {
		got := d.nextRefreshDelay()
		if got < ssdpRefreshMin || got > ssdpRefreshMax {
			t.Errorf("nextRefreshDelay #%d = %v; want [%v, %v]", i, got, ssdpRefreshMin, ssdpRefreshMax)
		}
	}
}

// ----- handleMSearch / randomDelayUpTo / SERVER token -----

// fakePacketWriter is a packetWriter that captures every WriteTo call.
// Tests assert on the captured payloads and destinations to verify
// reply content + unicast routing without binding any UDP sockets.
type fakePacketWriter struct {
	mu      sync.Mutex
	writes  []fakeWrite
	closed  bool
	closeOK bool
}

type fakeWrite struct {
	body []byte
	addr net.Addr
}

func (f *fakePacketWriter) WriteTo(b []byte, addr net.Addr) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Copy the body — handleMSearch may reuse buffers. Today's
	// implementation builds a fresh []byte per write, but defensive
	// copying makes the assertion code robust to a future change.
	cp := make([]byte, len(b))
	copy(cp, b)
	f.writes = append(f.writes, fakeWrite{body: cp, addr: addr})
	return len(b), nil
}

func (f *fakePacketWriter) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	if f.closeOK {
		return nil
	}
	return nil
}

func (f *fakePacketWriter) snapshotWrites() []fakeWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeWrite, len(f.writes))
	copy(out, f.writes)
	return out
}

// newHandleMSearchTestDiscovery builds a Discovery with a fake sender
// and a deterministic RNG so the per-USN delay/stagger arithmetic is
// observable in the assertions. wg is included so the in-flight
// goroutines spawned by handleMSearch can be drained.
func newHandleMSearchTestDiscovery(t *testing.T) (*Discovery, *fakePacketWriter) {
	t.Helper()
	cfg := validDiscoveryConfig()
	d := newTestDiscovery(cfg)
	// rng must be non-nil — handleMSearch calls randomDelayUpTo. Seed
	// with a fixed value so any timing assertions can rely on
	// reproducibility (though the current tests don't time-assert).
	d.rng = rand.New(rand.NewSource(42))
	d.stop = make(chan struct{})
	fw := &fakePacketWriter{}
	d.sender = fw
	return d, fw
}

// drainHandleMSearch waits for handleMSearch's worker goroutines to
// finish via Discovery.wg, then signals stop so any laggers exit.
// Returns once the WaitGroup count hits zero or the timeout fires.
func drainHandleMSearch(t *testing.T, d *Discovery, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		// Signal stop so any reply goroutines blocked on time.After
		// exit. They check stop in their select.
		close(d.stop)
		select {
		case <-done:
		case <-time.After(timeout):
			t.Fatal("handleMSearch goroutines did not drain")
		}
	}
}

func TestHandleMSearch_AllMatchesAllTargets(t *testing.T) {
	d, fw := newHandleMSearchTestDiscovery(t)
	src := &net.UDPAddr{IP: net.ParseIP("192.168.1.99"), Port: 1900}

	// MX="0" yields an immediate-reply ceiling; replies fire after only
	// the per-USN stagger which is at most 5*ssdpUSNStagger == 250ms.
	d.handleMSearch("ssdp:all", "0", src)
	drainHandleMSearch(t, d, 1*time.Second)

	writes := fw.snapshotWrites()
	if len(writes) != len(d.targets) {
		t.Fatalf("ssdp:all produced %d replies; want %d (one per target)",
			len(writes), len(d.targets))
	}
	// Every reply must be unicast to src (not the multicast group).
	for i, w := range writes {
		if got, want := w.addr.String(), src.String(); got != want {
			t.Errorf("reply[%d] addr = %q, want %q (unicast)", i, got, want)
		}
		if !strings.HasPrefix(string(w.body), "HTTP/1.1 200 OK") {
			t.Errorf("reply[%d] not an HTTP 200 OK reply: %q", i, snippet(string(w.body)))
		}
	}
}

func TestHandleMSearch_SpecificSTMatchesSingleTarget(t *testing.T) {
	d, fw := newHandleMSearchTestDiscovery(t)
	src := &net.UDPAddr{IP: net.ParseIP("192.168.1.99"), Port: 1900}

	d.handleMSearch("urn:schemas-upnp-org:device:MediaRenderer:1", "0", src)
	drainHandleMSearch(t, d, 1*time.Second)

	writes := fw.snapshotWrites()
	if len(writes) != 1 {
		t.Fatalf("MediaRenderer M-SEARCH produced %d replies; want 1", len(writes))
	}
	body := string(writes[0].body)
	if !strings.Contains(body, "ST: urn:schemas-upnp-org:device:MediaRenderer:1\r\n") {
		t.Errorf("reply ST mismatch:\n%s", snippet(body))
	}
}

func TestHandleMSearch_NoMatchYieldsNoReply(t *testing.T) {
	d, fw := newHandleMSearchTestDiscovery(t)
	src := &net.UDPAddr{IP: net.ParseIP("192.168.1.99"), Port: 1900}

	d.handleMSearch("urn:schemas-upnp-org:service:DoesNotExist:1", "0", src)
	drainHandleMSearch(t, d, 500*time.Millisecond)

	writes := fw.snapshotWrites()
	if len(writes) != 0 {
		t.Errorf("non-matching ST produced %d replies; want 0\nwrites: %+v",
			len(writes), writes)
	}
}

func TestHandleMSearch_ReplyAddrIsUnicastSource(t *testing.T) {
	d, fw := newHandleMSearchTestDiscovery(t)
	src := &net.UDPAddr{IP: net.ParseIP("10.0.0.42"), Port: 53412}

	d.handleMSearch("upnp:rootdevice", "0", src)
	drainHandleMSearch(t, d, 500*time.Millisecond)

	writes := fw.snapshotWrites()
	if len(writes) != 1 {
		t.Fatalf("rootdevice produced %d replies; want 1", len(writes))
	}
	got, _ := writes[0].addr.(*net.UDPAddr)
	if got == nil {
		t.Fatalf("reply addr is %T, want *net.UDPAddr", writes[0].addr)
	}
	if !got.IP.Equal(src.IP) || got.Port != src.Port {
		t.Errorf("reply addr = %v, want %v (unicast back to source)", got, src)
	}
}

func TestRandomDelayUpTo_ZeroCeilingReturnsZero(t *testing.T) {
	d := newTestDiscovery(validDiscoveryConfig())
	d.rng = rand.New(rand.NewSource(1))
	if got := d.randomDelayUpTo(0); got != 0 {
		t.Errorf("randomDelayUpTo(0) = %v, want 0", got)
	}
	if got := d.randomDelayUpTo(-1 * time.Second); got != 0 {
		t.Errorf("randomDelayUpTo(-1s) = %v, want 0", got)
	}
}

func TestRandomDelayUpTo_BoundedBelowCeiling(t *testing.T) {
	d := newTestDiscovery(validDiscoveryConfig())
	d.rng = rand.New(rand.NewSource(7))

	const ceiling = 1 * time.Second
	for i := 0; i < 100; i++ {
		got := d.randomDelayUpTo(ceiling)
		if got < 0 || got > ceiling {
			t.Errorf("iter %d: randomDelayUpTo(%v) = %v; want [0, %v]",
				i, ceiling, got, ceiling)
		}
	}
}

func TestBuildServerToken_VersionThreaded(t *testing.T) {
	cases := []struct {
		version string
		want    string
	}{
		{"1.2.3", "MiSTerGroovyRelay/1.2.3 UPnP/1.0 MiSTer-DLNA/1.0"},
		{"", "MiSTerGroovyRelay/0.0.0 UPnP/1.0 MiSTer-DLNA/1.0"},
		{"  ", "MiSTerGroovyRelay/0.0.0 UPnP/1.0 MiSTer-DLNA/1.0"},
		{"v0.9.1-beta", "MiSTerGroovyRelay/v0.9.1-beta UPnP/1.0 MiSTer-DLNA/1.0"},
	}
	for _, tc := range cases {
		if got := buildServerToken(tc.version); got != tc.want {
			t.Errorf("buildServerToken(%q) = %q, want %q", tc.version, got, tc.want)
		}
	}
}

func TestNotify_EmbedsCustomServerToken(t *testing.T) {
	cfg := validDiscoveryConfig()
	cfg.ServerToken = "MiSTerGroovyRelay/9.9.9 UPnP/1.0 MiSTer-DLNA/1.0"
	d := newTestDiscovery(cfg)
	body := string(d.buildNotify("ssdp:alive", "upnp:rootdevice",
		"uuid:abcdef01-2345-6789-abcd-ef0123456789::upnp:rootdevice"))
	if !strings.Contains(body, "SERVER: MiSTerGroovyRelay/9.9.9 UPnP/1.0 MiSTer-DLNA/1.0\r\n") {
		t.Errorf("alive NOTIFY missing custom SERVER header\nbody:\n%s", body)
	}
}

func TestSearchResponse_EmbedsCustomServerToken(t *testing.T) {
	cfg := validDiscoveryConfig()
	cfg.ServerToken = "MiSTerGroovyRelay/2.0.0 UPnP/1.0 MiSTer-DLNA/1.0"
	d := newTestDiscovery(cfg)
	body := string(d.buildSearchResponse("upnp:rootdevice",
		"uuid:abcdef01-2345-6789-abcd-ef0123456789::upnp:rootdevice"))
	if !strings.Contains(body, "SERVER: MiSTerGroovyRelay/2.0.0 UPnP/1.0 MiSTer-DLNA/1.0\r\n") {
		t.Errorf("search response missing custom SERVER header\nbody:\n%s", body)
	}
}

// snippet returns the first 200 bytes of s for failure messages.
func snippet(s string) string {
	const n = 200
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// ----- helpers -----

// matchedNTs returns the NT field of every target whose matches(st) is
// true. Used for table-driven assertions on the M-SEARCH selector.
func matchedNTs(targets []ssdpTarget, st string) []string {
	var out []string
	for _, t := range targets {
		if t.matches(st) {
			out = append(out, t.nt)
		}
	}
	return out
}

func assertEqualStringSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("got %d entries (%v), want %d (%v)", len(got), got, len(want), want)
		return
	}
	gotSet := make(map[string]bool, len(got))
	for _, s := range got {
		gotSet[s] = true
	}
	for _, w := range want {
		if !gotSet[w] {
			t.Errorf("missing %q in got=%v", w, got)
		}
	}
}
