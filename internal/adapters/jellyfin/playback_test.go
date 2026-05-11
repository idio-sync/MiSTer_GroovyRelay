package jellyfin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

func TestFetchPlaybackInfo_BodyShape(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/Items/") || !strings.HasSuffix(r.URL.Path, "/PlaybackInfo") {
			t.Errorf("path = %q", r.URL.Path)
		}
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"MediaSources":[{"Id":"src-1","TranscodingUrl":"/videos/itm/master.m3u8?MediaSourceId=src-1&PlaySessionId=ps-7"}],
			"PlaySessionId":"ps-7"
		}`))
	}))
	defer srv.Close()

	res, err := FetchPlaybackInfo(t.Context(), PlaybackInfoInput{
		ServerURL:           srv.URL,
		Token:               "tok",
		DeviceID:            "dev",
		Version:             "v",
		ItemID:              "itm",
		UserID:              "uid",
		MaxVideoBitrateKbps: 4000,
		StartPositionTicks:  60_0000_0000, // 6 seconds
		Preset:              mustPreset(t, "PAL_288p"),
	})
	if err != nil {
		t.Fatalf("FetchPlaybackInfo: %v", err)
	}
	if res.PlaySessionID != "ps-7" {
		t.Errorf("PlaySessionID = %q", res.PlaySessionID)
	}
	if !strings.Contains(res.TranscodingURL, "/videos/itm/master.m3u8") {
		t.Errorf("TranscodingURL = %q", res.TranscodingURL)
	}

	var body map[string]any
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatal(err)
	}
	wantTrue := []string{"EnableTranscoding", "AlwaysBurnInSubtitleWhenTranscoding"}
	wantFalse := []string{"EnableDirectPlay", "EnableDirectStream"}
	for _, k := range wantTrue {
		if body[k] != true {
			t.Errorf("body[%s] = %v, want true", k, body[k])
		}
	}
	for _, k := range wantFalse {
		if body[k] != false {
			t.Errorf("body[%s] = %v, want false", k, body[k])
		}
	}
	if body["UserId"] != "uid" {
		t.Errorf("UserId = %v", body["UserId"])
	}
	if got := body["MaxStreamingBitrate"].(float64); got != 4_000_000 {
		t.Errorf("MaxStreamingBitrate = %v, want 4000000", got)
	}
	dp := body["DeviceProfile"].(map[string]any)
	codecProfiles := dp["CodecProfiles"].([]any)
	conditions := codecProfiles[0].(map[string]any)["Conditions"].([]any)
	foundPALHeight := false
	foundPALFPS := false
	for _, raw := range conditions {
		cond := raw.(map[string]any)
		if cond["Property"] == "Height" && cond["Value"] == "288" {
			foundPALHeight = true
		}
		if cond["Property"] == "VideoFramerate" && cond["Value"] == "50" {
			foundPALFPS = true
		}
	}
	if !foundPALHeight || !foundPALFPS {
		t.Errorf("DeviceProfile conditions did not carry PAL_288p shape: %#v", conditions)
	}
}

func TestFetchPlaybackInfo_AudioBodyUsesAudioProfile(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"MediaSources":[{"Id":"src-audio","TranscodingUrl":"/audio/itm/universal?MediaSourceId=src-audio"}],
			"PlaySessionId":"ps-audio",
			"Item":{"Type":"Audio","Name":"Blue Monday"}
		}`))
	}))
	defer srv.Close()

	_, err := FetchPlaybackInfo(t.Context(), PlaybackInfoInput{
		ServerURL: srv.URL, Token: "tok", DeviceID: "dev", Version: "v",
		ItemID: "itm", UserID: "uid", MaxVideoBitrateKbps: 4000,
		Preset: mustPreset(t, "NTSC_480i"), MediaKind: core.MediaKindMusic,
	})
	if err != nil {
		t.Fatalf("FetchPlaybackInfo: %v", err)
	}

	var body struct {
		DeviceProfile DeviceProfile `json:"DeviceProfile"`
	}
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.DeviceProfile.TranscodingProfiles) != 1 {
		t.Fatalf("TranscodingProfiles len = %d, want 1", len(body.DeviceProfile.TranscodingProfiles))
	}
	profile := body.DeviceProfile.TranscodingProfiles[0]
	if profile.Type != "Audio" || profile.Container != "mp3" || profile.AudioCodec != "mp3" {
		t.Fatalf("audio profile = %+v, want Audio/mp3/mp3", profile)
	}
	if strings.Contains(string(gotBody), "VideoCodec") {
		t.Fatalf("audio PlaybackInfo body contains VideoCodec: %s", string(gotBody))
	}
}

