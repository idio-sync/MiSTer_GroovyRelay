package hlsbuffer

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenSessionWarmsStartSegmentsAndPublishesLocalPlaylist(t *testing.T) {
	var fetched atomic.Int32
	srv := newHLSSessionTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			_, _ = fmt.Fprint(w, livePlaylist(10, 4, 4))
		case "/seg-011.ts", "/seg-012.ts":
			fetched.Add(1)
			_, _ = fmt.Fprintf(w, "body-%s", r.URL.Path)
		default:
			http.NotFound(w, r)
		}
	})

	sess, err := OpenSession(context.Background(), sessionTestOptions(t, srv.URL+"/live.m3u8"))
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	if fetched.Load() != 2 {
		t.Fatalf("fetched segments = %d, want 2", fetched.Load())
	}

	manifest := readFile(t, sess.PlaybackPath)
	if strings.Contains(manifest, "http://") || strings.Contains(manifest, "https://") {
		t.Fatalf("local playlist leaked remote URL:\n%s", manifest)
	}
	for _, want := range []string{"#EXT-X-MEDIA-SEQUENCE:11", "segment-000011.ts", "segment-000012.ts"} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("local playlist missing %q:\n%s", want, manifest)
		}
	}
	for _, name := range []string{"segment-000011.ts", "segment-000012.ts"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(sess.PlaybackPath), name)); err != nil {
			t.Fatalf("cached segment %s missing: %v", name, err)
		}
	}
}

func TestOpenSessionReloadsPlaylistAndPublishesNewSegments(t *testing.T) {
	var mediaSequence atomic.Int64
	mediaSequence.Store(100)
	srv := newHLSSessionTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			_, _ = fmt.Fprint(w, livePlaylist(int(mediaSequence.Load()), 4, 1))
		case "/seg-101.ts", "/seg-102.ts", "/seg-103.ts", "/seg-104.ts", "/seg-105.ts":
			_, _ = fmt.Fprintf(w, "body-%s", r.URL.Path)
		default:
			http.NotFound(w, r)
		}
	})

	sess, err := OpenSession(context.Background(), sessionTestOptions(t, srv.URL+"/live.m3u8"))
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	mediaSequence.Store(102)
	waitForLocalPlaylist(t, sess.PlaybackPath, "segment-000103.ts")
}

func TestOpenSessionUsesLocalOnlyMediaPolicy(t *testing.T) {
	srv := newHLSSessionTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			_, _ = fmt.Fprint(w, livePlaylist(20, 2, 4))
		case "/seg-020.ts", "/seg-021.ts":
			_, _ = fmt.Fprint(w, "segment")
		default:
			http.NotFound(w, r)
		}
	})

	sess, err := OpenSession(context.Background(), sessionTestOptions(t, srv.URL+"/live.m3u8"))
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	if got := strings.Join(sess.Policy.ProtocolWhitelist, ","); got != "file" {
		t.Fatalf("ProtocolWhitelist = %q, want file", got)
	}
	if !sess.Policy.DisableReconnect {
		t.Fatal("DisableReconnect = false, want true")
	}
}

func TestOpenSessionCleansUpOnClose(t *testing.T) {
	srv := newHLSSessionTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			_, _ = fmt.Fprint(w, livePlaylist(30, 2, 4))
		case "/seg-030.ts", "/seg-031.ts":
			_, _ = fmt.Fprint(w, "segment")
		default:
			http.NotFound(w, r)
		}
	})

	sess, err := OpenSession(context.Background(), sessionTestOptions(t, srv.URL+"/live.m3u8"))
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	sessionDir := filepath.Dir(sess.PlaybackPath)
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("session dir should be removed, stat err = %v", err)
	}
}

func TestSlowSegmentServerIsSmoothedByPrefetch(t *testing.T) {
	requested := make(chan string, 2)
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	srv := newHLSSessionTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			_, _ = fmt.Fprint(w, livePlaylist(40, 2, 4))
		case "/seg-040.ts", "/seg-041.ts":
			requested <- r.URL.Path
			<-release
			_, _ = fmt.Fprint(w, "segment")
		default:
			http.NotFound(w, r)
		}
	})

	done := make(chan error, 1)
	go func() {
		sess, err := OpenSession(context.Background(), sessionTestOptions(t, srv.URL+"/live.m3u8"))
		if err == nil {
			_ = sess.Close()
		}
		done <- err
	}()
	waitForSegmentRequests(t, requested, "/seg-040.ts", "/seg-041.ts")
	close(release)
	released = true

	if err := <-done; err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
}

