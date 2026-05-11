package dlna

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// set_avtransport_uri_test.go covers the P2.3 SetAVTransportURI handler:
// argument extraction, validator integration, busy-reject on
// startInFlight, metadata parse + MIME cross-check, and the lastError
// clear semantics.
//
// Tests use the runAVT helper from avtransport_test.go for its envelope
// scaffolding when they don't need to bypass the validator's address
// classification. Tests that need the validator's redirect chase use a
// custom adapter constructor that injects a hostMappingResolver via a
// new helper (see avtWithStubResolver below).

// avtWithLoopbackResolver constructs an Adapter wired to accept the
// httptest.NewServer loopback host as a private RFC1918 address. The
// validator's parseAndClassify still does real classification, but
// the resolver lies about the IP so the loopback-rejection rule
// doesn't kill every redirect-chase test.
//
// Returns the adapter (with cfg.Enabled=true) and a function that
// sets the dnsResolverFunc on the validator created INSIDE
// validateMediaURL. Achieved by overriding the package-private
// validateMediaURL via a function variable, so we don't have to
// thread a resolver through every public entry point. See
// makeStubValidator for the implementation seam.
//
// Returns the adapter pre-configured with Enabled=true and
// AllowPublicSourceURLs=false (the spec default).
func avtWithLoopbackResolver(t *testing.T) *Adapter {
	t.Helper()
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)
	return a
}

// avtSendSetURI sends a SetAVTransportURI SOAP request through
// handleAVTransportSOAP and returns the recorder. argsXML is the
// inner argument fragment.
func avtSendSetURI(t *testing.T, a *Adapter, argsXML string) *httptest.ResponseRecorder {
	t.Helper()
	req, rr := avtSOAPRequest(t, "SetAVTransportURI", argsXML)
	a.handleAVTransportSOAP(rr, req)
	return rr
}

func hlsDIDL(protocolInfo string) string {
	return fmt.Sprintf(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><item><dc:title>HLS</dc:title><upnp:class>object.item.videoItem</upnp:class><res protocolInfo=%q>http://x</res></item></DIDL-Lite>`, protocolInfo)
}

func newHLSTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u8", "/one.m3u8", "/two.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\nseg.ts\n#EXT-X-ENDLIST\n")
		case "/seg.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			_, _ = w.Write([]byte("segment-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, nil, srv.URL)
	return srv
}

func sendHLSSetURI(t *testing.T, a *Adapter, rawURL string) *httptest.ResponseRecorder {
	t.Helper()
	return avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s</CurrentURI>`+
			`<CurrentURIMetaData>%s</CurrentURIMetaData>`,
		html.EscapeString(rawURL),
		html.EscapeString(hlsDIDL("http-get:*:application/vnd.apple.mpegurl:*")),
	))
}

// Each top-level test installs its own httptest server AND injects a
// resolver mapping the server's hostname to a private IP via
// installResolverOverride (testhelpers_resolver_test.go). The
// SetAVTransportURI handler invokes validateMediaURL through the
// package-level defaultDNSResolver seam.

// ---- AcceptsValidPrivateURL ----

func TestSetAVTransportURI_AcceptsValidPrivateURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	a := avtWithLoopbackResolver(t)
	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
	))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loadedURI == "" {
		t.Errorf("loadedURI empty after success; want srv URL")
	}
	if !strings.HasPrefix(a.loadedURI, srv.URL) {
		t.Errorf("loadedURI = %q, want prefix %q", a.loadedURI, srv.URL)
	}
	if a.loadedPlaybackURI != a.loadedURI {
		t.Errorf("loadedPlaybackURI = %q, want loadedURI %q for direct HTTP", a.loadedPlaybackURI, a.loadedURI)
	}
	if !a.loadedCanSeek {
		t.Error("loadedCanSeek = false, want true for direct HTTP")
	}
	if a.lastError != "" {
		t.Errorf("lastError = %q, want empty after success", a.lastError)
	}
}

// ---- StoresFinalURLAfterRedirect ----

func TestSetAVTransportURI_StoresFinalURLAfterRedirect(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(final.Close)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/final.mp4", http.StatusFound)
	}))
	t.Cleanup(redir.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	a := avtWithLoopbackResolver(t)
	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/start.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`,
		html.EscapeString(redir.URL),
	))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	a.mu.Lock()
	loaded := a.loadedURI
	a.mu.Unlock()
	if !strings.HasPrefix(loaded, final.URL) {
		t.Errorf("loadedURI = %q, want final-server prefix %q (validator must store post-redirect URL)",
			loaded, final.URL)
	}
}

