package dlna

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	hlsMaxPlaylistBytes = 2 << 20
	hlsMaxDepth         = 4
	hlsMaxEntries       = 2048
	hlsMaxResourceBytes = 64 << 20
	hlsMaxTotalBytes    = 512 << 20
)

type hlsLimits struct {
	MaxPlaylistBytes int64
	MaxDepth         int
	MaxEntries       int
	MaxResourceBytes int64
	MaxTotalBytes    int64
}

func defaultHLSLimits() hlsLimits {
	return hlsLimits{
		MaxPlaylistBytes: hlsMaxPlaylistBytes,
		MaxDepth:         hlsMaxDepth,
		MaxEntries:       hlsMaxEntries,
		MaxResourceBytes: hlsMaxResourceBytes,
		MaxTotalBytes:    hlsMaxTotalBytes,
	}
}

var errHLSInvalid = errors.New("dlna: invalid hls playlist")

var (
	hlsFetchTimeout = validatorRequestTimeout * time.Duration(validatorMaxRedirects+1)
	hlsCacheNameRE  = regexp.MustCompile(`^(playlist|segment|init)-[0-9]{6}\.(m3u8|ts|m4s|mp4|aac|mp3|vtt|bin)$`)
)

type hlsPlayback struct {
	PlaybackURI string
	tempDir     string
}

func (h hlsPlayback) Cleanup() error {
	if h.tempDir == "" {
		return nil
	}
	return os.RemoveAll(h.tempDir)
}

func isHLSMIME(mime string) bool {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "application/vnd.apple.mpegurl", "application/x-mpegurl":
		return true
	default:
		return false
	}
}

func prepareHLSPlayback(ctx context.Context, rawURL string, policy AddressPolicy) (hlsPlayback, error) {
	return prepareHLSPlaybackWithLimits(ctx, rawURL, policy, defaultHLSLimits())
}

func prepareHLSPlaybackWithLimits(ctx context.Context, rawURL string, policy AddressPolicy, limits hlsLimits) (hlsPlayback, error) {
	finalURL, err := validateHLSMediaURL(ctx, rawURL, policy)
	if err != nil {
		return hlsPlayback{}, err
	}

	tempDir, err := os.MkdirTemp("", "mister-groovyrelay-dlna-hls-*")
	if err != nil {
		return hlsPlayback{}, err
	}

	preparer := &hlsCachePreparer{
		root:      tempDir,
		policy:    policy,
		limits:    limits,
		transport: newHLSFetchTransport(policy),
	}
	preparer.client = newHLSFetchClient(preparer.transport)
	defer preparer.closeIdleConnections()

	name, err := preparer.cachePlaylist(ctx, finalURL, 0)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return hlsPlayback{}, err
	}
	playbackPath, err := safeHLSCachePath(tempDir, name)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return hlsPlayback{}, err
	}
	return hlsPlayback{
		PlaybackURI: playbackPath,
		tempDir:     tempDir,
	}, nil
}

type hlsCachePreparer struct {
	root      string
	policy    AddressPolicy
	limits    hlsLimits
	transport *http.Transport
	client    *http.Client

	playlistCounter int
	segmentCounter  int
	initCounter     int
	entries         int
	totalBytes      int64
}

func newHLSFetchTransport(policy AddressPolicy) *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           hlsDialContextForPolicy(policy),
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: hlsFetchTimeout,
	}
}

