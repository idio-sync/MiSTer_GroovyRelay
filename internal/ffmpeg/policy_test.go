package ffmpeg

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMediaInputPolicy_ZeroValueIsZero(t *testing.T) {
	var p MediaInputPolicy
	if !p.IsZero() {
		t.Errorf("zero value should report IsZero() = true")
	}
	// Apply on the zero value must not change argv.
	got := p.Apply([]string{"-v", "error"})
	want := []string{"-v", "error"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Apply on zero policy returned %v, want %v", got, want)
	}
}

func TestMediaInputPolicy_IsZeroFalseForEachField(t *testing.T) {
	cases := []struct {
		name string
		p    MediaInputPolicy
	}{
		{"protocol whitelist", MediaInputPolicy{ProtocolWhitelist: []string{"file"}}},
		{"disable redirects", MediaInputPolicy{DisableRedirects: true}},
		{"disable reconnect", MediaInputPolicy{DisableReconnect: true}},
		{"rw timeout", MediaInputPolicy{RWTimeout: time.Second}},
		{"blocked headers", MediaInputPolicy{BlockedHeaders: []string{"Cookie"}}},
		{"disable playlists", MediaInputPolicy{DisablePlaylists: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.p.IsZero() {
				t.Errorf("IsZero() should be false for %+v", tc.p)
			}
		})
	}
}

func TestMediaInputPolicy_Apply_ProtocolWhitelist(t *testing.T) {
	p := MediaInputPolicy{
		ProtocolWhitelist: []string{"file", "http", "https", "tcp", "tls", "crypto"},
	}
	got := p.Apply(nil)
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-protocol_whitelist file,http,https,tcp,tls,crypto") {
		t.Errorf("expected comma-joined whitelist, got %q", joined)
	}
}

func TestMediaInputPolicy_Apply_DisableReconnectEmits(t *testing.T) {
	// DisableReconnect emits FOUR reconnect flags as defense-in-depth.
	// All four default to 0/off in current FFmpeg, but a future build
	// could flip a default; explicit zeros prevent silent regression.
	// See policy.go DisableReconnect doc.
	p := MediaInputPolicy{DisableReconnect: true}
	got := strings.Join(p.Apply(nil), " ")
	for _, want := range []string{
		"-reconnect 0",
		"-reconnect_at_eof 0",
		"-reconnect_streamed 0",
		"-reconnect_on_network_error 0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in argv: %q", want, got)
		}
	}
}

func TestMediaInputPolicy_Apply_RWTimeoutEmitsMicroseconds(t *testing.T) {
	p := MediaInputPolicy{RWTimeout: 5 * time.Second}
	got := strings.Join(p.Apply(nil), " ")
	// 5 seconds == 5_000_000 microseconds.
	if !strings.Contains(got, "-rw_timeout 5000000") {
		t.Errorf("expected -rw_timeout 5000000, got %q", got)
	}
}

func TestMediaInputPolicy_Apply_DisableRedirectsIsNoOp(t *testing.T) {
	// Per the field's documented contract: DisableRedirects intentionally
	// emits no flag. The redirect-safety guarantee is achieved by
	// adapter-side URL pre-validation + ProtocolWhitelist; see policy.go.
	p := MediaInputPolicy{DisableRedirects: true}
	got := p.Apply(nil)
	if len(got) != 0 {
		t.Errorf("DisableRedirects must not change argv, got %v", got)
	}
}

func TestMediaInputPolicy_Apply_DisablePlaylistsIsNoOp(t *testing.T) {
	// Adapter-enforced marker today: DLNA Phase 5 validates/caches HLS
	// before FFmpeg, and DisablePlaylists still emits no argv.
	p := MediaInputPolicy{DisablePlaylists: true}
	got := p.Apply(nil)
	if len(got) != 0 {
		t.Errorf("DisablePlaylists must not change argv today, got %v", got)
	}
}

func TestMediaInputPolicy_Apply_FullPolicyRoundTrip(t *testing.T) {
	p := MediaInputPolicy{
		ProtocolWhitelist: []string{"file", "http", "https"},
		DisableReconnect:  true,
		RWTimeout:         3 * time.Second,
	}
	args := p.Apply([]string{"-v", "error"})
	want := []string{
		"-v", "error",
		"-protocol_whitelist", "file,http,https",
		"-reconnect", "0",
		"-reconnect_at_eof", "0",
		"-reconnect_streamed", "0",
		"-reconnect_on_network_error", "0",
		"-rw_timeout", "3000000",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("argv mismatch:\ngot  %v\nwant %v", args, want)
	}
}

func TestMediaInputPolicy_FilterHeaders_CaseInsensitiveDeny(t *testing.T) {
	p := MediaInputPolicy{BlockedHeaders: []string{"Referer", "Cookie"}}
	in := map[string]string{
		"referer":    "http://internal/",
		"Cookie":     "session=abc",
		"User-Agent": "groovyrelay",
	}
	out := p.FilterHeaders(in)
	if _, ok := out["referer"]; ok {
		t.Errorf("lowercase 'referer' should have been dropped: %v", out)
	}
	if _, ok := out["Cookie"]; ok {
		t.Errorf("'Cookie' should have been dropped: %v", out)
	}
	if got := out["User-Agent"]; got != "groovyrelay" {
		t.Errorf("User-Agent should survive, got %q (full out=%v)", got, out)
	}
	if len(out) != 1 {
		t.Errorf("expected exactly 1 surviving header, got %d (%v)", len(out), out)
	}
}

func TestMediaInputPolicy_FilterHeaders_EmptyDenyListReturnsInput(t *testing.T) {
	p := MediaInputPolicy{}
	in := map[string]string{"X-Plex-Token": "abc"}
	out := p.FilterHeaders(in)
	if !reflect.DeepEqual(out, in) {
		t.Errorf("empty deny-list should pass through unchanged; got %v want %v", out, in)
	}
}

func TestMediaInputPolicy_FilterHeaders_NilInput(t *testing.T) {
	p := MediaInputPolicy{BlockedHeaders: []string{"Cookie"}}
	if got := p.FilterHeaders(nil); got != nil {
		t.Errorf("nil input should stay nil (preserve no-headers path); got %v", got)
	}
}

// TestMediaInputPolicy_FilterHeaders_DoesNotMutateInput verifies the filter
// returns a fresh map so adapter-owned header maps are not corrupted across
// sessions or used for unrelated probes.
func TestMediaInputPolicy_FilterHeaders_DoesNotMutateInput(t *testing.T) {
	p := MediaInputPolicy{BlockedHeaders: []string{"Cookie"}}
	in := map[string]string{
		"Cookie":     "session=abc",
		"User-Agent": "ua",
	}
	_ = p.FilterHeaders(in)
	if _, ok := in["Cookie"]; !ok {
		t.Errorf("FilterHeaders mutated input map; Cookie should still exist in source: %v", in)
	}
	if len(in) != 2 {
		t.Errorf("FilterHeaders changed input size: %v", in)
	}
}
