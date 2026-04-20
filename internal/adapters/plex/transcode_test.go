package plex

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildTranscodeURL_ContainsExpectedParams(t *testing.T) {
	req := TranscodeRequest{
		PlexServerURL: "http://192.168.1.10:32400",
		MediaPath:     "/library/metadata/42",
		Token:         "xyz",
		OffsetMs:      0,
		OutputWidth:   720,
		OutputHeight:  480,
		ClientID:      "client-id-abc",
	}
	u := BuildTranscodeURL(req)
	for _, substr := range []string{
		"directPlay=0", "directStream=0", "copyts=1",
		"videoResolution=720x480", "X-Plex-Token=xyz",
	} {
		if !strings.Contains(u, substr) {
			t.Errorf("url missing %q: %s", substr, u)
		}
	}
}

func TestSubtitleURLFor_FindsMatchingStream(t *testing.T) {
	xmlBody := `<?xml version="1.0"?>
<MediaContainer>
	<Video ratingKey="42">
		<Media>
			<Part key="/library/parts/99/file.mkv">
				<Stream id="201" streamType="3" key="/library/streams/201" codec="srt"/>
				<Stream id="202" streamType="3" key="/library/streams/202" codec="subrip"/>
			</Part>
		</Media>
	</Video>
</MediaContainer>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/metadata/42" {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(xmlBody))
	}))
	defer ts.Close()

	url, err := SubtitleURLFor(ts.URL, "/library/metadata/42", "202", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(url, "/library/streams/202") {
		t.Errorf("got %q, want containing /library/streams/202", url)
	}
	if !strings.Contains(url, "X-Plex-Token=tok") {
		t.Error("subtitle URL must carry token for FFmpeg")
	}
}

func TestSubtitleURLFor_NoMatch(t *testing.T) {
	xmlBody := `<MediaContainer><Video><Media><Part/></Media></Video></MediaContainer>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(xmlBody))
	}))
	defer ts.Close()
	_, err := SubtitleURLFor(ts.URL, "/library/metadata/42", "999", "tok")
	if err == nil {
		t.Error("expected error for missing stream id")
	}
}

func TestFetchSubtitleToFile_WritesLocalPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-subrip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("1\n00:00:00,000 --> 00:00:02,000\nhello\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path, err := FetchSubtitleToFile(ctx, srv.URL+"/sub.srt", dir, "session-xyz")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "session-xyz.srt" {
		t.Errorf("unexpected filename: %s", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("hello")) {
		t.Errorf("subtitle body not written: %q", body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o600 {
			t.Errorf("file perms = %o, want 0600", info.Mode().Perm())
		}
	}
}
