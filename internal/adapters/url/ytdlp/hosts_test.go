package ytdlp

import "testing"

func TestMatch_ExactHost(t *testing.T) {
	if !Match("youtube.com", []string{"youtube.com"}) {
		t.Error("exact match should hit")
	}
}

func TestMatch_SubdomainSuffix(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"www.youtube.com", true},
		{"m.youtube.com", true},
		{"music.youtube.com", true},
		{"foo.bar.youtube.com", true},
	}
	allow := []string{"youtube.com"}
	for _, c := range cases {
		if got := Match(c.host, allow); got != c.want {
			t.Errorf("Match(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestMatch_RejectsSubstringOnly(t *testing.T) {
	cases := []string{
		"fakeyoutube.com",
		"notyoutube.com",
		"youtube.com.evil.example",
		"myyoutube.com",
	}
	allow := []string{"youtube.com"}
	for _, h := range cases {
		if Match(h, allow) {
			t.Errorf("Match(%q) wrongly matched youtube.com", h)
		}
	}
}

func TestMatch_RejectsTLDOnly(t *testing.T) {
	if Match("foo.com", []string{"com"}) {
		t.Error("TLD-only entry should not match")
	}
}

func TestMatch_EmptyAllowlist(t *testing.T) {
	if Match("youtube.com", nil) {
		t.Error("empty allowlist should never match")
	}
	if Match("youtube.com", []string{}) {
		t.Error("empty allowlist should never match")
	}
}

func TestMatch_CaseInsensitive(t *testing.T) {
	if !Match("WWW.YouTube.COM", []string{"youtube.com"}) {
		t.Error("uppercase host should match lowercase allowlist entry")
	}
	if !Match("youtube.com", []string{"YouTube.COM"}) {
		t.Error("uppercase allowlist entry should match lowercase host")
	}
}

func TestMatch_MultipleEntries(t *testing.T) {
	allow := []string{"twitch.tv", "youtube.com", "vimeo.com"}
	if !Match("clips.twitch.tv", allow) {
		t.Error("clips.twitch.tv should match twitch.tv in middle of list")
	}
}

func TestMatch_EmptyHost(t *testing.T) {
	if Match("", []string{"youtube.com"}) {
		t.Error("empty host should never match")
	}
}