// ---- RejectsBadInstanceID ----

func TestSetAVTransportURI_RejectsBadInstanceID(t *testing.T) {
	a := avtWithLoopbackResolver(t)
	rr := avtSendSetURI(t, a,
		`<InstanceID>1</InstanceID><CurrentURI>http://example.com/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`)
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>718</errorCode>") {
		t.Errorf("body missing errorCode 718: %s", rr.Body.String())
	}
}

// ---- RejectsBadScheme ----

func TestSetAVTransportURI_RejectsBadScheme(t *testing.T) {
	a := avtWithLoopbackResolver(t)
	rr := avtSendSetURI(t, a,
		`<InstanceID>0</InstanceID><CurrentURI>file:///etc/passwd</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`)
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>716</errorCode>") {
		t.Errorf("body missing errorCode 716: %s", rr.Body.String())
	}
	// Failure should set lastError so GetTransportInfo reports
	// ERROR_OCCURRED.
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastError == "" {
		t.Error("lastError empty after rejected SetAVTransportURI; want redacted message")
	}
	if strings.Contains(a.lastError, "/etc/passwd") {
		t.Errorf("lastError leaks raw URL path: %q", a.lastError)
	}
	if a.loadedURI != "" {
		t.Errorf("loadedURI = %q after rejection; want empty", a.loadedURI)
	}
}

// ---- RejectsLoopback ----

func TestSetAVTransportURI_RejectsLoopback(t *testing.T) {
	t.Cleanup(installResolverOverride(t, staticResolver("127.0.0.1")))
	a := avtWithLoopbackResolver(t)
	rr := avtSendSetURI(t, a,
		`<InstanceID>0</InstanceID><CurrentURI>http://localhost/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`)
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>716</errorCode>") {
		t.Errorf("body missing errorCode 716: %s", rr.Body.String())
	}
}

// ---- RejectsPublicWhenAllowPublicFalse ----

func TestSetAVTransportURI_RejectsPublicWhenAllowPublicFalse(t *testing.T) {
	t.Cleanup(installResolverOverride(t, staticResolver("8.8.8.8")))
	a := avtWithLoopbackResolver(t)
	a.mu.Lock()
	a.cfg.AllowPublicSourceURLs = false
	a.mu.Unlock()

	rr := avtSendSetURI(t, a,
		`<InstanceID>0</InstanceID><CurrentURI>http://public.example/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`)
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>716</errorCode>") {
		t.Errorf("body missing errorCode 716: %s", rr.Body.String())
	}
}

// ---- AcceptsPublicWhenAllowPublicTrue ----

func TestSetAVTransportURI_AcceptsPublicWhenAllowPublicTrue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	// Resolver classifies srv host as public so we exercise the "public,
	// AllowPublic=true" code path. The actual httptest dial still goes
	// to loopback because the URL string carries the loopback host:port —
	// the validator only consults the resolver for classification.
	srvHost := strings.TrimPrefix(srv.URL, "http://")
	if i := strings.IndexByte(srvHost, ':'); i >= 0 {
		srvHost = srvHost[:i]
	}
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, map[string]string{
		srvHost: "8.8.8.8",
	})))

	a := avtWithLoopbackResolver(t)
	a.mu.Lock()
	a.cfg.AllowPublicSourceURLs = true
	a.mu.Unlock()

	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
	))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// ---- RejectsBusyDuringStartInFlight ----

func TestSetAVTransportURI_RejectsBusyDuringStartInFlight(t *testing.T) {
	a := avtWithLoopbackResolver(t)
	// Pre-populate loadedURI so we can confirm it's untouched.
	a.mu.Lock()
	a.loadedURI = "http://prior.local/already.mp4"
	a.startInFlight = true
	a.mu.Unlock()

	rr := avtSendSetURI(t, a,
		`<InstanceID>0</InstanceID><CurrentURI>http://192.168.50.50/new.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`)
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loadedURI != "http://prior.local/already.mp4" {
		t.Errorf("loadedURI mutated on busy-reject: got %q, want %q",
			a.loadedURI, "http://prior.local/already.mp4")
	}
}

