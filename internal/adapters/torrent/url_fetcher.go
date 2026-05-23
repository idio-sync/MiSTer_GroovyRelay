package torrent

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/sourcefetch"
)

const (
	torrentURLMaxRedirects = 3
	torrentURLFetchTimeout = 15 * time.Second
	torrentURLUserAgent    = "MiSTer_GroovyRelay-torrent-url-fetcher/1"
)

type TorrentURLFetchResult struct {
	Body        []byte
	FinalURL    string
	ContentType string
}

type torrentURLFetcher interface {
	FetchTorrentURL(ctx context.Context, rawURL string, limit int64) (TorrentURLFetchResult, error)
}

type sourceFetcher interface {
	Fetch(ctx context.Context, method, rawURL string, limits sourcefetch.Limits, condition sourcefetch.Condition) (sourcefetch.Response, error)
}

type torrentURLHTTPFetcher struct {
	source sourceFetcher
}

func (f torrentURLHTTPFetcher) FetchTorrentURL(ctx context.Context, rawURL string, limit int64) (TorrentURLFetchResult, error) {
	rawURL = strings.TrimSpace(rawURL)
	if err := validateTorrentURLInput(rawURL); err != nil {
		return TorrentURLFetchResult{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, torrentURLFetchTimeout)
	defer cancel()

	source := f.source
	if source == nil {
		source = sourcefetch.Fetcher{}
	}
	limits := sourcefetch.Limits{
		MaxBytes:       limit,
		MaxRedirects:   torrentURLMaxRedirects,
		AllowedSchemes: []string{"http", "https"},
		UserAgent:      torrentURLUserAgent,
	}

	if !torrentURLPathCandidate(rawURL) {
		resp, err := source.Fetch(ctx, http.MethodHead, rawURL, limits, sourcefetch.Condition{})
		if err != nil {
			return TorrentURLFetchResult{}, &TorrentError{
				Kind:    ErrBadInput,
				Message: "torrent URL must end in .torrent or support HEAD with a BitTorrent content type",
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 || !torrentURLAcceptable(rawURL, resp.FinalURL, resp.ContentType) {
			return TorrentURLFetchResult{}, &TorrentError{Kind: ErrBadInput, Message: "URL does not look like a torrent file"}
		}
	}

	resp, err := source.Fetch(ctx, http.MethodGet, rawURL, limits, sourcefetch.Condition{})
	if err != nil {
		if errors.Is(err, sourcefetch.ErrBodyTooLarge) {
			return TorrentURLFetchResult{}, &TorrentError{Kind: ErrUploadTooLarge, Message: "torrent file exceeds 4 MiB", Err: err}
		}
		return TorrentURLFetchResult{}, &TorrentError{Kind: ErrBadInput, Message: "torrent URL fetch failed"}
	}
	if !torrentURLAcceptable(rawURL, resp.FinalURL, resp.ContentType) {
		return TorrentURLFetchResult{}, &TorrentError{Kind: ErrBadInput, Message: "URL does not look like a torrent file"}
	}

	return TorrentURLFetchResult{
		Body:        resp.Body,
		FinalURL:    resp.FinalURL,
		ContentType: resp.ContentType,
	}, nil
}

func validateTorrentURLInput(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() {
		return &TorrentError{Kind: ErrBadInput, Message: "invalid torrent URL"}
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return &TorrentError{Kind: ErrBadInput, Message: "torrent URL scheme must be http or https"}
	}
	if u.User != nil {
		return &TorrentError{Kind: ErrBadInput, Message: "torrent URL credentials are not allowed"}
	}
	hostname := u.Hostname()
	if hostname == "" {
		return &TorrentError{Kind: ErrBadInput, Message: "torrent URL host is required"}
	}
	if _, err := netip.ParseAddr(stripIPv6Zone(hostname)); err == nil {
		return &TorrentError{Kind: ErrBadInput, Message: "torrent URL IP literal hosts are not allowed"}
	}
	return nil
}

func torrentURLAcceptable(originalURL, finalURL, contentType string) bool {
	if torrentURLPathCandidate(originalURL) || torrentURLPathCandidate(finalURL) {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return false
	}
	switch strings.ToLower(mediaType) {
	case "application/x-bittorrent", "application/x-torrent":
		return true
	default:
		return false
	}
}

func torrentURLPathCandidate(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".torrent")
}

func stripIPv6Zone(hostname string) string {
	if zone := strings.LastIndex(hostname, "%"); zone >= 0 {
		return hostname[:zone]
	}
	return hostname
}

var _ torrentURLFetcher = torrentURLHTTPFetcher{}
