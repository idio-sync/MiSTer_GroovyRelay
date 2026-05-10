package streams

import (
	"errors"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestResolveStreamURL_MTVChannel(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=metal")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if !matched || res.ProviderID != "mtv-rewind" || res.ChannelID != "metal" {
		t.Fatalf("resolution = %+v matched=%v", res, matched)
	}
}

func TestResolveStreamURLLoadsBundledBeforeStart(t *testing.T) {
	a, err := New(AdapterConfig{Bridge: config.BridgeConfig{DataDir: t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=metal")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if !matched || res.ProviderID != "mtv-rewind" || res.ChannelID != "metal" {
		t.Fatalf("resolution = %+v matched=%v, want mtv-rewind metal", res, matched)
	}
}

func TestResolveStreamURL_MTVShortVideo(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/s/dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if !matched || res.ProviderID != "mtv-rewind" || res.ItemID != "dQw4w9WgXcQ" {
		t.Fatalf("resolution = %+v matched=%v", res, matched)
	}
}

func TestResolveStreamURL_MTVPlayerVideo(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?v=dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if !matched || res.ProviderID != "mtv-rewind" || res.ItemID != "dQw4w9WgXcQ" {
		t.Fatalf("resolution = %+v matched=%v", res, matched)
	}
}

func TestResolveStreamURL_MTVPlayerVideoOutOfCatalog(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	const itemID = "abcdefghijk"

	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?v="+itemID)
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if !matched || res.ProviderID != "mtv-rewind" || res.ItemID != itemID || res.ChannelID != reservedAdhocID {
		t.Fatalf("resolution = %+v matched=%v", res, matched)
	}
}

func TestResolveStreamURL_MTVShortVideoOutOfCatalog(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	const itemID = "zyxwvutsrqp"

	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/s/"+itemID)
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if !matched || res.ProviderID != "mtv-rewind" || res.ItemID != itemID || res.ChannelID != reservedAdhocID {
		t.Fatalf("resolution = %+v matched=%v", res, matched)
	}
}

func TestResolveStreamURLRejectsInvalidYouTubeID(t *testing.T) {
	a := newTestAdapterWithCatalog(t)

	_, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?v=too-short")
	if !matched || err == nil {
		t.Fatal("invalid YouTube ID should match provider and return validation error")
	}
	assertStreamsErrorKind(t, err, ErrKindInvalidExtraction)
}

func TestResolveStreamURL_BareLandingPageNoMatch(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if matched || res.ProviderID != "" || res.ChannelID != "" || res.ItemID != "" {
		t.Fatalf("landing page resolution = %+v matched=%v, want no match", res, matched)
	}
}

func TestResolveStreamURLRejectsRepeatedQueryParam(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	_, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=metal&channel=80s")
	if !matched || err == nil {
		t.Fatal("repeated channel param should match provider and return validation error")
	}
	assertStreamsErrorKind(t, err, ErrKindInvalidExtraction)
}

func TestResolveStreamURLRejectsEmptyQueryParam(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	_, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=")
	if !matched || err == nil {
		t.Fatal("empty channel param should match provider and return validation error")
	}
	assertStreamsErrorKind(t, err, ErrKindInvalidExtraction)
}

func TestResolveStreamURLProviderOrderFollowsDefinitions(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	first := ProviderDefinition{
		ID: "z-first",
		URLRules: []URLRule{{
			ID:         "first-channel",
			Schemes:    []string{"https"},
			Hosts:      []string{"overlap.example"},
			Path:       "/player.html",
			Target:     "channel",
			QueryParam: "channel",
		}},
	}
	second := ProviderDefinition{
		ID: "a-second",
		URLRules: []URLRule{{
			ID:         "second-channel",
			Schemes:    []string{"https"},
			Hosts:      []string{"overlap.example"},
			Path:       "/player.html",
			Target:     "channel",
			QueryParam: "channel",
		}},
	}
	a.replaceDefinitionsForTest([]ProviderDefinition{first, second})
	a.replaceCatalogsForTest([]ProviderCatalog{
		{
			ProviderID: "z-first",
			Name:       "First",
			Channels:   []Channel{{ID: "metal", Name: "Metal"}},
		},
		{
			ProviderID: "a-second",
			Name:       "Second",
			Channels:   []Channel{{ID: "metal", Name: "Metal"}},
		},
	})

	res, matched, err := a.ResolveStreamURL(t.Context(), "https://overlap.example/player.html?channel=metal")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if !matched || res.ProviderID != "z-first" || res.ChannelID != "metal" {
		t.Fatalf("resolution = %+v matched=%v, want first inserted provider", res, matched)
	}
}

func TestStartResolvedStreamRejectsDisabledAdapter(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=metal")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if !matched {
		t.Fatal("expected provider URL to match")
	}

	_, err = a.StartResolvedStream(t.Context(), res)
	if err == nil {
		t.Fatal("StartResolvedStream should reject disabled streams adapter")
	}
	assertStreamsErrorKind(t, err, ErrKindProviderDisabled)
	if !strings.Contains(strings.ToLower(err.Error()), "disabled") {
		t.Fatalf("disabled error should be user-facing, got %q", err.Error())
	}
}

func TestStartResolvedStreamReturnsStartResult(t *testing.T) {
	a := newTestAdapterWithCatalog(t)
	a.SetEnabled(true)

	res, matched, err := a.ResolveStreamURL(t.Context(), "https://wantmymtv.vercel.app/player.html?channel=metal")
	if err != nil {
		t.Fatalf("ResolveStreamURL: %v", err)
	}
	if !matched {
		t.Fatal("expected provider URL to match")
	}

	started, err := a.StartResolvedStream(t.Context(), res)
	if err != nil {
		t.Fatalf("StartResolvedStream: %v", err)
	}
	if !strings.HasPrefix(started.AdapterRef, "streams:") ||
		started.ProviderID != "mtv-rewind" ||
		started.ChannelID != "metal" ||
		started.ItemID != "" {
		t.Fatalf("StartResult = %+v", started)
	}
}

func assertStreamsErrorKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	var streamsErr *StreamsError
	if !errors.As(err, &streamsErr) {
		t.Fatalf("error type = %T, want *StreamsError", err)
	}
	if streamsErr.Kind != want {
		t.Fatalf("error kind = %q, want %q", streamsErr.Kind, want)
	}
}