func newHLSFetchClient(transport *http.Transport) *http.Client {
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (p *hlsCachePreparer) closeIdleConnections() {
	if p.transport != nil {
		p.transport.CloseIdleConnections()
	}
}

func (p *hlsCachePreparer) cachePlaylist(ctx context.Context, rawURL string, depth int) (string, error) {
	if depth > p.limits.MaxDepth {
		return "", fmt.Errorf("%w: nested playlist depth exceeded", errHLSInvalid)
	}
	finalURL, err := validateHLSMediaURL(ctx, rawURL, p.policy)
	if err != nil {
		return "", err
	}
	body, finalURL, err := p.fetchBytes(ctx, finalURL, p.limits.MaxPlaylistBytes)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("playlist-%06d.m3u8", p.playlistCounter)
	p.playlistCounter++

	rewritten, err := p.rewritePlaylist(ctx, string(body), finalURL, depth)
	if err != nil {
		return "", err
	}
	if err := p.writeCacheFile(name, []byte(rewritten)); err != nil {
		return "", err
	}
	return name, nil
}

func (p *hlsCachePreparer) rewritePlaylist(ctx context.Context, body string, parentURL string, depth int) (string, error) {
	lines := strings.Split(body, "\n")
	var out strings.Builder
	seenHeader := false
	hasEndlist := false
	expectVariantURI := false
	sawVariant := false
	sawMediaResource := false

	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if !seenHeader {
			if trimmed == "" {
				continue
			}
			if trimmed != "#EXTM3U" {
				return "", fmt.Errorf("%w: missing EXTM3U header", errHLSInvalid)
			}
			seenHeader = true
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		if trimmed == "" {
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}

		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, "#EXT-X-KEY"):
			return "", fmt.Errorf("%w: encrypted playlists are not supported", errHLSInvalid)
		case strings.HasPrefix(upper, "#EXT-X-BYTERANGE"):
			return "", fmt.Errorf("%w: byte ranges are not supported", errHLSInvalid)
		case strings.HasPrefix(upper, "#EXT-X-DISCONTINUITY"):
			return "", fmt.Errorf("%w: discontinuities are not supported", errHLSInvalid)
		case strings.HasPrefix(upper, "#EXT-X-MEDIA") && strings.Contains(upper, "URI="):
			return "", fmt.Errorf("%w: alternate media playlists are not supported", errHLSInvalid)
		case strings.HasPrefix(upper, "#EXT-X-ENDLIST"):
			hasEndlist = true
			out.WriteString(line)
			out.WriteByte('\n')
		case strings.HasPrefix(upper, "#EXT-X-MAP:"):
			if err := p.countEntry(); err != nil {
				return "", err
			}
			childURL, err := resolveHLSReference(parentURL, extractHLSTagURI(line))
			if err != nil {
				return "", err
			}
			localName, err := p.cacheResource(ctx, childURL, "init", ".bin")
			if err != nil {
				return "", err
			}
			rewritten, err := rewriteHLSTagURI(line, localName)
			if err != nil {
				return "", err
			}
			out.WriteString(rewritten)
			out.WriteByte('\n')
		case strings.HasPrefix(upper, "#EXT-X-STREAM-INF"):
			if hasUnhandledHLSURIAttribute(upper) {
				return "", fmt.Errorf("%w: STREAM-INF URI-bearing attributes are not supported", errHLSInvalid)
			}
			sawVariant = true
			expectVariantURI = true
			out.WriteString(line)
			out.WriteByte('\n')
		case strings.HasPrefix(trimmed, "#"):
			if hasUnhandledHLSURIAttribute(upper) {
				return "", fmt.Errorf("%w: unhandled URI-bearing tag is not supported", errHLSInvalid)
			}
			out.WriteString(line)
			out.WriteByte('\n')
		case expectVariantURI:
			if err := p.countEntry(); err != nil {
				return "", err
			}
			childURL, err := resolveHLSReference(parentURL, trimmed)
			if err != nil {
				return "", err
			}
			localName, err := p.cachePlaylist(ctx, childURL, depth+1)
			if err != nil {
				return "", err
			}
			expectVariantURI = false
			out.WriteString(localName)
			out.WriteByte('\n')
		default:
			if err := p.countEntry(); err != nil {
				return "", err
			}
			sawMediaResource = true
			childURL, err := resolveHLSReference(parentURL, trimmed)
			if err != nil {
				return "", err
			}
			localName, err := p.cacheResource(ctx, childURL, "segment", ".bin")
			if err != nil {
				return "", err
			}
			out.WriteString(localName)
			out.WriteByte('\n')
		}
	}
	if !seenHeader {
		return "", fmt.Errorf("%w: missing EXTM3U header", errHLSInvalid)
	}
	if expectVariantURI {
		return "", fmt.Errorf("%w: missing variant playlist URI", errHLSInvalid)
	}
	if !hasEndlist && (!sawVariant || sawMediaResource) {
		return "", fmt.Errorf("%w: live playlists are not supported", errHLSInvalid)
	}
	rewritten := out.String()
	if containsHLSRemoteURL(rewritten) {
		return "", fmt.Errorf("%w: rewritten playlist contains remote URL", errHLSInvalid)
	}
	return rewritten, nil
}

func hasUnhandledHLSURIAttribute(upperLine string) bool {
	return strings.Contains(upperLine, "URI=") || strings.Contains(upperLine, "SERVER-URI=")
}