func TestSetAVTransportURI_RechecksBusyAfterValidation(t *testing.T) {
	a := avtWithLoopbackResolver(t)
	a.mu.Lock()
	a.loadedURI = "http://prior.local/already.mp4"
	a.loadedMetaRaw = "<prior/>"
	a.mu.Unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.startInFlight = true
		a.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/new.mp4</CurrentURI><CurrentURIMetaData><new/></CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
	))
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loadedURI != "http://prior.local/already.mp4" {
		t.Errorf("loadedURI mutated after post-validation busy state: got %q", a.loadedURI)
	}
	if a.loadedMetaRaw != "<prior/>" {
		t.Errorf("loadedMetaRaw mutated after post-validation busy state: got %q", a.loadedMetaRaw)
	}
}

// ---- RejectsMalformedMetadata ----

func TestSetAVTransportURI_RejectsMalformedMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	a := avtWithLoopbackResolver(t)
	a.mu.Lock()
	a.loadedURI = "http://prior.local/already.mp4"
	a.mu.Unlock()

	// Garbled XML — opens an element that never closes. parseDIDLMetadata
	// returns ErrInvalidMetadata, which the handler maps to 402.
	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.mp4</CurrentURI>`+
			`<CurrentURIMetaData>&lt;DIDL-Lite&gt;&lt;item&gt;</CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
	))
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>402</errorCode>") {
		t.Errorf("body missing errorCode 402: %s", rr.Body.String())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loadedURI != "http://prior.local/already.mp4" {
		t.Errorf("loadedURI mutated on metadata-reject: got %q, want unchanged", a.loadedURI)
	}
}

// ---- RejectsUnsupportedMIME ----

func TestSetAVTransportURI_RejectsUnsupportedMIME(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	a := avtWithLoopbackResolver(t)

	didl := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><item><dc:title>DASH</dc:title><upnp:class>object.item.videoItem</upnp:class><res protocolInfo="http-get:*:application/dash+xml:*">http://x</res></item></DIDL-Lite>`
	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.bin</CurrentURI>`+
			`<CurrentURIMetaData>%s</CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
		html.EscapeString(didl),
	))
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>714</errorCode>") {
		t.Errorf("body missing errorCode 714: %s", rr.Body.String())
	}
}

func TestSetAVTransportURI_AcceptsHLSAfterCachingChildren(t *testing.T) {
	srv := newHLSTestServer(t)
	a := avtWithLoopbackResolver(t)

	rawURL := srv.URL + "/playlist.m3u8"
	rr := sendHLSSetURI(t, a, rawURL)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	a.mu.Lock()
	loadedURI := a.loadedURI
	playbackURI := a.loadedPlaybackURI
	canSeek := a.loadedCanSeek
	a.mu.Unlock()
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(playbackURI)) })

	if loadedURI != rawURL {
		t.Errorf("loadedURI = %q, want controller-facing URL %q", loadedURI, rawURL)
	}
	if strings.HasPrefix(playbackURI, "http://") || strings.HasPrefix(playbackURI, "https://") {
		t.Fatalf("loadedPlaybackURI = %q, want local cached manifest", playbackURI)
	}
	if filepath.Ext(playbackURI) != ".m3u8" {
		t.Errorf("loadedPlaybackURI = %q, want .m3u8 manifest", playbackURI)
	}
	if _, err := os.Stat(playbackURI); err != nil {
		t.Fatalf("cached manifest missing: %v", err)
	}
	if canSeek {
		t.Error("loadedCanSeek = true, want false for cached HLS")
	}
}

