package dlna

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installHLSTestNetwork(t *testing.T, mapping map[string]string, serverURL string) {
	t.Helper()
	t.Cleanup(installResolverOverride(t, hostMappingResolver(t, mapping)))

	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	serverAddr := u.Host
	prev := hlsNetDialContext
	hlsNetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		_, serverPort, err := net.SplitHostPort(serverAddr)
		if err != nil {
			return nil, err
		}
		if port == serverPort {
			var d net.Dialer
			return d.DialContext(ctx, network, serverAddr)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
	t.Cleanup(func() {
		hlsNetDialContext = prev
	})
}

func readCachedManifest(t *testing.T, playback hlsPlayback) string {
	t.Helper()
	b, err := os.ReadFile(playback.PlaybackURI)
	if err != nil {
		t.Fatalf("read cached manifest: %v", err)
	}
	return string(b)
}

func assertNoHLSRemoteURL(t *testing.T, manifest string) {
	t.Helper()
	if strings.Contains(manifest, "http://") || strings.Contains(manifest, "https://") {
		t.Fatalf("cached manifest contains remote URL:\n%s", manifest)
	}
}

func TestPrepareHLSPlaybackCachesRelativeSegmentsToLocalFiles(t *testing.T) {
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg1.ts
#EXTINF:4,
media/seg2.ts
#EXT-X-ENDLIST
`
	segments := map[string]string{
		"/seg1.ts":       "segment-one",
		"/media/seg2.ts": "segment-two",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/playlist.m3u8" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(playlist))
			return
		}
		if body, ok := segments[r.URL.Path]; ok {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, nil, srv.URL)

	playback, err := prepareHLSPlayback(context.Background(), srv.URL+"/playlist.m3u8", PolicyPrivateOnly)
	if err != nil {
		t.Fatalf("prepareHLSPlayback err = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := playback.Cleanup(); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	})

	manifest := readCachedManifest(t, playback)
	for _, want := range []string{"segment-000001.ts", "segment-000002.ts", "#EXT-X-ENDLIST"} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("cached manifest missing %q:\n%s", want, manifest)
		}
	}
	assertNoHLSRemoteURL(t, manifest)

	for name, want := range map[string]string{
		"segment-000001.ts": "segment-one",
		"segment-000002.ts": "segment-two",
	} {
		got, err := os.ReadFile(filepath.Join(playback.tempDir, name))
		if err != nil {
			t.Fatalf("read cached segment %s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("%s bytes = %q, want %q", name, got, want)
		}
	}
}

func TestPrepareHLSPlaybackCachesInitMapAndRewritesTag(t *testing.T) {
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXT-X-MAP:URI="init.mp4"
#EXTINF:4,
seg.m4s
#EXT-X-ENDLIST
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u8":
			_, _ = w.Write([]byte(playlist))
		case "/init.mp4":
			_, _ = w.Write([]byte("init-bytes"))
		case "/seg.m4s":
			_, _ = w.Write([]byte("segment-bytes"))
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

	manifest := readCachedManifest(t, playback)
	if !strings.Contains(manifest, `#EXT-X-MAP:URI="init-000001.mp4"`) {
		t.Fatalf("cached manifest did not rewrite init map:\n%s", manifest)
	}
	assertNoHLSRemoteURL(t, manifest)
	got, err := os.ReadFile(filepath.Join(playback.tempDir, "init-000001.mp4"))
	if err != nil {
		t.Fatalf("read cached init map: %v", err)
	}
	if string(got) != "init-bytes" {
		t.Fatalf("init bytes = %q, want init-bytes", got)
	}
}

