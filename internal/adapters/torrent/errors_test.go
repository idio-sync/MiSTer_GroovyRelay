package torrent

import (
	"errors"
	"net/http"
	"testing"
)

func TestTorrentErrorHTTPStatus(t *testing.T) {
	cases := []struct {
		kind TorrentErrorKind
		want int
	}{
		{ErrDisabled, http.StatusConflict},
		{ErrTrafficNotAcknowledged, http.StatusForbidden},
		{ErrBadInput, http.StatusBadRequest},
		{ErrUploadTooLarge, http.StatusRequestEntityTooLarge},
		{ErrMetadataTimeout, http.StatusGatewayTimeout},
		{ErrNoPlayableFile, http.StatusUnprocessableEntity},
		{ErrExpiredToken, http.StatusNotFound},
		{ErrNonLoopback, http.StatusForbidden},
		{ErrCoreStart, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		err := &TorrentError{Kind: tc.kind, Message: "x"}
		if got := torrentErrorStatus(err); got != tc.want {
			t.Fatalf("%s status = %d, want %d", tc.kind, got, tc.want)
		}
	}
	if got := torrentErrorStatus(errors.New("plain")); got != http.StatusInternalServerError {
		t.Fatalf("plain error status = %d, want 500", got)
	}
}

func TestRedactMagnetKeepsOnlyShortInfoHash(t *testing.T) {
	raw := "magnet:?xt=urn:btih:0123456789ABCDEF0123456789abcdef01234567&dn=Movie&tr=http://tracker.example/announce&xs=http://x&ws=http://w&as=http://a"
	got := redactMagnet(raw)
	want := "magnet:?xt=urn:btih:01234567"
	if got != want {
		t.Fatalf("redactMagnet = %q, want %q", got, want)
	}
}

func TestRedactMagnetAcceptsBase32InfoHash(t *testing.T) {
	raw := "magnet:?xt=urn:btih:AAAQEAYEAUDAOCAJBIFQYDIOB4IBCEQT&dn=Movie&tr=http://tracker.example/announce"
	got := redactMagnet(raw)
	want := "magnet:?xt=urn:btih:00010203"
	if got != want {
		t.Fatalf("redactMagnet base32 = %q, want %q", got, want)
	}
}

func TestRedactMagnetInvalid(t *testing.T) {
	for _, raw := range []string{
		"magnet:?dn=Movie",
		"magnet:?xt=urn:btih:not-hex-at-all",
		"magnet:?xt=urn:btih:01234567",
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef0123456",
		"magnet:?xt=urn:btih:AAAQEAYEAUDAOCAJBIFQYDIOB4IBCEQ!",
		"magnet:?xt=urn:sha1:0123456789abcdef0123456789abcdef01234567",
		"https://example.invalid/?xt=urn:btih:01234567",
	} {
		t.Run(raw, func(t *testing.T) {
			if got := redactMagnet(raw); got != "magnet:<invalid>" {
				t.Fatalf("redactMagnet invalid = %q", got)
			}
		})
	}
}
