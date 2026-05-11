package dlna

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestPrepareHLSPlaybackGeneratedFilenamesStayUnderTempRoot(t *testing.T) {
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
..%2f..%2fescape.ts
#EXT-X-ENDLIST
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u8":
			_, _ = w.Write([]byte(playlist))
		case "/escape.ts":
			_, _ = w.Write([]byte("escaped"))
		default:
			if strings.Contains(strings.ToLower(r.URL.EscapedPath()), "..%2f") {
				_, _ = w.Write([]byte("escaped"))
				return
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, nil, srv.URL)

	playback, err := prepareHLSPlayback(context.Background(), srv.URL+"/playlist.m3u8", PolicyPrivateOnly)
	if err != nil {
		t.Fatalf("prepareHLSPlayback err = %v, want nil", err)
	}
	t.Cleanup(func() { _ = playback.Cleanup() })

	entries, err := os.ReadDir(playback.tempDir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected cached files")
	}
	cleanRoot := filepath.Clean(playback.tempDir)
	for _, entry := range entries {
		name := entry.Name()
		if !hlsCacheNameRE.MatchString(name) {
			t.Fatalf("cache name %q does not match fixed regex", name)
		}
		path := filepath.Join(playback.tempDir, name)
		cleanPath := filepath.Clean(path)
		if cleanPath != cleanRoot && !strings.HasPrefix(cleanPath, cleanRoot+string(os.PathSeparator)) {
			t.Fatalf("cache path escaped temp root: %s", cleanPath)
		}
	}
	manifest := readCachedManifest(t, playback)
	if strings.Contains(manifest, "escape.ts") || strings.Contains(manifest, "..") {
		t.Fatalf("manifest leaked unsafe source basename/path:\n%s", manifest)
	}
}