func TestFetchPlaybackInfo_RetriesWithAudioProfileWhenResponseIdentifiesAudio(t *testing.T) {
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			_, _ = w.Write([]byte(`{
				"MediaSources":[{"Id":"src-video-profile","TranscodingUrl":"/videos/song-1/master.m3u8"}],
				"PlaySessionId":"ps-video-profile",
				"Item":{"Type":"Audio","Name":"Blue Monday"}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"MediaSources":[{"Id":"src-audio","TranscodingUrl":"/audio/song-1/universal?MediaSourceId=src-audio"}],
			"PlaySessionId":"ps-audio",
			"Item":{"Type":"Audio","Name":"Blue Monday"}
		}`))
	}))
	defer srv.Close()

	result, err := FetchPlaybackInfo(t.Context(), PlaybackInfoInput{
		ServerURL: srv.URL, Token: "tok", DeviceID: "dev", Version: "v",
		ItemID: "song-1", UserID: "uid", MaxVideoBitrateKbps: 4000,
		Preset: mustPreset(t, "NTSC_480i"),
	})
	if err != nil {
		t.Fatalf("FetchPlaybackInfo: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("PlaybackInfo calls = %d, want 2", len(bodies))
	}
	if !strings.Contains(string(bodies[0]), `"Type":"Video"`) {
		t.Fatalf("first PlaybackInfo body did not use video profile: %s", bodies[0])
	}
	if !strings.Contains(string(bodies[1]), `"Type":"Audio"`) || strings.Contains(string(bodies[1]), "VideoCodec") {
		t.Fatalf("second PlaybackInfo body did not use clean audio profile: %s", bodies[1])
	}
	if result.MediaKind != core.MediaKindMusic {
		t.Fatalf("MediaKind = %q, want music", result.MediaKind)
	}
	if !strings.Contains(result.TranscodingURL, "/audio/song-1/universal") {
		t.Fatalf("TranscodingURL = %q, want audio retry URL", result.TranscodingURL)
	}
}

func TestFetchPlaybackInfo_RetriesAudioProfileOnNoCompatibleStreamWhenRetryIdentifiesAudio(t *testing.T) {
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			_, _ = w.Write([]byte(`{"ErrorCode":"NoCompatibleStream","MediaSources":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"MediaSources":[{"Id":"src-audio","TranscodingUrl":"/audio/song-1/universal?MediaSourceId=src-audio"}],
			"PlaySessionId":"ps-audio",
			"Item":{"Type":"Audio","Name":"Blue Monday"}
		}`))
	}))
	defer srv.Close()

	result, err := FetchPlaybackInfo(t.Context(), PlaybackInfoInput{
		ServerURL: srv.URL, Token: "tok", DeviceID: "dev", Version: "v",
		ItemID: "song-1", UserID: "uid", MaxVideoBitrateKbps: 4000,
		Preset: mustPreset(t, "NTSC_480i"),
	})
	if err != nil {
		t.Fatalf("FetchPlaybackInfo: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("PlaybackInfo calls = %d, want 2", len(bodies))
	}
	if !strings.Contains(string(bodies[1]), `"Type":"Audio"`) || strings.Contains(string(bodies[1]), "VideoCodec") {
		t.Fatalf("retry PlaybackInfo body did not use clean audio profile: %s", bodies[1])
	}
	if result.MediaKind != core.MediaKindMusic {
		t.Fatalf("MediaKind = %q, want music", result.MediaKind)
	}
}

func TestFetchPlaybackInfo_ErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ErrorCode":"NoCompatibleStream","MediaSources":[]}`))
	}))
	defer srv.Close()

	_, err := FetchPlaybackInfo(t.Context(), PlaybackInfoInput{
		ServerURL: srv.URL, Token: "x", DeviceID: "y", Version: "z",
		ItemID: "i", UserID: "u", MaxVideoBitrateKbps: 4000,
		Preset: mustPreset(t, "NTSC_480i"),
	})
	if err == nil {
		t.Fatal("FetchPlaybackInfo with ErrorCode returned nil, want error")
	}
	if !strings.Contains(err.Error(), "NoCompatibleStream") {
		t.Errorf("err = %v", err)
	}
}

func TestBuildAbsoluteStreamURL_AddsAPIKeyWhenMissing(t *testing.T) {
	got := BuildAbsoluteStreamURL("https://jf.example.com",
		"/videos/itm/master.m3u8?MediaSourceId=src&PlaySessionId=ps", "tok")
	if !strings.Contains(got, "api_key=tok") {
		t.Errorf("expected api_key in: %s", got)
	}
	if !strings.HasPrefix(got, "https://jf.example.com/videos/") {
		t.Errorf("absolute URL = %s", got)
	}
}

func TestBuildAbsoluteStreamURL_NoDoubleAPIKey(t *testing.T) {
	got := BuildAbsoluteStreamURL("https://jf.example.com",
		"/videos/itm/master.m3u8?MediaSourceId=src&api_key=already", "tok")
	count := strings.Count(got, "api_key=")
	if count != 1 {
		t.Errorf("api_key appears %d times, want 1: %s", count, got)
	}
	if !strings.Contains(got, "api_key=already") {
		t.Errorf("existing api_key replaced: %s", got)
	}
}