func TestUnsupportedPlaylistFailsClearly(t *testing.T) {
	srv := newHLSSessionTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="key.bin"
#EXTINF:4,
seg.ts
`)
	})

	_, err := OpenSession(context.Background(), sessionTestOptions(t, srv.URL+"/live.m3u8"))
	if err == nil {
		t.Fatal("OpenSession error = nil, want unsupported playlist error")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("OpenSession error = %q, want mention unsupported", err)
	}
}

func TestSessionStatsReportUnits(t *testing.T) {
	srv := newHLSSessionTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			_, _ = fmt.Fprint(w, `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=854x480,CODECS="avc1.64001f,mp4a.40.2"
live.m3u8
`)
		case "/live.m3u8":
			_, _ = fmt.Fprint(w, livePlaylist(50, 2, 4))
		case "/seg-050.ts", "/seg-051.ts":
			_, _ = fmt.Fprint(w, "12345")
		default:
			http.NotFound(w, r)
		}
	})

	sess, err := OpenSession(context.Background(), sessionTestOptions(t, srv.URL+"/master.m3u8"))
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	stats := sess.Stats()
	if stats.CachedSegments != 2 {
		t.Fatalf("CachedSegments = %d, want 2", stats.CachedSegments)
	}
	if stats.CachedMediaDuration != 8*time.Second {
		t.Fatalf("CachedMediaDuration = %v, want 8s", stats.CachedMediaDuration)
	}
	if stats.CacheBytes != 10 {
		t.Fatalf("CacheBytes = %d, want 10", stats.CacheBytes)
	}
	if stats.PlaylistReloadsTotal != 2 {
		t.Fatalf("PlaylistReloadsTotal = %d, want 2", stats.PlaylistReloadsTotal)
	}
	if stats.SegmentDownloadsTotal != 2 {
		t.Fatalf("SegmentDownloadsTotal = %d, want 2", stats.SegmentDownloadsTotal)
	}
	if stats.SelectedVariant.URI != "live.m3u8" || stats.SelectedVariant.Height != 480 {
		t.Fatalf("SelectedVariant = %+v, want live.m3u8 480p", stats.SelectedVariant)
	}
}

func sessionTestOptions(t *testing.T, rawURL string) SessionOptions {
	t.Helper()
	return SessionOptions{
		SourceURL:    publicHostURL(rawURL),
		CacheRoot:    t.TempDir(),
		TrustMode:    TrustModeGenericPublic,
		OutputHeight: 480,
		Config: Config{
			LiveEdgeSegments:       3,
			StartSegments:          2,
			MaxCachedSegments:      6,
			MaxCacheBytes:          1 << 20,
			MaxPlaylistBytes:       1 << 20,
			MaxSegmentBytes:        1 << 20,
			SegmentTimeout:         time.Second,
			PlaylistTimeout:        time.Second,
			MaxVariantHeight:       720,
			StaleCacheReapInterval: 24 * time.Hour,
		},
		Validator: URLValidator{
			Resolver: staticResolver(map[string][]net.IP{
				"public.example": {net.ParseIP("93.184.216.34")},
			}),
		},
		Client: rewriteHostClient(t, &httptest.Server{URL: rawURL[:strings.LastIndex(rawURL, "/")]}),
	}
}

func newHLSSessionTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func publicHostURL(rawURL string) string {
	return strings.Replace(rawURL, "127.0.0.1", "public.example", 1)
}

func livePlaylist(start, count int, duration int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n", duration, start)
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, "#EXTINF:%d,\nseg-%03d.ts\n", duration, start+i)
	}
	return b.String()
}

func waitForSegmentRequests(t *testing.T, ch <-chan string, wants ...string) {
	t.Helper()
	seen := map[string]bool{}
	deadline := time.After(time.Second)
	for len(seen) < len(wants) {
		select {
		case got := <-ch:
			seen[got] = true
		case <-deadline:
			t.Fatalf("timed out waiting for segment requests; seen=%v want=%v", seen, wants)
		}
	}
	for _, want := range wants {
		if !seen[want] {
			t.Fatalf("segment request %q not seen; seen=%v", want, seen)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func waitForLocalPlaylist(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body := readFile(t, path)
		if strings.Contains(body, want) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for local playlist %s to contain %q; last body:\n%s", path, want, readFile(t, path))
}