func TestSetAVTransportURI_AcceptsM3U8WithEmptyMetadataAfterCachingChildren(t *testing.T) {
	srv := newHLSTestServer(t)
	a := avtWithLoopbackResolver(t)

	rawURL := srv.URL + "/playlist.m3u8"
	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`,
		html.EscapeString(rawURL),
	))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	a.mu.Lock()
	loadedURI := a.loadedURI
	playbackURI := a.loadedPlaybackURI
	canSeek := a.loadedCanSeek
	a.mu.Unlock()
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(playbackURI)) })

	if loadedURI != rawURL {
		t.Errorf("loadedURI = %q, want controller-facing URL %q", loadedURI, rawURL)
	}
	if strings.HasPrefix(playbackURI, "http://") || strings.HasPrefix(playbackURI, "https://") {
		t.Fatalf("loadedPlaybackURI = %q, want local cached manifest", playbackURI)
	}
	if filepath.Ext(playbackURI) != ".m3u8" {
		t.Errorf("loadedPlaybackURI = %q, want .m3u8 manifest", playbackURI)
	}
	if canSeek {
		t.Error("loadedCanSeek = true, want false for URL-classified HLS")
	}
}

func TestSetAVTransportURI_RejectsMPDWithEmptyMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/dash+xml")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	a := avtWithLoopbackResolver(t)
	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/manifest.mpd</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
	))
	if rr.Code != 500 {
		t.Fatalf("status = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>714</errorCode>") {
		t.Fatalf("body missing errorCode 714: %s", rr.Body.String())
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loadedURI != "" {
		t.Errorf("loadedURI = %q after MPD rejection; want empty", a.loadedURI)
	}
	if a.loadedPlaybackURI != "" {
		t.Errorf("loadedPlaybackURI = %q after MPD rejection; want empty", a.loadedPlaybackURI)
	}
}

func TestSetAVTransportURI_BackToBackHLSCleansOldCache(t *testing.T) {
	srv := newHLSTestServer(t)
	a := avtWithLoopbackResolver(t)

	rr1 := sendHLSSetURI(t, a, srv.URL+"/one.m3u8")
	if rr1.Code != 200 {
		t.Fatalf("first status = %d, want 200; body=%s", rr1.Code, rr1.Body.String())
	}
	a.mu.Lock()
	firstPlayback := a.loadedPlaybackURI
	a.mu.Unlock()
	firstDir := filepath.Dir(firstPlayback)
	if _, err := os.Stat(firstPlayback); err != nil {
		t.Fatalf("first cached manifest missing before replacement: %v", err)
	}

	rr2 := sendHLSSetURI(t, a, srv.URL+"/two.m3u8")
	if rr2.Code != 200 {
		t.Fatalf("second status = %d, want 200; body=%s", rr2.Code, rr2.Body.String())
	}
	a.mu.Lock()
	secondPlayback := a.loadedPlaybackURI
	a.mu.Unlock()
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(secondPlayback)) })

	if secondPlayback == firstPlayback {
		t.Fatalf("second playback URI reused first cache path %q", secondPlayback)
	}
	if _, err := os.Stat(firstDir); !os.IsNotExist(err) {
		t.Fatalf("old HLS cache dir still present after replacement; stat err=%v", err)
	}
	if _, err := os.Stat(secondPlayback); err != nil {
		t.Fatalf("second cached manifest missing: %v", err)
	}
}

func TestSetAVTransportURI_StopCleansLoadedHLSCache(t *testing.T) {
	srv := newHLSTestServer(t)
	a := avtWithLoopbackResolver(t)

	rr := sendHLSSetURI(t, a, srv.URL+"/playlist.m3u8")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	a.mu.Lock()
	playbackURI := a.loadedPlaybackURI
	loadedURI := a.loadedURI
	a.mu.Unlock()
	cacheDir := filepath.Dir(playbackURI)

	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("HLS cache dir still present after Stop; stat err=%v", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loadedURI != loadedURI {
		t.Errorf("loadedURI = %q after Stop, want preserved %q", a.loadedURI, loadedURI)
	}
	if a.loadedPlaybackURI != "" {
		t.Errorf("loadedPlaybackURI = %q after Stop, want cleared", a.loadedPlaybackURI)
	}
}

func TestSetAVTransportURI_RejectsWhileHLSPlayingKeepsActiveCache(t *testing.T) {
	srv := newHLSTestServer(t)
	fake := &captureSessionManager{}
	cfg := validAdapterConfig()
	cfg.Core = fake
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.SetEnabled(true)

	rrSet := sendHLSSetURI(t, a, srv.URL+"/one.m3u8")
	if rrSet.Code != 200 {
		t.Fatalf("initial SetAVTransportURI status = %d, want 200; body=%s", rrSet.Code, rrSet.Body.String())
	}
	a.mu.Lock()
	activePlaybackURI := a.loadedPlaybackURI
	a.mu.Unlock()
	activeCacheDir := filepath.Dir(activePlaybackURI)

	rrPlay := avtSendPlay(t, a, "<InstanceID>0</InstanceID><Speed>1</Speed>")
	if rrPlay.Code != 200 {
		t.Fatalf("Play status = %d, want 200; body=%s", rrPlay.Code, rrPlay.Body.String())
	}
	onStop := fake.lastReq().OnStop
	if onStop == nil {
		t.Fatal("captured OnStop nil")
	}

	rrReplace := sendHLSSetURI(t, a, srv.URL+"/two.m3u8")
	if rrReplace.Code != 500 {
		t.Fatalf("replacement SetAVTransportURI status = %d, want 500; body=%s", rrReplace.Code, rrReplace.Body.String())
	}
	if !strings.Contains(rrReplace.Body.String(), "<errorCode>701</errorCode>") {
		t.Fatalf("replacement body missing errorCode 701: %s", rrReplace.Body.String())
	}
	if _, err := os.Stat(activePlaybackURI); err != nil {
		t.Fatalf("active HLS cache was removed by rejected replacement: %v", err)
	}

	onStop("stopped")
	if _, err := os.Stat(activeCacheDir); !os.IsNotExist(err) {
		t.Fatalf("active HLS cache dir still present after OnStop; stat err=%v", err)
	}
}

// ---- AcceptsKnownMIME ----

func TestSetAVTransportURI_AcceptsKnownMIME(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	a := avtWithLoopbackResolver(t)

	// video/mp4 is in sinkProtocolInfoEntries.
	didl := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><item><dc:title>BBB</dc:title><upnp:class>object.item.videoItem.movie</upnp:class><res protocolInfo="http-get:*:video/mp4:*" duration="00:09:56">http://x</res></item></DIDL-Lite>`
	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.mp4</CurrentURI>`+
			`<CurrentURIMetaData>%s</CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
		html.EscapeString(didl),
	))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loadedMeta.ProtocolInfo != "http-get:*:video/mp4:*" {
		t.Errorf("loadedMeta.ProtocolInfo = %q, want %q",
			a.loadedMeta.ProtocolInfo, "http-get:*:video/mp4:*")
	}
}

