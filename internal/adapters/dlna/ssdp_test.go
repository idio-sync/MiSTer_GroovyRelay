package dlna

import (
	"strings"
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
func newTestDiscovery(cfg DiscoveryConfig) *Discovery {
	return &Discovery{
		cfg:     cfg,
		targets: newTargets(cfg.DeviceUUID),
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