func TestFetchPlaybackInfo_DecodesTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"MediaSources":[{"Id":"src1","Name":"Source 1","TranscodingUrl":"/x"}],
			"PlaySessionId":"session-abc",
			"Item":{"Name":"Game of Thrones · S01E03"}
		}`))
	}))
	defer srv.Close()

	result, err := FetchPlaybackInfo(t.Context(), PlaybackInfoInput{
		ServerURL: srv.URL, Token: "x", DeviceID: "y", Version: "z",
		ItemID: "i", UserID: "u", MaxVideoBitrateKbps: 4000,
		Preset: mustPreset(t, "NTSC_480i"),
	})
	if err != nil {
		t.Fatalf("FetchPlaybackInfo: %v", err)
	}
	if result.Title != "Game of Thrones · S01E03" {
		t.Errorf("Title: got %q, want %q", result.Title, "Game of Thrones · S01E03")
	}
}

func TestFetchPlaybackInfo_DecodesAudioMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"MediaSources":[{"Id":"src1","Name":"Source 1","TranscodingUrl":"/audio/itm/universal?MediaSourceId=src1"}],
			"PlaySessionId":"session-abc",
			"Item":{
				"Type":"Audio",
				"Name":"Blue Monday",
				"Artists":["New Order"],
				"Album":"Power Corruption & Lies",
				"RunTimeTicks":4500000000
			}
		}`))
	}))
	defer srv.Close()

	result, err := FetchPlaybackInfo(t.Context(), PlaybackInfoInput{
		ServerURL: srv.URL, Token: "x", DeviceID: "y", Version: "z",
		ItemID: "i", UserID: "u", MaxVideoBitrateKbps: 4000,
		Preset: mustPreset(t, "NTSC_480i"), MediaKind: core.MediaKindMusic,
	})
	if err != nil {
		t.Fatalf("FetchPlaybackInfo: %v", err)
	}
	if result.MediaKind != core.MediaKindMusic {
		t.Errorf("MediaKind = %q, want music", result.MediaKind)
	}
	if result.Title != "Blue Monday" {
		t.Errorf("Title = %q, want Blue Monday", result.Title)
	}
	if result.Artist != "New Order" {
		t.Errorf("Artist = %q, want New Order", result.Artist)
	}
	if result.Album != "Power Corruption & Lies" {
		t.Errorf("Album = %q, want Power Corruption & Lies", result.Album)
	}
	if result.Duration != 7*time.Minute+30*time.Second {
		t.Errorf("Duration = %v, want 7m30s", result.Duration)
	}
}

func TestFetchItemMetadata_DecodesAudioMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Items/song-1" {
			t.Errorf("path = %q, want /Items/song-1", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"Id":"song-1",
			"Type":"Audio",
			"Name":"Age of Consent",
			"Artists":["New Order"],
			"Album":"Power Corruption & Lies",
			"RunTimeTicks":3150000000
		}`))
	}))
	defer srv.Close()

	result, err := FetchItemMetadata(t.Context(), ItemMetadataInput{
		ServerURL: srv.URL, Token: "tok", DeviceID: "dev", DeviceName: "Groovy",
		Version: "v", UserID: "uid", ItemID: "song-1",
	})
	if err != nil {
		t.Fatalf("FetchItemMetadata: %v", err)
	}
	if result.MediaKind != core.MediaKindMusic {
		t.Errorf("MediaKind = %q, want music", result.MediaKind)
	}
	if result.Title != "Age of Consent" || result.Artist != "New Order" || result.Album != "Power Corruption & Lies" {
		t.Errorf("metadata = %+v", result)
	}
	if result.Duration != 5*time.Minute+15*time.Second {
		t.Errorf("Duration = %v, want 5m15s", result.Duration)
	}
}

func TestMergePlaybackMetadata_ReplacesItemIDTitleFallback(t *testing.T) {
	got := mergePlaybackMetadata(
		PlaybackInfoResult{Title: "song-1", MediaKind: core.MediaKindMusic},
		ItemMetadataResult{ItemID: "song-1", Title: "Age of Consent", MediaKind: core.MediaKindMusic},
	)
	if got.Title != "Age of Consent" {
		t.Fatalf("Title = %q, want metadata title", got.Title)
	}
}

func TestFetchPlaybackInfo_TitleFallsBackToSourceName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// No Item.Name — should fall back to MediaSource.Name
		_, _ = w.Write([]byte(`{
			"MediaSources":[{"Id":"src1","Name":"Fallback Source","TranscodingUrl":"/x"}],
			"PlaySessionId":"session-abc"
		}`))
	}))
	defer srv.Close()

	result, err := FetchPlaybackInfo(t.Context(), PlaybackInfoInput{
		ServerURL: srv.URL, Token: "x", DeviceID: "y", Version: "z",
		ItemID: "i", UserID: "u", MaxVideoBitrateKbps: 4000,
		Preset: mustPreset(t, "NTSC_480i"),
	})
	if err != nil {
		t.Fatalf("FetchPlaybackInfo: %v", err)
	}
	if result.Title != "Fallback Source" {
		t.Errorf("Title fallback: got %q, want %q", result.Title, "Fallback Source")
	}
}