// ---- AcceptsEmptyMetadata ----

func TestSetAVTransportURI_AcceptsEmptyMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	a := avtWithLoopbackResolver(t)
	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
	))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// ---- ClearsLastError ----

func TestSetAVTransportURI_ClearsLastError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	a := avtWithLoopbackResolver(t)
	a.mu.Lock()
	a.lastError = "previous failure"
	a.mu.Unlock()

	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
	))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastError != "" {
		t.Errorf("lastError = %q after success, want empty", a.lastError)
	}
}

// ---- RejectsWhenDisabled ----

func TestSetAVTransportURI_RejectsWhenDisabled(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Default cfg.Enabled=false.
	rr := avtSendSetURI(t, a,
		`<InstanceID>0</InstanceID><CurrentURI>http://192.168.1.1/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`)
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	// Disabled returns 701 — see set_avtransport_uri.go's rationale.
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing errorCode 701: %s", rr.Body.String())
	}
	// Disabled rejection MUST NOT touch lastError (it's a configuration
	// state, not a validation observation).
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastError != "" {
		t.Errorf("lastError = %q after disabled-reject, want empty", a.lastError)
	}
}

// ---- Query-action visibility post-SetAVTransportURI ----

func TestGetMediaInfo_AfterSetAVTransportURI_ReflectsStoredURI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	a := avtWithLoopbackResolver(t)
	didl := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><item><dc:title>BBB</dc:title><upnp:class>object.item.videoItem.movie</upnp:class><res protocolInfo="http-get:*:video/mp4:*" duration="00:09:56">http://x</res></item></DIDL-Lite>`
	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.mp4</CurrentURI>`+
			`<CurrentURIMetaData>%s</CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
		html.EscapeString(didl),
	))
	if rr.Code != 200 {
		t.Fatalf("SetAVTransportURI status = %d, body=%s", rr.Code, rr.Body.String())
	}

	// Query GetMediaInfo and assert real values surface.
	req2, rr2 := avtSOAPRequest(t, "GetMediaInfo", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr2, req2)
	body := rr2.Body.String()
	for _, want := range []string{
		"<NrTracks>1</NrTracks>",
		"<MediaDuration>00:09:56</MediaDuration>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody:\n%s", want, body)
		}
	}
	// CurrentURI must be the stored loadedURI (the validator's FinalURL
	// — same as srv.URL+/v.mp4 here since no redirect happened).
	if !strings.Contains(body, srv.URL) {
		t.Errorf("body missing srv URL %q\nbody:\n%s", srv.URL, body)
	}
}

