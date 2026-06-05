package hlsbuffer

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func OpenSession(ctx context.Context, opts SessionOptions) (*Session, error) {
	cfg := normalizeSessionConfig(opts.Config)
	if opts.TrustMode == 0 {
		opts.TrustMode = TrustModeGenericPublic
	}
	if opts.OutputHeight == 0 {
		opts.OutputHeight = cfg.MaxVariantHeight
	}
	validator := opts.Validator
	if validator.Client == nil {
		validator.Client = opts.Client
	}

	sessionDir, err := os.MkdirTemp(opts.CacheRoot, "hls-buffer-*")
	if err != nil {
		return nil, fmt.Errorf("hls session: create cache dir: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(sessionDir) }
	if err := os.WriteFile(filepath.Join(sessionDir, ActiveLockName), []byte(time.Now().Format(time.RFC3339Nano)), 0o600); err != nil {
		_ = cleanup()
		return nil, fmt.Errorf("hls session: write active lock: %w", err)
	}

	stats := &sessionStats{}
	playlist, mediaURL, selected, err := loadSessionPlaylist(ctx, opts.SourceURL, opts.TrustMode, opts.OutputHeight, cfg, validator)
	if err != nil {
		stats.setFailure(err.Error())
		_ = cleanup()
		return nil, err
	}
	stats.addPlaylistReload()
	if selected.URI != "" {
		stats.addPlaylistReload()
		stats.setSelectedVariant(selected)
	}

	cache := NewSegmentCache(sessionDir, cfg.MaxCachedSegments, cfg.MaxCacheBytes)
	warmSegments := chooseWarmSegments(playlist.Segments, cfg)
	cached, err := warmSegmentCache(ctx, mediaURL, warmSegments, cfg, opts.TrustMode, validator, cache)
	if err != nil {
		stats.setFailure(err.Error())
		_ = cleanup()
		return nil, err
	}
	for _, item := range cached {
		stats.addSegment(item.segment.Duration, item.size)
	}
	stats.setCurrent(cached, cache.TotalBytes())

	playbackPath := filepath.Join(sessionDir, "playlist.m3u8")
	if err := writeLocalPlaylist(playbackPath, playlist, cached); err != nil {
		stats.setFailure(err.Error())
		_ = cleanup()
		return nil, err
	}

	loopCtx, loopCancel := context.WithCancel(context.Background())
	loopDone := make(chan struct{})
	refresh := &refreshState{
		mediaURL:     mediaURL,
		cfg:          cfg,
		trustMode:    opts.TrustMode,
		validator:    validator,
		cache:        cache,
		stats:        stats,
		playbackPath: playbackPath,
		cachedBySeq:  cachedBySequence(cached),
	}
	go func() {
		defer close(loopDone)
		runRefreshLoop(loopCtx, refresh, playlist)
	}()

	var closeOnce sync.Once
	var closeErr error
	return &Session{
		PlaybackPath: playbackPath,
		Policy: core.MediaInputPolicy{
			// ffmpeg's -reconnect* flags are HTTP-demuxer-private options;
			// combined with ProtocolWhitelist=["file"] they have nothing to
			// bind to and Alpine ffmpeg 6.x exits with "Option reconnect not
			// found." before any frames flow. The local playlist + cached
			// .ts segments are file:// reads only, so reconnect behavior is
			// already moot. Do NOT add DisableReconnect here.
			ProtocolWhitelist: []string{"file"},
			DisableRedirects:  true,
			DisablePlaylists:  true,
		},
		Stats: stats.snapshot,
		Close: func() error {
			closeOnce.Do(func() {
				loopCancel()
				<-loopDone
				closeErr = cleanup()
			})
			return closeErr
		},
	}, nil
}

func normalizeSessionConfig(c Config) Config {
	if c.LiveEdgeSegments == 0 {
		c.LiveEdgeSegments = 3
	}
	if c.StartSegments == 0 {
		c.StartSegments = 2
	}
	if c.MaxCachedSegments == 0 {
		c.MaxCachedSegments = 6
	}
	if c.MaxCacheBytes == 0 {
		c.MaxCacheBytes = 268435456
	}
	if c.MaxPlaylistBytes == 0 {
		c.MaxPlaylistBytes = 1048576
	}
	if c.MaxSegmentBytes == 0 {
		c.MaxSegmentBytes = 52428800
	}
	if c.SegmentTimeout == 0 {
		c.SegmentTimeout = 10 * time.Second
	}
	if c.PlaylistTimeout == 0 {
		c.PlaylistTimeout = 10 * time.Second
	}
	if c.MaxVariantHeight == 0 {
		c.MaxVariantHeight = 720
	}
	if c.StaleCacheReapInterval == 0 {
		c.StaleCacheReapInterval = 24 * time.Hour
	}
	return c
}

func loadSessionPlaylist(ctx context.Context, sourceURL string, trustMode TrustMode, outputHeight int, cfg Config, validator URLValidator) (Playlist, string, Variant, error) {
	body, finalURL, err := fetchBytes(ctx, sourceURL, cfg.MaxPlaylistBytes, cfg.PlaylistTimeout, trustMode, validator)
	if err != nil {
		return Playlist{}, "", Variant{}, err
	}
	playlist, err := ParsePlaylist(body)
	if err != nil {
		return Playlist{}, "", Variant{}, err
	}
	if playlist.Kind == PlaylistMedia {
		return playlist, finalURL, Variant{}, nil
	}

	selected, err := SelectVariant(playlist.Variants, outputHeight, cfg.MaxVariantHeight)
	if err != nil {
		return Playlist{}, "", Variant{}, err
	}
	childURL, err := resolveURLReference(finalURL, selected.URI)
	if err != nil {
		return Playlist{}, "", Variant{}, err
	}
	body, mediaURL, err := fetchBytes(ctx, childURL, cfg.MaxPlaylistBytes, cfg.PlaylistTimeout, trustMode, validator)
	if err != nil {
		return Playlist{}, "", Variant{}, err
	}
	mediaPlaylist, err := ParsePlaylist(body)
	if err != nil {
		return Playlist{}, "", Variant{}, err
	}
	if mediaPlaylist.Kind != PlaylistMedia {
		return Playlist{}, "", Variant{}, fmt.Errorf("hls session: selected variant did not resolve to media playlist")
	}
	return mediaPlaylist, mediaURL, selected, nil
}

type cachedSegment struct {
	segment Segment
	name    string
	size    int64
}

func chooseWarmSegments(segments []Segment, cfg Config) []Segment {
	if len(segments) == 0 {
		return nil
	}
	start := 0
	if len(segments) > cfg.LiveEdgeSegments {
		start = len(segments) - cfg.LiveEdgeSegments
	}
	end := start + cfg.StartSegments
	if end > len(segments) {
		end = len(segments)
	}
	return append([]Segment(nil), segments[start:end]...)
}

func warmSegmentCache(ctx context.Context, mediaURL string, segments []Segment, cfg Config, trustMode TrustMode, validator URLValidator, cache *SegmentCache) ([]cachedSegment, error) {
	// Derive a cancellable context so that returning on the first error
	// also tears down sibling segment fetches still in flight. Without
	// this, a single fail forces the remaining goroutines to wait out
	// SegmentTimeout while keeping connections open.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		idx  int
		body []byte
		err  error
	}
	results := make(chan result, len(segments))
	for i, segment := range segments {
		i, segment := i, segment
		go func() {
			segmentURL, err := resolveURLReference(mediaURL, segment.URI)
			if err != nil {
				results <- result{idx: i, err: err}
				return
			}
			body, _, err := fetchBytes(ctx, segmentURL, cfg.MaxSegmentBytes, cfg.SegmentTimeout, trustMode, validator)
			results <- result{idx: i, body: body, err: err}
		}()
	}

	bodies := make([][]byte, len(segments))
	for range segments {
		res := <-results
		if res.err != nil {
			return nil, res.err
		}
		bodies[res.idx] = res.body
	}

	cached := make([]cachedSegment, 0, len(segments))
	for i, segment := range segments {
		name := segmentCacheName(segment.Sequence)
		if err := cache.Put(name, bodies[i]); err != nil {
			return nil, err
		}
		cached = append(cached, cachedSegment{
			segment: segment,
			name:    name,
			size:    int64(len(bodies[i])),
		})
	}
	return cached, nil
}

type refreshState struct {
	mediaURL     string
	cfg          Config
	trustMode    TrustMode
	validator    URLValidator
	cache        *SegmentCache
	stats        *sessionStats
	playbackPath string
	cachedBySeq  map[int64]cachedSegment
}

func runRefreshLoop(ctx context.Context, state *refreshState, playlist Playlist) {
	interval := playlistReloadInterval(playlist)
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		next, err := refreshPlaylist(ctx, state)
		if err != nil {
			state.stats.setFailure(err.Error())
		} else {
			playlist = next
			interval = playlistReloadInterval(playlist)
		}
		timer.Reset(interval)
	}
}