func TestPrepareHLSPlaybackPreservesLocalCommentsAndTags(t *testing.T) {
	const playlist = `#EXTM3U
# local vendor note
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	manifest := readCachedManifest(t, playback)
	for _, want := range []string{"# local vendor note", "#EXT-X-VERSION:3", "segment-000001.ts"} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("cached manifest missing %q:\n%s", want, manifest)
		}
	}
	assertNoHLSRemoteURL(t, manifest)
}

func TestPrepareHLSPlaybackCachesNestedVariantPlaylist(t *testing.T) {
	const master = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1280000
variant/child.m3u8
#EXT-X-ENDLIST
`
	const child = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			_, _ = w.Write([]byte(master))
		case "/variant/child.m3u8":
			_, _ = w.Write([]byte(child))
		case "/variant/seg.ts":
			_, _ = w.Write([]byte("nested-segment"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, nil, srv.URL)

	playback, err := prepareHLSPlayback(context.Background(), srv.URL+"/master.m3u8", PolicyPrivateOnly)
	if err != nil {
		t.Fatalf("prepareHLSPlayback err = %v, want nil", err)
	}
	t.Cleanup(func() { _ = playback.Cleanup() })

	masterManifest := readCachedManifest(t, playback)
	if !strings.Contains(masterManifest, "playlist-000001.m3u8") {
		t.Fatalf("master manifest did not reference local child playlist:\n%s", masterManifest)
	}
	assertNoHLSRemoteURL(t, masterManifest)
	childBytes, err := os.ReadFile(filepath.Join(playback.tempDir, "playlist-000001.m3u8"))
	if err != nil {
		t.Fatalf("read child playlist: %v", err)
	}
	childManifest := string(childBytes)
	if !strings.Contains(childManifest, "segment-000001.ts") {
		t.Fatalf("child manifest did not reference cached segment:\n%s", childManifest)
	}
	assertNoHLSRemoteURL(t, childManifest)
}

func TestPrepareHLSPlaybackAcceptsPublicSourceWhenPolicyAllowsIt(t *testing.T) {
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u8":
			_, _ = w.Write([]byte(playlist))
		case "/seg.ts":
			_, _ = w.Write([]byte("public-ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	installHLSTestNetwork(t, map[string]string{
		u.Hostname(): "8.8.8.8",
	}, srv.URL)

	playback, err := prepareHLSPlayback(context.Background(), srv.URL+"/playlist.m3u8", PolicyAllowPublic)
	if err != nil {
		t.Fatalf("prepareHLSPlayback err = %v, want nil", err)
	}
	t.Cleanup(func() { _ = playback.Cleanup() })

	manifest := readCachedManifest(t, playback)
	if !strings.Contains(manifest, "segment-000001.ts") {
		t.Fatalf("manifest missing cached segment:\n%s", manifest)
	}
}

func TestPrepareHLSPlaybackRejectsLivePlaylistWithoutEndlist(t *testing.T) {
	assertPrepareHLSRejectsInlinePlaylist(t, `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
`)
}

func TestPrepareHLSPlaybackRejectsEncryptedPlaylist(t *testing.T) {
	assertPrepareHLSRejectsInlinePlaylist(t, `#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="key.bin"
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`)
}

func TestPrepareHLSPlaybackRejectsByteRange(t *testing.T) {
	assertPrepareHLSRejectsInlinePlaylist(t, `#EXTM3U
#EXT-X-BYTERANGE:10@0
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`)
}

func TestPrepareHLSPlaybackRejectsDiscontinuity(t *testing.T) {
	assertPrepareHLSRejectsInlinePlaylist(t, `#EXTM3U
#EXT-X-DISCONTINUITY
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`)
}

func TestPrepareHLSPlaybackRejectsMediaTagWithURI(t *testing.T) {
	assertPrepareHLSRejectsInlinePlaylist(t, `#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",URI="audio.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=1280000
video.m3u8
#EXT-X-ENDLIST
`)
}

func TestPrepareHLSPlaybackAcceptsMasterPlaylistWithoutEndlistWhenChildIsFinite(t *testing.T) {
	const master = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1280000
video.m3u8
`
	const child = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			_, _ = w.Write([]byte(master))
		case "/video.m3u8":
			_, _ = w.Write([]byte(child))
		case "/seg.ts":
			_, _ = w.Write([]byte("segment"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, nil, srv.URL)

	playback, err := prepareHLSPlayback(context.Background(), srv.URL+"/master.m3u8", PolicyPrivateOnly)
	if err != nil {
		t.Fatalf("prepareHLSPlayback err = %v, want nil", err)
	}
	t.Cleanup(func() { _ = playback.Cleanup() })
	manifest := readCachedManifest(t, playback)
	if !strings.Contains(manifest, "playlist-000001.m3u8") {
		t.Fatalf("master manifest did not reference local child playlist:\n%s", manifest)
	}
}

func TestPrepareHLSPlaybackRejectsUnknownURIAttributeTags(t *testing.T) {
	cases := []struct {
		name     string
		playlist string
	}{
		{
			name: "iframe stream URI",
			playlist: `#EXTM3U
#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=1280000,URI="https://public.invalid/iframe.m3u8"
#EXT-X-ENDLIST
`,
		},
		{
			name: "content steering server URI",
			playlist: `#EXTM3U
#EXT-X-CONTENT-STEERING:SERVER-URI="https://public.invalid/steering.json"
#EXT-X-ENDLIST
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPrepareHLSRejectsInlinePlaylist(t, tc.playlist)
		})
	}
}

func TestPrepareHLSPlaybackRejectsUnknownURIAttributeTagsCaseInsensitive(t *testing.T) {
	assertPrepareHLSRejectsInlinePlaylist(t, `#EXTM3U
#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=1280000,uri="https://public.invalid/iframe.m3u8"
#EXT-X-ENDLIST
`)
}

