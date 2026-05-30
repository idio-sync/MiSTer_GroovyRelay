package adapters

import "testing"

func TestFormatSeasonEpisode(t *testing.T) {
	cases := []struct {
		s, e, y int
		want    string
	}{
		{4, 5, 2008, "S04E05 · 2008"},
		{4, 5, 0, "S04E05"},
		{4, 0, 0, "S04"},
		{0, 5, 0, "E05"},
		{0, 0, 2017, "2017"},
		{0, 0, 0, ""},
	}
	for _, c := range cases {
		if got := FormatSeasonEpisode(c.s, c.e, c.y); got != c.want {
			t.Errorf("FormatSeasonEpisode(%d,%d,%d) = %q, want %q", c.s, c.e, c.y, got, c.want)
		}
	}
}

func TestFormatUploadDate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"20240315", "2024-03-15"},
		{"2024031", ""},
		{"abcd1234", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := FormatUploadDate(c.in); got != c.want {
			t.Errorf("FormatUploadDate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
