package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type fakePlaybackAdapter struct {
	name        string
	displayName string
	enabled     bool
	view        adapters.PlaybackBannerAdapterView
	owns        bool
	tabs        []adapters.QuickCastTab
}

func (f *fakePlaybackAdapter) Name() string { return f.name }
func (f *fakePlaybackAdapter) DisplayName() string {
	if f.displayName != "" {
		return f.displayName
	}
	return f.name
}
func (f *fakePlaybackAdapter) Fields() []adapters.FieldDef { return nil }
func (f *fakePlaybackAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error {
	return nil
}
func (f *fakePlaybackAdapter) IsEnabled() bool {
	return f.enabled
}
func (f *fakePlaybackAdapter) Start(context.Context) error { return nil }
func (f *fakePlaybackAdapter) Stop() error                 { return nil }
func (f *fakePlaybackAdapter) Status() adapters.Status {
	return adapters.Status{State: adapters.StateRunning}
}
func (f *fakePlaybackAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}
func (f *fakePlaybackAdapter) PlaybackBanner(context.Context, adapters.PlaybackBannerSnapshot) (adapters.PlaybackBannerAdapterView, bool) {
	return f.view, f.owns
}
func (f *fakePlaybackAdapter) HandlePlaybackAction(context.Context, adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	return adapters.PlaybackActionResult{}, nil
}
func (f *fakePlaybackAdapter) QuickCastTabs() []adapters.QuickCastTab { return f.tabs }
func (f *fakePlaybackAdapter) HandleQuickCast(context.Context, adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	return adapters.QuickCastResult{}, nil
}

type fakeBareAdapter struct {
	name        string
	displayName string
	enabled     bool
}

func (f fakeBareAdapter) Name() string { return f.name }
func (f fakeBareAdapter) DisplayName() string {
	if f.displayName != "" {
		return f.displayName
	}
	return f.name
}
func (f fakeBareAdapter) Fields() []adapters.FieldDef { return nil }
func (f fakeBareAdapter) DecodeConfig(toml.Primitive, toml.MetaData) error {
	return nil
}
func (f fakeBareAdapter) IsEnabled() bool             { return f.enabled }
func (f fakeBareAdapter) Start(context.Context) error { return nil }
func (f fakeBareAdapter) Stop() error                 { return nil }
func (f fakeBareAdapter) Status() adapters.Status {
	return adapters.Status{State: adapters.StateRunning}
}
func (f fakeBareAdapter) ApplyConfig(toml.Primitive, toml.MetaData) (adapters.ApplyScope, error) {
	return adapters.ScopeHotSwap, nil
}

func TestPlaybackBannerIdleRendersReadyAndQuickCast(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		tabs: []adapters.QuickCastTab{{
			ID:       "url",
			Label:    "URL",
			Enabled:  true,
			Encoding: adapters.QuickCastEncodingForm,
			Fields:   []adapters.QuickCastField{{Name: "url", Label: "URL", Type: "url", Required: true}},
		}},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})

	r := httptest.NewRequest(http.MethodGet, "/ui/playback/banner", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, body)
	}
	for _, want := range []string{`id="gr-now-playing"`, "Ready to cast", "Cast", "URL", `hx-trigger="every 5s"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("banner missing %q: %s", want, body)
		}
	}
}

func TestPlaybackBannerPlayingRendersProviderActionsAndTimeline(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		owns:    true,
		view: adapters.PlaybackBannerAdapterView{
			Actions: []adapters.PlaybackAction{{ID: adapters.PlaybackActionPause, Label: "Pause", Icon: "pause", Enabled: true}},
			Seek:    &adapters.PlaybackSeek{Enabled: true, OffsetMS: 90000, DurationMS: 600000},
		},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{
			State:      core.StatePlaying,
			Source:     "url",
			Title:      "Movie Night",
			AdapterRef: "url:abc",
			Generation: 11,
			Position:   90 * time.Second,
			Duration:   10 * time.Minute,
		}}
	})

	r := httptest.NewRequest(http.MethodGet, "/ui/playback/banner", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{"Now Playing", "Movie Night", "01:30", "10:00", `name="generation" value="11"`, "Pause", `hx-trigger="every 1s"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("banner missing %q: %s", want, body)
		}
	}
}

func TestPlaybackBannerRendersQuickCastRadioOptionsAndDisabledReason(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		tabs: []adapters.QuickCastTab{{
			ID:             "url",
			Label:          "URL",
			Enabled:        false,
			DisabledReason: "url adapter is disabled",
			Encoding:       adapters.QuickCastEncodingForm,
			Fields: []adapters.QuickCastField{{
				Name: "mode", Label: "Mode", Type: "radio", Required: true,
				Options: []adapters.QuickCastOption{{Value: "auto", Label: "Auto"}, {Value: "ytdlp", Label: "yt-dlp"}, {Value: "direct", Label: "Direct"}},
			}},
		}},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})
	r := httptest.NewRequest(http.MethodGet, "/ui/playback/banner?drawer=cast", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{`type="radio"`, `name="mode" value="auto"`, `name="mode" value="ytdlp"`, `name="mode" value="direct"`, "url adapter is disabled"} {
		if !strings.Contains(body, want) {
			t.Fatalf("banner missing %q: %s", want, body)
		}
	}
}

func TestPlaybackBannerOpenDrawerDoesNotPollAwayForm(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		tabs: []adapters.QuickCastTab{{
			ID:       "url",
			Label:    "URL",
			Enabled:  true,
			Encoding: adapters.QuickCastEncodingForm,
			Fields: []adapters.QuickCastField{{
				Name: "url", Label: "URL", Type: "url", Required: true,
			}},
		}},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})
	r := httptest.NewRequest(http.MethodGet, "/ui/playback/banner?drawer=cast", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "gr-now-playing-drawer") {
		t.Fatalf("drawer did not render: %s", body)
	}
	if strings.Contains(body, `hx-trigger="every`) {
		t.Fatalf("open drawer should not poll and discard in-progress form input: %s", body)
	}
	if strings.Contains(body, `<section id="gr-now-playing" class="gr-now-playing gr-now-playing--top" hx-get=`) {
		t.Fatalf("open drawer should not have section-level htmx trigger defaults: %s", body)
	}
}

func TestPlaybackBannerUsesAdapterRefFallbackWhenSourceMissing(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		owns:    true,
		view: adapters.PlaybackBannerAdapterView{
			SourceDisplay: "URL",
			Actions: []adapters.PlaybackAction{{
				ID:      adapters.PlaybackActionStop,
				Label:   "Stop",
				Icon:    "stop",
				Enabled: true,
			}},
		},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StatePlaying, AdapterRef: "url:abc", Generation: 11, Title: "Legacy URL"}}
	})
	r := httptest.NewRequest(http.MethodGet, "/ui/playback/banner", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{"Legacy URL", "URL", `name="action" value="stop"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("legacy fallback banner missing %q: %s", want, body)
		}
	}
}

func TestPlaybackBannerActiveNonProviderIsReadOnly(t *testing.T) {
	bare := fakeBareAdapter{name: "plex", displayName: "Plex", enabled: true}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(bare)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StatePlaying, Source: "plex", AdapterRef: "plex:/library/metadata/1", Generation: 12, Title: "Plex Movie"}}
	})
	r := httptest.NewRequest(http.MethodGet, "/ui/playback/banner", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{"Plex Movie", "Plex", `hx-trigger="every 5s"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("read-only banner missing %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{`/ui/playback/action`, `gr-now-playing-seek`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("read-only banner rendered control %q: %s", forbidden, body)
		}
	}
}