func TestPrepareHLSPlaybackRejectsNestedLoopbackChildURL(t *testing.T) {
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
http://loopback.invalid/seg.ts
#EXT-X-ENDLIST
`
	assertPrepareHLSRejectsPlaylistWithDNS(t, playlist, map[string]string{
		"loopback.invalid": "127.0.0.1",
	})
}

func TestPrepareHLSPlaybackRejectsFileChildBeforeLocalManifestWrittenAndNoTempLeak(t *testing.T) {
	before := snapshotHLSTempDirs(t)
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
file:///etc/passwd
#EXT-X-ENDLIST
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(playlist))
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, nil, srv.URL)

	playback, err := prepareHLSPlayback(context.Background(), srv.URL, PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if playback.PlaybackURI != "" {
		t.Fatalf("PlaybackURI = %q, want empty", playback.PlaybackURI)
	}
	if !errors.Is(err, errHLSInvalid) {
		t.Fatalf("err = %v, want errHLSInvalid", err)
	}
	assertNoNewHLSTempDirs(t, before)
}

func TestPrepareHLSPlaybackRejectsManifestLikeMediaSegment(t *testing.T) {
	for _, segment := range []string{"nested.m3u8", "manifest.mpd"} {
		t.Run(segment, func(t *testing.T) {
			before := snapshotHLSTempDirs(t)
			playlist := "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\n" + segment + "\n#EXT-X-ENDLIST\n"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/playlist.m3u8":
					_, _ = w.Write([]byte(playlist))
				case "/" + segment:
					_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-ENDLIST\n"))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			installHLSTestNetwork(t, nil, srv.URL)

			_, err := prepareHLSPlayback(context.Background(), srv.URL+"/playlist.m3u8", PolicyPrivateOnly)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, errHLSInvalid) {
				t.Fatalf("err = %v, want errHLSInvalid", err)
			}
			assertNoNewHLSTempDirs(t, before)
		})
	}
}

func TestPrepareHLSPlaybackRejectsChildRedirectToLinkLocal(t *testing.T) {
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
redir.ts
#EXT-X-ENDLIST
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u8":
			_, _ = w.Write([]byte(playlist))
		case "/redir.ts":
			http.Redirect(w, r, "http://metadata.invalid/latest", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, map[string]string{
		"metadata.invalid": "169.254.169.254",
	}, srv.URL)

	_, err := prepareHLSPlayback(context.Background(), srv.URL+"/playlist.m3u8", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, errHLSInvalid) {
		t.Fatalf("err = %v, want errHLSInvalid", err)
	}
}

func TestPrepareHLSPlaybackRejectsGETTimeDNSRebindingToLoopback(t *testing.T) {
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`
	var lookups int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u8":
			_, _ = w.Write([]byte(playlist))
		case "/seg.ts":
			_, _ = w.Write([]byte("should-not-cache"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	host := u.Hostname()
	t.Cleanup(installResolverOverride(t, func(ctx context.Context, q string) ([]net.IP, error) {
		if q == host {
			lookups++
			if lookups <= 4 {
				return []net.IP{net.ParseIP("192.168.99.1")}, nil
			}
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		return hostMappingResolver(t, nil)(ctx, q)
	}))
	prev := hlsNetDialContext
	hlsNetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, u.Host)
	}
	t.Cleanup(func() { hlsNetDialContext = prev })

	_, err = prepareHLSPlayback(context.Background(), srv.URL+"/playlist.m3u8", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, errHLSInvalid) {
		t.Fatalf("err = %v, want errHLSInvalid", err)
	}
}

func TestPrepareHLSPlaybackDoesNotIssuePreflightHEAD(t *testing.T) {
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`
	var headSeen bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			headSeen = true
			w.WriteHeader(http.StatusOK)
			return
		}
		switch r.URL.Path {
		case "/playlist.m3u8":
			_, _ = w.Write([]byte(playlist))
		case "/seg.ts":
			_, _ = w.Write([]byte("segment"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, nil, srv.URL)

	playback, err := prepareHLSPlayback(context.Background(), srv.URL+"/playlist.m3u8", PolicyPrivateOnly)
	if err != nil {
		t.Fatalf("prepareHLSPlayback err = %v, want nil", err)
	}
	t.Cleanup(func() { _ = playback.Cleanup() })
	if headSeen {
		t.Fatal("prepareHLSPlayback issued a preflight HEAD outside the hardened HLS GET transport")
	}
}

func TestPrepareHLSPlaybackRejectsPublicChildUnderPrivatePolicy(t *testing.T) {
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
http://public.invalid/seg.ts
#EXT-X-ENDLIST
`
	assertPrepareHLSRejectsPlaylistWithDNS(t, playlist, map[string]string{
		"public.invalid": "8.8.8.8",
	})
}

func TestPrepareHLSPlaybackCleansTempDirOnValidatorError(t *testing.T) {
	before := snapshotHLSTempDirs(t)
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
http://public.invalid/seg.ts
#EXT-X-ENDLIST
`
	assertPrepareHLSRejectsPlaylistWithDNS(t, playlist, map[string]string{
		"public.invalid": "8.8.8.8",
	})
	assertNoNewHLSTempDirs(t, before)
}

func assertPrepareHLSRejectsPlaylistWithDNS(t *testing.T, playlist string, mapping map[string]string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/playlist.m3u8" || r.URL.Path == "/" {
			_, _ = w.Write([]byte(playlist))
			return
		}
		_, _ = w.Write([]byte("segment"))
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, mapping, srv.URL)

	_, err := prepareHLSPlayback(context.Background(), srv.URL+"/playlist.m3u8", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, errHLSInvalid) {
		t.Fatalf("err = %v, want errHLSInvalid", err)
	}
}

func snapshotHLSTempDirs(t *testing.T) map[string]struct{} {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "mister-groovyrelay-dlna-hls-*"))
	if err != nil {
		t.Fatalf("glob hls temp dirs: %v", err)
	}
	out := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		out[match] = struct{}{}
	}
	return out
}

func assertNoNewHLSTempDirs(t *testing.T, before map[string]struct{}) {
	t.Helper()
	after := snapshotHLSTempDirs(t)
	var leaked []string
	for path := range after {
		if _, ok := before[path]; !ok {
			leaked = append(leaked, path)
		}
	}
	sort.Strings(leaked)
	if len(leaked) > 0 {
		t.Fatalf("new HLS temp dirs leaked: %v", leaked)
	}
}
