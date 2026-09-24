package adapters

import (
	"strings"
	"testing"
)

func TestRedactErrorText_StripsCredentialsFromEmbeddedURLs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    string
		secrets []string
	}{
		{
			name:    "go url.Error with api_key query",
			in:      `jellyfin: probe: Get "http://192.168.50.25:8096/System/Info?api_key=b8494f77deadbeef": dial tcp 192.168.50.25:8096: connect: connection refused`,
			want:    `jellyfin: probe: Get "http://192.168.50.25:8096/System/Info": dial tcp 192.168.50.25:8096: connect: connection refused`,
			secrets: []string{"b8494f77deadbeef", "api_key"},
		},
		{
			name:    "websocket url",
			in:      `websocket: dial wss://jf.example/socket?api_key=tok123&deviceId=abc: bad handshake`,
			want:    `websocket: dial wss://jf.example/socket: bad handshake`,
			secrets: []string{"tok123"},
		},
		{
			name:    "userinfo",
			in:      `Get "https://user:hunter2@example.com/x": EOF`,
			want:    `Get "https://example.com/x": EOF`,
			secrets: []string{"hunter2"},
		},
		{
			name:    "bare token pair outside a url",
			in:      `request failed with X-Plex-Token=abcdef and access_token=zzz`,
			want:    `request failed with X-Plex-Token=REDACTED and access_token=REDACTED`,
			secrets: []string{"abcdef", "zzz"},
		},
		{
			name: "plain message untouched",
			in:   "DLNA requires a reachable bridge.host_ip",
			want: "DLNA requires a reachable bridge.host_ip",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RedactErrorText(tc.in)
			if got != tc.want {
				t.Errorf("RedactErrorText(%q)\n got  %q\n want %q", tc.in, got, tc.want)
			}
			for _, s := range tc.secrets {
				if strings.Contains(got, s) && !strings.Contains(tc.want, s) {
					t.Errorf("secret %q survived redaction: %q", s, got)
				}
			}
		})
	}
}
