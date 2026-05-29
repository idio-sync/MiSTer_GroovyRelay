package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVideoMetadataFor_Episode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<MediaContainer><Video type="episode" title="The Constant" grandparentTitle="Lost" index="5" parentIndex="4" year="2008"/></MediaContainer>`))
	}))
	defer srv.Close()
	md, ok, err := VideoMetadataFor(context.Background(), srv.URL, "/library/metadata/1", "tok")
	if err != nil || !ok {
		t.Fatalf("VideoMetadataFor ok=%v err=%v", ok, err)
	}
	d := plexVideoDisplay(md, "fallback")
	if d.Primary != "Lost" || d.Secondary != "The Constant" || d.Tertiary != "S04E05 · 2008" {
		t.Fatalf("episode display = %+v", d)
	}
}

func TestPlexVideoDisplay_Movie(t *testing.T) {
	d := plexVideoDisplay(VideoMetadata{Type: "movie", Title: "Blade Runner 2049", Year: 2017}, "fallback")
	if d.Primary != "Blade Runner 2049" || d.Secondary != "2017" || d.Tertiary != "" {
		t.Fatalf("movie display = %+v", d)
	}
}

func TestPlexVideoDisplay_FallbackWhenEmpty(t *testing.T) {
	d := plexVideoDisplay(VideoMetadata{}, "Controller Title")
	if d.Primary != "Controller Title" {
		t.Fatalf("fallback display = %+v", d)
	}
}
