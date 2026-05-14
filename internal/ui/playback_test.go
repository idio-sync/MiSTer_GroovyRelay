package ui

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	actionCalls []adapters.PlaybackActionRequest
	actionMsg   string
	quickCalls  []adapters.QuickCastRequest
	quickMsg    string
	quickErr    error
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
func (f *fakePlaybackAdapter) HandlePlaybackAction(_ context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	f.actionCalls = append(f.actionCalls, req)
	return adapters.PlaybackActionResult{Message: f.actionMsg}, nil
}
func (f *fakePlaybackAdapter) QuickCastTabs() []adapters.QuickCastTab { return f.tabs }
func (f *fakePlaybackAdapter) HandleQuickCast(_ context.Context, req adapters.QuickCastRequest) (adapters.QuickCastResult, error) {
	f.quickCalls = append(f.quickCalls, req)
	if f.quickErr != nil {
		return adapters.QuickCastResult{}, f.quickErr
	}
	return adapters.QuickCastResult{Message: f.quickMsg}, nil
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
			Actions:  []adapters.PlaybackAction{{ID: adapters.PlaybackActionPause, Label: "Pause", Icon: "pause", Enabled: true}},
			Seek:     &adapters.PlaybackSeek{Enabled: true, OffsetMS: 90000, DurationMS: 600000},
			Subtitle: "NTSC_480i",
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
	for _, want := range []string{
		`class="gr-now-playing-kicker"`,
		"Now Playing",
		"Movie Night",
		"NTSC_480i",
		"01:30",
		"10:00",
		`class="gr-now-playing-progress"`,
		`class="gr-playback-action-form"`,
		`class="gr-playback-action"`,
		`name="generation" value="11"`,
		"Pause",
		`hx-trigger="every 1s"`,
		`class="gr-now-playing-seek" hx-post="/ui/playback/seek" hx-trigger="change"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("banner missing %q: %s", want, body)
		}
	}
}

func TestPlaybackBannerWithoutQuickCastProviderDoesNotOpenCastDrawer(t *testing.T) {
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fakeBareAdapter{name: "plex", displayName: "Plex", enabled: true})
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})

	r := httptest.NewRequest(http.MethodGet, "/ui/playback/banner?drawer=cast", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	body := w.Body.String()
	for _, forbidden := range []string{`drawer=cast`, `>Cast<`, "gr-now-playing-drawer"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("banner without quick-cast provider rendered %q: %s", forbidden, body)
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
	for _, want := range []string{`gr-quick-cast-panel`, `class="gr-quick-cast-tab"`, `class="gr-quick-cast-submit"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("drawer missing preview structure %q: %s", want, body)
		}
	}
	if strings.Contains(body, `hx-trigger="every`) {
		t.Fatalf("open drawer should not poll and discard in-progress form input: %s", body)
	}
	if strings.Contains(body, `<section id="gr-now-playing" class="gr-now-playing gr-now-playing--top" hx-get=`) {
		t.Fatalf("open drawer should not have section-level htmx trigger defaults: %s", body)
	}
	if !strings.Contains(body, `aria-expanded="true"`) || !strings.Contains(body, `hx-get="/ui/playback/banner"`) {
		t.Fatalf("open drawer Cast button should close the drawer: %s", body)
	}
	if strings.Contains(body, `hx-get="/ui/playback/banner?drawer=cast" hx-target="#gr-now-playing" hx-swap="outerHTML">Cast</button>`) {
		t.Fatalf("open drawer Cast button still points at open drawer route: %s", body)
	}
}

