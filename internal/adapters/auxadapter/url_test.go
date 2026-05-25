package aux

import "testing"

func TestValidateStreamURLRejectsUnsafeShapes(t *testing.T) {
	for _, raw := range []string{
		"",
		"file:///tmp/a.wav",
		"udp://239.0.0.1:5004",
		"http://user:pass@example.test/a.wav",
		"http:///missing-host",
		"http://example.test/a.wav#frag",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := validateStreamURL(raw); err == nil {
				t.Fatalf("validateStreamURL(%q) succeeded, want error", raw)
			}
		})
	}
}

func TestValidateStreamURLAcceptsHTTPAndHTTPS(t *testing.T) {
	for _, raw := range []string{"http://capture-host:8090/aux.wav", "https://capture.example/aux.wav"} {
		if _, err := validateStreamURL(raw); err != nil {
			t.Fatalf("validateStreamURL(%q): %v", raw, err)
		}
	}
}
