package jellyfin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostCapabilities_BodyShape(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	err := PostCapabilities(t.Context(), CapabilitiesInput{
		ServerURL:           srv.URL,
		Token:               "tok-1",
		DeviceID:            "device-uuid",
		DeviceName:          "Living Room MiSTer",
		Version:             "0.1.0",
		MaxVideoBitrateKbps: 4000,
	})
	if err != nil {
		t.Fatalf("PostCapabilities: %v", err)
	}
	if gotPath != "/Sessions/Capabilities/Full" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotAuth, `Token="tok-1"`) {
		t.Errorf("auth = %q", gotAuth)
	}
	if !strings.Contains(gotAuth, `Device="Living Room MiSTer"`) {
		t.Errorf("auth = %q", gotAuth)
	}

	var body map[string]any
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("body unmarshal: %v\nbody:%s", err, gotBody)
	}
	if body["SupportsMediaControl"] != true {
		t.Errorf("SupportsMediaControl = %v, want true", body["SupportsMediaControl"])
	}
	if body["SupportsPersistentIdentifier"] != true {
		t.Errorf("SupportsPersistentIdentifier = %v", body["SupportsPersistentIdentifier"])
	}
	pmt, _ := body["PlayableMediaTypes"].([]any)
	if len(pmt) == 0 {
		t.Errorf("PlayableMediaTypes empty")
	}
	cmds, _ := body["SupportedCommands"].([]any)
	wantCmds := []string{
		"Play", "PlayState", "PlayNext", "PlayMediaSource",
		"VolumeUp", "VolumeDown", "Mute", "Unmute", "ToggleMute", "SetVolume",
		"SetAudioStreamIndex", "SetSubtitleStreamIndex",
		"SetMaxStreamingBitrate", "SetRepeatMode", "DisplayMessage",
	}
	cmdSet := map[string]bool{}
	for _, v := range cmds {
		cmdSet[v.(string)] = true
	}
	for _, want := range wantCmds {
		if !cmdSet[want] {
			t.Errorf("SupportedCommands missing %q", want)
		}
	}
	dp, _ := body["DeviceProfile"].(map[string]any)
	if dp["Name"] != "MiSTer_GroovyRelay" {
		t.Errorf("DeviceProfile.Name = %v", dp["Name"])
	}
}

func TestPostCapabilities_NonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := PostCapabilities(t.Context(), CapabilitiesInput{
		ServerURL: srv.URL, Token: "x", DeviceID: "y", Version: "z", MaxVideoBitrateKbps: 4000,
	})
	if err == nil {
		t.Fatal("PostCapabilities(500) returned nil, want error")
	}
}
