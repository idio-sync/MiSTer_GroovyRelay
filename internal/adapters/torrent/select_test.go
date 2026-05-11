package torrent

import "testing"

func TestPickLargestPlayableVideo(t *testing.T) {
	files := []FileCandidate{
		{DisplayPath: "disc/readme.txt", Length: 100},
		{DisplayPath: "extras/trailer.mp4", Length: 1000},
		{DisplayPath: "movie/Movie.mkv", Length: 9000},
	}
	got, err := pickLargestPlayable(files)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayPath != "movie/Movie.mkv" {
		t.Fatalf("selected %q, want movie/Movie.mkv", got.DisplayPath)
	}
}

func TestPickLargestPlayableTieBreaksByDisplayPath(t *testing.T) {
	files := []FileCandidate{
		{DisplayPath: "b/movie.mkv", Length: 100},
		{DisplayPath: "a/movie.mp4", Length: 100},
	}
	got, err := pickLargestPlayable(files)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayPath != "a/movie.mp4" {
		t.Fatalf("selected %q, want lexical first", got.DisplayPath)
	}
}

func TestPickLargestPlayableTieBreaksDuplicatePathByIndex(t *testing.T) {
	files := []FileCandidate{
		{DisplayPath: "movie.mkv", Length: 100, Index: 2},
		{DisplayPath: "movie.mkv", Length: 100, Index: 1},
	}
	got, err := pickLargestPlayable(files)
	if err != nil {
		t.Fatal(err)
	}
	if got.Index != 1 {
		t.Fatalf("selected index %d, want 1", got.Index)
	}
}

func TestPickLargestPlayableReturnsTypedError(t *testing.T) {
	_, err := pickLargestPlayable([]FileCandidate{{DisplayPath: "readme.txt", Length: 1}})
	if terr, ok := err.(*TorrentError); !ok || terr.Kind != ErrNoPlayableFile {
		t.Fatalf("error = %#v, want ErrNoPlayableFile", err)
	}
}

func TestSanitizeTitle(t *testing.T) {
	cases := map[string]string{
		"folder/Movie.Name.1999.mkv": "Movie.Name.1999.mkv",
		"folder\\Movie\x00Name.mp4":  "MovieName.mp4",
		".":                          "Torrent video",
		"..":                         "Torrent video",
		"   ":                        "Torrent video",
	}
	for in, want := range cases {
		if got := sanitizeTitle(in); got != want {
			t.Fatalf("sanitizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
