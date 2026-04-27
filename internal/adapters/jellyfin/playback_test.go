package jellyfin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
