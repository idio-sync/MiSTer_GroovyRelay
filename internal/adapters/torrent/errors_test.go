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

func TestRedactMagnetInvalid(t *testing.T) {
	if got := redactMagnet("magnet:?dn=Movie"); got != "magnet:<invalid>" {
		t.Fatalf("redactMagnet invalid = %q", got)
	}
	if got := redactMagnet("magnet:?xt=urn:btih:not-hex-at-all"); got != "magnet:<invalid>" {
		t.Fatalf("redactMagnet non-hex = %q", got)
	}
	if got := redactMagnet("https://example.invalid/?xt=urn:btih:01234567"); got != "magnet:<invalid>" {
		t.Fatalf("redactMagnet wrong scheme = %q", got)
	}
}