func playlistReloadInterval(playlist Playlist) time.Duration {
	if playlist.Target <= 0 {
		return time.Second
	}
	interval := playlist.Target / 2
	if interval < 100*time.Millisecond {
		return 100 * time.Millisecond
	}
	return interval
}

func refreshPlaylist(ctx context.Context, state *refreshState) (Playlist, error) {
	body, finalURL, err := fetchBytes(ctx, state.mediaURL, state.cfg.MaxPlaylistBytes, state.cfg.PlaylistTimeout, state.trustMode, state.validator)
	if err != nil {
		return Playlist{}, err
	}
	playlist, err := ParsePlaylist(body)
	if err != nil {
		return Playlist{}, err
	}
	if playlist.Kind != PlaylistMedia {
		return Playlist{}, fmt.Errorf("hls session: refreshed playlist is not a media playlist")
	}
	state.mediaURL = finalURL
	state.stats.addPlaylistReload()

	segments := segmentsMissingFromCache(playlist.Segments, state.cachedBySeq)
	cached, err := warmSegmentCache(ctx, state.mediaURL, segments, state.cfg, state.trustMode, state.validator, state.cache)
	if err != nil {
		return Playlist{}, err
	}
	for _, item := range cached {
		state.cachedBySeq[item.segment.Sequence] = item
		state.stats.addSegment(item.segment.Duration, item.size)
	}
	pruneCachedByEntries(state.cachedBySeq, state.cache.Entries())
	window := cachedWindow(state.cachedBySeq)
	state.stats.setCurrent(window, state.cache.TotalBytes())
	if err := writeLocalPlaylist(state.playbackPath, playlist, window); err != nil {
		return Playlist{}, err
	}
	return playlist, nil
}

