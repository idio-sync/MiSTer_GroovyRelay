package companion

import "testing"

func TestIsExtensionOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin string
		want   bool
	}{
		{"moz-extension://abc", true},
		{"chrome-extension://abc", true},
		{"safari-web-extension://abc", true},
		{"", false},
		{"null", false},
		{"http://bridge.lan:32500", false},
		{"https://moz-extension.example", false},
	} {
		if got := isExtensionOrigin(tc.origin); got != tc.want {
			t.Errorf("isExtensionOrigin(%q) = %v, want %v", tc.origin, got, tc.want)
		}
	}
}