func containsHLSRemoteURL(manifest string) bool {
	lower := strings.ToLower(manifest)
	return strings.Contains(lower, "http://") || strings.Contains(lower, "https://")
}

func (p *hlsCachePreparer) countEntry() error {
	p.entries++
	if p.entries > p.limits.MaxEntries {
		return fmt.Errorf("%w: too many playlist entries", errHLSInvalid)
	}
	return nil
}

func (p *hlsCachePreparer) cacheResource(ctx context.Context, rawURL string, prefix string, fallbackExt string) (string, error) {
	finalURL, err := validateHLSMediaURL(ctx, rawURL, p.policy)
	if err != nil {
		return "", err
	}
	if isManifestLikeHLSResourceURL(finalURL) {
		return "", fmt.Errorf("%w: manifest URL is not a media resource", errHLSInvalid)
	}
	body, finalURL, err := p.fetchBytes(ctx, finalURL, p.limits.MaxResourceBytes)
	if err != nil {
		return "", err
	}
	if isManifestLikeHLSResourceURL(finalURL) {
		return "", fmt.Errorf("%w: manifest URL is not a media resource", errHLSInvalid)
	}

	ext := sanitizedHLSExt(finalURL, fallbackExt)
	var name string
	switch prefix {
	case "init":
		p.initCounter++
		name = fmt.Sprintf("init-%06d%s", p.initCounter, ext)
	case "segment":
		p.segmentCounter++
		name = fmt.Sprintf("segment-%06d%s", p.segmentCounter, ext)
	default:
		return "", fmt.Errorf("%w: unsupported cache resource kind", errHLSInvalid)
	}
	if err := p.writeCacheFile(name, body); err != nil {
		return "", err
	}
	return name, nil
}

func (p *hlsCachePreparer) fetchBytes(ctx context.Context, rawURL string, maxBytes int64) ([]byte, string, error) {
	// hlsFetchTimeout caps the cumulative time across the whole redirect chain
	// for one resource, so a caller without a deadline (e.g. context.Background())
	// can't hang. Per-hop header stalls are bounded by the transport's
	// ResponseHeaderTimeout.
	fetchCtx, cancel := context.WithTimeout(ctx, hlsFetchTimeout)
	defer cancel()
	return p.fetchBytesRedirects(fetchCtx, rawURL, maxBytes, 0)
}

func (p *hlsCachePreparer) fetchBytesRedirects(ctx context.Context, rawURL string, maxBytes int64, redirects int) ([]byte, string, error) {
	if redirects > validatorMaxRedirects {
		return nil, "", fmt.Errorf("%w: %w", errHLSInvalid, ErrTooManyRedirects)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", errHLSInvalid, err)
	}
	req.Header.Set("User-Agent", "MiSTer_GroovyRelay-DLNA-HLS-cache/1")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: fetch %s: %v", errHLSInvalid, rawURL, err)
	}

	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		location := resp.Header.Get("Location")
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		if location == "" {
			return nil, "", fmt.Errorf("%w: redirect without location", errHLSInvalid)
		}
		nextURL, err := resolveHLSReference(rawURL, location)
		if err != nil {
			return nil, "", err
		}
		finalURL, err := validateHLSMediaURL(ctx, nextURL, p.policy)
		if err != nil {
			return nil, "", err
		}
		return p.fetchBytesRedirects(ctx, finalURL, maxBytes, redirects+1)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Errorf("%w: fetch %s returned HTTP %d", errHLSInvalid, rawURL, resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", fmt.Errorf("%w: read %s: %v", errHLSInvalid, rawURL, err)
	}
	if int64(len(body)) > maxBytes {
		return nil, "", fmt.Errorf("%w: resource exceeds byte limit", errHLSInvalid)
	}
	return body, rawURL, nil
}

func (p *hlsCachePreparer) writeCacheFile(name string, body []byte) error {
	if p.totalBytes+int64(len(body)) > p.limits.MaxTotalBytes {
		return fmt.Errorf("%w: total cache size exceeded", errHLSInvalid)
	}
	path, err := safeHLSCachePath(p.root, name)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("%w: write cache file: %v", errHLSInvalid, err)
	}
	p.totalBytes += int64(len(body))
	return nil
}

func validateHLSMediaURL(ctx context.Context, rawURL string, policy AddressPolicy) (string, error) {
	v := &urlValidator{resolver: defaultDNSResolver}
	finalURL, _, err := v.parseAndClassify(ctx, rawURL, policy)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errHLSInvalid, err)
	}
	return finalURL, nil
}