func segmentsMissingFromCache(segments []Segment, cached map[int64]cachedSegment) []Segment {
	if len(segments) == 0 {
		return nil
	}
	minCached := int64(0)
	haveCached := false
	for seq := range cached {
		if !haveCached || seq < minCached {
			minCached = seq
			haveCached = true
		}
	}
	out := make([]Segment, 0, len(segments))
	for _, segment := range segments {
		if haveCached && segment.Sequence < minCached {
			continue
		}
		if _, ok := cached[segment.Sequence]; ok {
			continue
		}
		out = append(out, segment)
	}
	return out
}

func cachedBySequence(items []cachedSegment) map[int64]cachedSegment {
	out := make(map[int64]cachedSegment, len(items))
	for _, item := range items {
		out[item.segment.Sequence] = item
	}
	return out
}

func pruneCachedByEntries(cached map[int64]cachedSegment, entries []CacheEntry) {
	names := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		names[entry.Name] = struct{}{}
	}
	for seq, item := range cached {
		if _, ok := names[item.name]; !ok {
			delete(cached, seq)
		}
	}
}

func cachedWindow(cached map[int64]cachedSegment) []cachedSegment {
	out := make([]cachedSegment, 0, len(cached))
	for _, item := range cached {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].segment.Sequence < out[j].segment.Sequence
	})
	return out
}