func TestPlaybackBannerQuickCastDefaultsFirstRadioOption(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		tabs: []adapters.QuickCastTab{{
			ID:       "url",
			Label:    "URL",
			Enabled:  true,
			Encoding: adapters.QuickCastEncodingForm,
			Fields: []adapters.QuickCastField{
				{Name: "url", Label: "URL", Type: "url", Required: true},
				{
					Name: "mode", Label: "Mode", Type: "radio", Required: true,
					Options: []adapters.QuickCastOption{{Value: "auto", Label: "Auto"}, {Value: "ytdlp", Label: "yt-dlp"}, {Value: "direct", Label: "Direct"}},
				},
			},
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
	for _, want := range []string{
		`class="gr-quick-cast-field gr-quick-cast-field--input"`,
		`class="gr-quick-cast-field gr-quick-cast-field--radio"`,
		`class="gr-quick-cast-option"`,
		`name="mode" value="auto" required checked`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("quick-cast form missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `name="mode" value="ytdlp" required checked`) || strings.Contains(body, `name="mode" value="direct" required checked`) {
		t.Fatalf("only the first radio option should default checked: %s", body)
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

func TestPlaybackActionDispatchesToOwningProvider(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:      "url",
		enabled:   true,
		owns:      true,
		actionMsg: "paused",
		view: adapters.PlaybackBannerAdapterView{
			Actions: []adapters.PlaybackAction{{ID: adapters.PlaybackActionPause, Label: "Pause", Enabled: true}},
		},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StatePlaying, Source: "url", AdapterRef: "url:abc", Generation: 9}}
	})
	form := url.Values{"action": {"pause"}, "adapter_ref": {"url:abc"}, "generation": {"9"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.actionCalls) != 1 || fake.actionCalls[0].Action != "pause" || fake.actionCalls[0].Generation != 9 {
		t.Fatalf("action calls = %#v", fake.actionCalls)
	}
	if !strings.Contains(rr.Body.String(), "paused") {
		t.Fatalf("success message missing: %s", rr.Body.String())
	}
}

func TestPlaybackActionRejectsSameAdapterStaleGenerationBeforeDispatch(t *testing.T) {
	fake := &fakePlaybackAdapter{name: "url", enabled: true, owns: true}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StatePlaying, Source: "url", AdapterRef: "url:abc", Generation: 10}}
	})
	form := url.Values{"action": {"stop"}, "adapter_ref": {"url:abc"}, "generation": {"9"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.actionCalls) != 0 {
		t.Fatalf("provider was called for stale action: %#v", fake.actionCalls)
	}
	if !strings.Contains(rr.Body.String(), "active session changed") {
		t.Fatalf("stale error missing: %s", rr.Body.String())
	}
}

func TestPlaybackActionRejectsActiveAdapterWithoutPlaybackProvider(t *testing.T) {
	bare := fakeBareAdapter{name: "plex", displayName: "Plex", enabled: true}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(bare)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StatePlaying, Source: "plex", AdapterRef: "plex:/library/metadata/1", Generation: 10}}
	})
	form := url.Values{"action": {"stop"}, "adapter_ref": {"plex:/library/metadata/1"}, "generation": {"10"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "active adapter does not expose playback controls") {
		t.Fatalf("missing non-provider error: %s", rr.Body.String())
	}
}

func TestPlaybackActionRejectsProviderThatDoesNotOwnSnapshot(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		owns:    false,
		view: adapters.PlaybackBannerAdapterView{
			Actions: []adapters.PlaybackAction{{ID: adapters.PlaybackActionStop, Label: "Stop", Enabled: true}},
		},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StatePlaying, Source: "url", AdapterRef: "url:abc", Generation: 10}}
	})
	form := url.Values{"action": {"stop"}, "adapter_ref": {"url:abc"}, "generation": {"10"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.actionCalls) != 0 {
		t.Fatalf("provider was called despite owns=false: %#v", fake.actionCalls)
	}
	if !strings.Contains(rr.Body.String(), "playback action unavailable") {
		t.Fatalf("missing owns=false error: %s", rr.Body.String())
	}
}

func TestPlaybackActionRejectsSeekOnActionRoute(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		owns:    true,
		view: adapters.PlaybackBannerAdapterView{
			Actions: []adapters.PlaybackAction{{ID: adapters.PlaybackActionSeek, Label: "Seek", Enabled: true}},
			Seek:    &adapters.PlaybackSeek{Enabled: true, OffsetMS: 0, DurationMS: 60000},
		},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StatePlaying, Source: "url", AdapterRef: "url:abc", Generation: 10, Duration: time.Minute}}
	})
	form := url.Values{"action": {adapters.PlaybackActionSeek}, "adapter_ref": {"url:abc"}, "generation": {"10"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.actionCalls) != 0 {
		t.Fatalf("seek dispatched through action route: %#v", fake.actionCalls)
	}
	if !strings.Contains(rr.Body.String(), "seek must use seek route") {
		t.Fatalf("missing action-route seek error: %s", rr.Body.String())
	}
}

func TestPlaybackSeekDispatchesOffset(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		owns:    true,
		view: adapters.PlaybackBannerAdapterView{
			Seek: &adapters.PlaybackSeek{Enabled: true, OffsetMS: 0, DurationMS: 60000},
		},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StatePlaying, Source: "url", AdapterRef: "url:abc", Generation: 12, Duration: time.Minute}}
	})
	form := url.Values{"adapter_ref": {"url:abc"}, "generation": {"12"}, "offset_ms": {"42000"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/seek", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.actionCalls) != 1 || fake.actionCalls[0].Action != adapters.PlaybackActionSeek || fake.actionCalls[0].OffsetMS != 42000 {
		t.Fatalf("seek calls = %#v", fake.actionCalls)
	}
}