func isManifestLikeHLSResourceURL(rawURL string) bool {
	return isHLSURLPath(rawURL) || isUnsupportedManifestURLPath(rawURL)
}

func resolveHLSReference(parentURL string, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("%w: empty URI", errHLSInvalid)
	}
	base, err := url.Parse(parentURL)
	if err != nil {
		return "", fmt.Errorf("%w: bad parent URL: %v", errHLSInvalid, err)
	}
	child, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", fmt.Errorf("%w: bad child URL: %v", errHLSInvalid, err)
	}
	return base.ResolveReference(child).String(), nil
}

func extractHLSTagURI(line string) string {
	idx := strings.Index(strings.ToUpper(line), "URI=")
	if idx < 0 {
		return ""
	}
	value := line[idx+len("URI="):]
	if value == "" {
		return ""
	}
	if value[0] == '"' {
		end := strings.IndexByte(value[1:], '"')
		if end < 0 {
			return ""
		}
		return value[1 : 1+end]
	}
	if comma := strings.IndexByte(value, ','); comma >= 0 {
		return strings.TrimSpace(value[:comma])
	}
	return strings.TrimSpace(value)
}

func rewriteHLSTagURI(line string, localName string) (string, error) {
	idx := strings.Index(strings.ToUpper(line), "URI=")
	if idx < 0 {
		return "", fmt.Errorf("%w: tag URI missing", errHLSInvalid)
	}
	start := idx + len("URI=")
	if start >= len(line) {
		return "", fmt.Errorf("%w: tag URI empty", errHLSInvalid)
	}
	if line[start] == '"' {
		endRel := strings.IndexByte(line[start+1:], '"')
		if endRel < 0 {
			return "", fmt.Errorf("%w: tag URI quote missing", errHLSInvalid)
		}
		end := start + 1 + endRel
		return line[:start+1] + localName + line[end:], nil
	}
	end := len(line)
	if comma := strings.IndexByte(line[start:], ','); comma >= 0 {
		end = start + comma
	}
	return line[:start] + localName + line[end:], nil
}

func sanitizedHLSExt(finalURL string, fallback string) string {
	parsedURL, err := url.Parse(finalURL)
	if err != nil {
		return fallback
	}
	decodedPath, err := url.PathUnescape(parsedURL.Path)
	if err != nil {
		decodedPath = parsedURL.Path
	}
	switch strings.ToLower(path.Ext(decodedPath)) {
	case ".ts":
		return ".ts"
	case ".m4s", ".mp4":
		return strings.ToLower(path.Ext(decodedPath))
	case ".aac", ".mp3":
		return strings.ToLower(path.Ext(decodedPath))
	case ".vtt":
		return ".vtt"
	default:
		return fallback
	}
}

func safeHLSCachePath(root string, name string) (string, error) {
	base := filepath.Base(name)
	if base != name || !hlsCacheNameRE.MatchString(base) {
		return "", fmt.Errorf("%w: unsafe cache filename", errHLSInvalid)
	}
	cleanRoot := filepath.Clean(root)
	joined := filepath.Clean(filepath.Join(cleanRoot, base))
	if joined == cleanRoot || !strings.HasPrefix(joined, cleanRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: cache path escapes temp root", errHLSInvalid)
	}
	return joined, nil
}

var hlsNetDialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

func hlsDialContextForPolicy(policy AddressPolicy) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
		}
		resolveCtx, cancel := context.WithTimeout(ctx, validatorRequestTimeout)
		defer cancel()
		ips, err := defaultDNSResolver(resolveCtx, host)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve %q: %v", ErrAddressNotAllowed, host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("%w: %q resolved to no addresses", ErrAddressNotAllowed, host)
		}

		for _, ip := range ips {
			class := classifyIP(ip)
			switch class {
			case ipClassDisallowed:
				return nil, fmt.Errorf("%w: %s -> %s (disallowed class)", ErrAddressNotAllowed, host, ip)
			case ipClassPrivate:
			case ipClassPublic:
				if policy != PolicyAllowPublic {
					return nil, fmt.Errorf("%w: %s -> %s (public, policy=PrivateOnly)", ErrAddressNotAllowed, host, ip)
				}
			}
		}
		approvedAddr := net.JoinHostPort(ips[0].String(), port)
		return hlsNetDialContext(ctx, network, approvedAddr)
	}
}