func segmentCacheName(sequence int64) string {
	return fmt.Sprintf("segment-%06d.ts", sequence)
}

func fetchBytes(ctx context.Context, rawURL string, maxBytes int64, timeout time.Duration, trustMode TrustMode, validator URLValidator) ([]byte, string, error) {
	finalURL, err := validator.ValidateReference(ctx, rawURL, trustMode)
	if err != nil {
		return nil, "", err
	}
	client := validator.noRedirectClient()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	for hop := 0; hop <= defaultMaxRedirects; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, finalURL, nil)
		if err != nil {
			return nil, "", fmt.Errorf("hls fetch: build request: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("hls fetch: %w", err)
		}
		if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
			location := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if location == "" {
				return nil, "", fmt.Errorf("hls fetch: redirect missing Location")
			}
			next, err := resolveURLReference(finalURL, location)
			if err != nil {
				return nil, "", err
			}
			finalURL, err = validator.ValidateReference(ctx, next, trustMode)
			if err != nil {
				return nil, "", err
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, "", fmt.Errorf("hls fetch: unexpected HTTP status %d", resp.StatusCode)
		}
		body, err := readLimited(resp.Body, maxBytes)
		if err != nil {
			return nil, "", err
		}
		return body, finalURL, nil
	}
	return nil, "", fmt.Errorf("hls fetch: redirect chain exceeded %d hops", defaultMaxRedirects)
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("hls fetch: read body: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("hls fetch: body exceeds %d bytes", maxBytes)
	}
	return body, nil
}

func writeLocalPlaylist(path string, source Playlist, cached []cachedSegment) error {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	target := source.Target
	if target == 0 {
		for _, item := range cached {
			if item.segment.Duration > target {
				target = item.segment.Duration
			}
		}
	}
	if target > 0 {
		fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", int(math.Ceil(target.Seconds())))
	}
	if len(cached) > 0 {
		fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", cached[0].segment.Sequence)
	}
	for _, item := range cached {
		if item.segment.Discontinuity {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		fmt.Fprintf(&b, "#EXTINF:%s,\n%s\n", formatHLSDuration(item.segment.Duration), item.name)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("hls session: write local playlist: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("hls session: publish local playlist: %w", err)
	}
	return nil
}

func formatHLSDuration(d time.Duration) string {
	seconds := d.Seconds()
	if seconds == math.Trunc(seconds) {
		return fmt.Sprintf("%.0f", seconds)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", seconds), "0"), ".")
}

type sessionStats struct {
	mu                    sync.Mutex
	currentSegments       int
	currentMediaDuration  time.Duration
	currentCacheBytes     int64
	playlistReloadsTotal  int64
	segmentDownloadsTotal int64
	selectedVariant       Variant
	failureReason         string
}

func (s *sessionStats) addPlaylistReload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.playlistReloadsTotal++
}

func (s *sessionStats) addSegment(time.Duration, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.segmentDownloadsTotal++
}

func (s *sessionStats) setCurrent(cached []cachedSegment, bytes int64) {
	var duration time.Duration
	for _, item := range cached {
		duration += item.segment.Duration
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentSegments = len(cached)
	s.currentMediaDuration = duration
	s.currentCacheBytes = bytes
}

func (s *sessionStats) setSelectedVariant(v Variant) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedVariant = v
}

func (s *sessionStats) setFailure(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failureReason = reason
}

func (s *sessionStats) snapshot() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		CachedSegments:        s.currentSegments,
		CachedMediaDuration:   s.currentMediaDuration,
		CacheBytes:            s.currentCacheBytes,
		PlaylistReloadsTotal:  s.playlistReloadsTotal,
		SegmentDownloadsTotal: s.segmentDownloadsTotal,
		SelectedVariant:       s.selectedVariant,
		FailureReason:         s.failureReason,
	}
}