func TestGetPositionInfo_AfterSetAVTransportURI_ReflectsStoredURI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, nil)))

	a := avtWithLoopbackResolver(t)
	rr := avtSendSetURI(t, a, fmt.Sprintf(
		`<InstanceID>0</InstanceID><CurrentURI>%s/v.mp4</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`,
		html.EscapeString(srv.URL),
	))
	if rr.Code != 200 {
		t.Fatalf("SetAVTransportURI status = %d", rr.Code)
	}

	req2, rr2 := avtSOAPRequest(t, "GetPositionInfo", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr2, req2)
	body := rr2.Body.String()
	if !strings.Contains(body, "<Track>1</Track>") {
		t.Errorf("body missing Track=1: %s", body)
	}
	if !strings.Contains(body, srv.URL) {
		t.Errorf("body missing TrackURI containing %q: %s", srv.URL, body)
	}
}

func TestGetTransportInfo_StatusOK_NoError(t *testing.T) {
	a := avtWithLoopbackResolver(t)
	// Fresh state (no SetAVTransportURI yet, no lastError) → OK.
	req, rr := avtSOAPRequest(t, "GetTransportInfo", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr, req)
	if !strings.Contains(rr.Body.String(), "<CurrentTransportStatus>OK</CurrentTransportStatus>") {
		t.Errorf("body missing OK status: %s", rr.Body.String())
	}
}

func TestGetTransportInfo_StatusError_AfterValidationFailure(t *testing.T) {
	a := avtWithLoopbackResolver(t)
	// Send a bad-scheme URI to trigger the lastError set path.
	rr := avtSendSetURI(t, a,
		`<InstanceID>0</InstanceID><CurrentURI>file:///etc/passwd</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`)
	if rr.Code != 500 {
		t.Fatalf("expected validation rejection, got %d", rr.Code)
	}

	req2, rr2 := avtSOAPRequest(t, "GetTransportInfo", "<InstanceID>0</InstanceID>")
	a.handleAVTransportSOAP(rr2, req2)
	if !strings.Contains(rr2.Body.String(), "<CurrentTransportStatus>ERROR_OCCURRED</CurrentTransportStatus>") {
		t.Errorf("body missing ERROR_OCCURRED status: %s", rr2.Body.String())
	}
}

// ---- Lower-level helpers tests (mimeFromProtocolInfo, redactURL) ----

func TestMimeFromProtocolInfo(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http-get:*:video/mp4:*", "video/mp4"},
		{"http-get:*:VIDEO/MP4:*", "video/mp4"},
		{"http-get:*:video/mp4;DLNA.ORG_PN=AVC_MP4_BL_CIF15_AAC_520:DLNA.ORG_OP=01", "video/mp4"},
		{"too:short", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := mimeFromProtocolInfo(tc.in)
		if got != tc.want {
			t.Errorf("mimeFromProtocolInfo(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestProtocolInfoMatchesSink(t *testing.T) {
	if !protocolInfoMatchesSink("http-get:*:video/mp4:*") {
		t.Error("video/mp4 should match sink")
	}
	if !protocolInfoMatchesSink("") {
		t.Error("empty protocolInfo should match (skipped check)")
	}
	if !protocolInfoMatchesSink("http-get:*:application/vnd.apple.mpegurl:*") {
		t.Error("HLS should match sink after cached-HLS support")
	}
	if protocolInfoMatchesSink("http-get:*:application/dash+xml:*") {
		t.Error("DASH should NOT match sink")
	}
}

func TestRedactURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://user:pass@host/path", "http://host"},
		{"http://host/path?token=secret", "http://host"},
		{"https://host:8080/path?x=1", "https://host:8080"},
		{"file:///etc/passwd", "file://"},
		{"", "<empty>"},
		// url.Parse is permissive; only IPv6-malformed input fails it.
		{"http://[bad", "<unparseable>"},
	}
	for _, tc := range cases {
		got := redactURL(tc.in)
		if got != tc.want {
			t.Errorf("redactURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