func TestPrepareHLSPlaybackRejectsStreamInfWithURIAttribute(t *testing.T) {
	const master = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1,URI="https://public.invalid/x.m3u8"
child.m3u8
#EXT-X-ENDLIST
`
	const child = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			_, _ = w.Write([]byte(master))
		case "/child.m3u8":
			_, _ = w.Write([]byte(child))
		case "/seg.ts":
			_, _ = w.Write([]byte("segment"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, nil, srv.URL)

	_, err := prepareHLSPlayback(context.Background(), srv.URL+"/master.m3u8", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, errHLSInvalid) {
		t.Fatalf("err = %v, want errHLSInvalid", err)
	}
}

func TestPrepareHLSPlaybackRejectsCopiedCommentOrTagContainingRemoteURL(t *testing.T) {
	cases := []struct {
		name     string
		playlist string
	}{
		{
			name: "comment",
			playlist: `#EXTM3U
# vendor note https://public.invalid/x
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`,
		},
		{
			name: "tag value",
			playlist: `#EXTM3U
#EXT-X-SESSION-DATA:DATA-ID="vendor.example",VALUE="https://public.invalid/x"
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`,
		},
		{
			name: "case insensitive",
			playlist: `#EXTM3U
# vendor note HTTPS://public.invalid/x
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPrepareHLSRejectsInlinePlaylist(t, tc.playlist)
		})
	}
}

func TestPrepareHLSPlaybackRejectsStalledPlaylistBodyWithoutCallerDeadline(t *testing.T) {
	prevTimeout := hlsFetchTimeout
	hlsFetchTimeout = 50 * time.Millisecond
	t.Cleanup(func() {
		hlsFetchTimeout = prevTimeout
	})

	releaseServer := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/playlist.m3u8" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:4\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-releaseServer:
		}
	}))
	t.Cleanup(func() {
		close(releaseServer)
		srv.CloseClientConnections()
		srv.Close()
	})
	installHLSTestNetwork(t, nil, srv.URL)

	start := time.Now()
	_, err := prepareHLSPlayback(context.Background(), srv.URL+"/playlist.m3u8", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, errHLSInvalid) {
		t.Fatalf("err = %v, want errHLSInvalid", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("stalled body returned after %s, want under 1s", elapsed)
	}
}

func TestPrepareHLSPlaybackRejectsTooManyEntriesUsingHLSMaxEntries(t *testing.T) {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:4\n")
	for i := 0; i < hlsMaxEntries+1; i++ {
		fmt.Fprintf(&b, "#EXTINF:4,\nseg-%04d.ts\n", i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	assertPrepareHLSRejectsInlinePlaylist(t, b.String())
}

func TestPrepareHLSPlaybackRejectsPlaylistOverMaxPlaylistBytes(t *testing.T) {
	playlist := "#EXTM3U\n" + strings.Repeat("# comment padding\n", 140000) + "#EXT-X-ENDLIST\n"
	assertPrepareHLSRejectsInlinePlaylist(t, playlist)
}

func TestPrepareHLSPlaybackRejectsNestedPlaylistOverMaxDepth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var idx int
		_, _ = fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/p"), "%d.m3u8", &idx)
		if idx <= hlsMaxDepth {
			fmt.Fprintf(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\np%d.m3u8\n#EXT-X-ENDLIST\n", idx+1)
			return
		}
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-ENDLIST\n"))
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, nil, srv.URL)

	_, err := prepareHLSPlayback(context.Background(), srv.URL+"/p0.m3u8", PolicyPrivateOnly)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, errHLSInvalid) {
		t.Fatalf("err = %v, want errHLSInvalid", err)
	}
}

func TestPrepareHLSPlaybackRejectsResourceOverMaxResourceBytes(t *testing.T) {
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg.ts
#EXT-X-ENDLIST
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u8":
			_, _ = w.Write([]byte(playlist))
		case "/seg.ts":
			_, _ = w.Write([]byte("0123456789"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, nil, srv.URL)

	limits := defaultHLSLimits()
	limits.MaxResourceBytes = 5
	_, err := prepareHLSPlaybackWithLimits(context.Background(), srv.URL+"/playlist.m3u8", PolicyPrivateOnly, limits)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, errHLSInvalid) {
		t.Fatalf("err = %v, want errHLSInvalid", err)
	}
}

func TestPrepareHLSPlaybackRejectsTotalCacheOverMaxTotalBytes(t *testing.T) {
	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:4
#EXTINF:4,
seg1.ts
#EXTINF:4,
seg2.ts
#EXT-X-ENDLIST
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u8":
			_, _ = w.Write([]byte(playlist))
		case "/seg1.ts", "/seg2.ts":
			_, _ = w.Write([]byte("12345"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	installHLSTestNetwork(t, nil, srv.URL)

	limits := defaultHLSLimits()
	limits.MaxTotalBytes = int64(len(playlist)) + 7
	_, err := prepareHLSPlaybackWithLimits(context.Background(), srv.URL+"/playlist.m3u8", PolicyPrivateOnly, limits)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, errHLSInvalid) {
		t.Fatalf("err = %v, want errHLSInvalid", err)
	}
}

func assertPrepareHLSRejectsInlinePlaylist(t *testing.T, playlist string) {
	t.Helper()
	before := snapshotHLSTempDirs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/playlist.m3u8" {
			_, _ = w.Write([]byte(playlist))
			return
		}
		_, _ = w.Write([]byte("segment"))
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
}