func TestQuickCastDispatchesToSelectedProvider(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:     "url",
		enabled:  true,
		quickMsg: "started",
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
	form := url.Values{"tab_id": {"url"}, "url": {"https://example.test/video.mp4"}, "mode": {"direct"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/quick-cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.quickCalls) != 1 || fake.quickCalls[0].Values["url"] != "https://example.test/video.mp4" {
		t.Fatalf("quick calls = %#v", fake.quickCalls)
	}
	if !strings.Contains(rr.Body.String(), "started") {
		t.Fatalf("quick-cast success missing: %s", rr.Body.String())
	}
}

func TestQuickCastErrorKeepsDrawerOpen(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:     "url",
		enabled:  true,
		quickErr: errors.New("bad url"),
		tabs:     []adapters.QuickCastTab{{ID: "url", Label: "URL", Enabled: true, Encoding: adapters.QuickCastEncodingForm}},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})
	form := url.Values{"tab_id": {"url"}, "url": {"not-a-url"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/quick-cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"bad url", "gr-now-playing-drawer"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("quick-cast error response missing %q: %s", want, rr.Body.String())
		}
	}
}

func TestQuickCastRejectsDisabledTabBeforeProviderDispatch(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:    "url",
		enabled: true,
		tabs: []adapters.QuickCastTab{{
			ID:             "url",
			Label:          "URL",
			Enabled:        false,
			DisabledReason: "url adapter is disabled",
			Encoding:       adapters.QuickCastEncodingForm,
		}},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})
	form := url.Values{"tab_id": {"url"}, "url": {"https://example.test/video.mp4"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/quick-cast", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.quickCalls) != 0 {
		t.Fatalf("disabled quick-cast tab dispatched to provider: %#v", fake.quickCalls)
	}
	if !strings.Contains(rr.Body.String(), "url adapter is disabled") {
		t.Fatalf("disabled reason missing: %s", rr.Body.String())
	}
}

func TestQuickCastMultipartTempFileRemovedAfterProviderReturns(t *testing.T) {
	fake := &fakePlaybackAdapter{
		name:     "torrent",
		enabled:  true,
		quickMsg: "started",
		tabs: []adapters.QuickCastTab{{
			ID:       "torrent-file",
			Label:    "Torrent File",
			Enabled:  true,
			Encoding: adapters.QuickCastEncodingMultipart,
		}},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("tab_id", "torrent-file")
	part, err := mw.CreateFormFile("torrent_file", "near-limit.torrent")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = part.Write(bytes.Repeat([]byte("x"), (4<<20)+1))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/quick-cast", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.quickCalls) != 1 || fake.quickCalls[0].File == nil {
		t.Fatalf("quick calls = %#v", fake.quickCalls)
	}
	file, err := fake.quickCalls[0].File.Header.Open()
	if err == nil {
		file.Close()
		t.Fatalf("multipart temp file still opens after handler returned")
	}
}

func TestQuickCastMultipartBodyIsCappedBeforeParsing(t *testing.T) {
	const oversizeQuickCastMultipartBytes = 4*1024*1024 + 64*1024 + 1
	fake := &fakePlaybackAdapter{
		name:    "torrent",
		enabled: true,
		tabs: []adapters.QuickCastTab{{
			ID:       "torrent-file",
			Label:    "Torrent File",
			Enabled:  true,
			Encoding: adapters.QuickCastEncodingMultipart,
		}},
	}
	_, mux := newTestServer(t, func(c *Config) {
		c.Registry = adapters.NewRegistryWith(fake)
		c.StatusViewer = fakeStatusViewer{v: core.StatusHomeView{State: core.StateIdle}}
	})
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("tab_id", "torrent-file")
	part, err := mw.CreateFormFile("torrent_file", "huge.torrent")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = part.Write(bytes.Repeat([]byte("x"), oversizeQuickCastMultipartBytes))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/ui/playback/quick-cast", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.quickCalls) != 0 {
		t.Fatalf("oversized multipart dispatched to provider: %#v", fake.quickCalls)
	}
	if !strings.Contains(rr.Body.String(), "parse multipart") {
		t.Fatalf("multipart cap error missing: %s", rr.Body.String())
	}
}
