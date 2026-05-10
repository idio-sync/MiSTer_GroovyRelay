package dlna

import (
	"errors"
	"testing"
	"time"
)

// metadata_test.go covers parseDIDLMetadata + parseDIDLDuration.
// Acceptance criteria from P2.2 §7.

func TestParseDIDLMetadata_Empty(t *testing.T) {
	got, err := parseDIDLMetadata("")
	if err != nil {
		t.Fatalf("parseDIDLMetadata(\"\") err = %v, want nil", err)
	}
	if got != (DIDLMetadata{}) {
		t.Errorf("parseDIDLMetadata(\"\") = %+v, want zero", got)
	}
}

func TestParseDIDLMetadata_WhitespaceOnly(t *testing.T) {
	got, err := parseDIDLMetadata("   \n\t  ")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != (DIDLMetadata{}) {
		t.Errorf("got %+v, want zero", got)
	}
}

func TestParseDIDLMetadata_FullValid(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"
           xmlns:dc="http://purl.org/dc/elements/1.1/"
           xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">
  <item id="0" parentID="-1" restricted="1">
    <dc:title>Big Buck Bunny</dc:title>
    <upnp:class>object.item.videoItem.movie</upnp:class>
    <res protocolInfo="http-get:*:video/mp4:*" duration="00:09:56.000">http://server/bbb.mp4</res>
  </item>
</DIDL-Lite>`
	got, err := parseDIDLMetadata(xml)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got.Title != "Big Buck Bunny" {
		t.Errorf("Title = %q, want %q", got.Title, "Big Buck Bunny")
	}
	if got.Class != "object.item.videoItem.movie" {
		t.Errorf("Class = %q", got.Class)
	}
	if got.ProtocolInfo != "http-get:*:video/mp4:*" {
		t.Errorf("ProtocolInfo = %q", got.ProtocolInfo)
	}
	want := 9*time.Minute + 56*time.Second
	if got.Duration != want {
		t.Errorf("Duration = %v, want %v", got.Duration, want)
	}
}

func TestParseDIDLMetadata_MissingTitle(t *testing.T) {
	xml := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"
                       xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">
  <item>
    <upnp:class>object.item.videoItem</upnp:class>
    <res protocolInfo="http-get:*:video/mp4:*" duration="00:01:00">http://h/v.mp4</res>
  </item>
</DIDL-Lite>`
	got, err := parseDIDLMetadata(xml)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got.Title != "" {
		t.Errorf("Title = %q, want empty", got.Title)
	}
	if got.Class != "object.item.videoItem" {
		t.Errorf("Class = %q", got.Class)
	}
	if got.Duration != time.Minute {
		t.Errorf("Duration = %v, want 1m", got.Duration)
	}
	if got.ProtocolInfo != "http-get:*:video/mp4:*" {
		t.Errorf("ProtocolInfo = %q", got.ProtocolInfo)
	}
}

func TestParseDIDLMetadata_MissingDuration(t *testing.T) {
	xml := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"
                       xmlns:dc="http://purl.org/dc/elements/1.1/">
  <item>
    <dc:title>Live Stream</dc:title>
    <res protocolInfo="http-get:*:video/mpeg:*">http://h/live.ts</res>
  </item>
</DIDL-Lite>`
	got, err := parseDIDLMetadata(xml)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got.Duration != 0 {
		t.Errorf("Duration = %v, want 0 (unknown)", got.Duration)
	}
	if got.Title != "Live Stream" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.ProtocolInfo != "http-get:*:video/mpeg:*" {
		t.Errorf("ProtocolInfo = %q", got.ProtocolInfo)
	}
}

func TestParseDIDLMetadata_DurationFractional(t *testing.T) {
	xml := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/">
  <item>
    <res duration="00:00:30.500">http://h/v.mp4</res>
  </item>
</DIDL-Lite>`
	got, err := parseDIDLMetadata(xml)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := 30*time.Second + 500*time.Millisecond
	if got.Duration != want {
		t.Errorf("Duration = %v, want %v", got.Duration, want)
	}
}

func TestParseDIDLMetadata_MalformedXML(t *testing.T) {
	got, err := parseDIDLMetadata("<broken")
	if err == nil {
		t.Fatalf("err = nil, want error")
	}
	if !errors.Is(err, ErrInvalidMetadata) {
		t.Errorf("err = %v, want errors.Is ErrInvalidMetadata", err)
	}
	if got != (DIDLMetadata{}) {
		t.Errorf("got %+v, want zero", got)
	}
}

func TestParseDIDLMetadata_MultipleResElements(t *testing.T) {
	// Two <res> elements: the picking rule documented in metadata.go
	// is "first <res> with a duration or protocolInfo attribute wins."
	// Both have those here, so we expect the FIRST one.
	xml := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"
                       xmlns:dc="http://purl.org/dc/elements/1.1/">
  <item>
    <dc:title>Multi-Res</dc:title>
    <res protocolInfo="http-get:*:video/mp4:*" duration="00:05:00">http://h/720p.mp4</res>
    <res protocolInfo="http-get:*:video/x-matroska:*" duration="00:05:01">http://h/1080p.mkv</res>
  </item>
</DIDL-Lite>`
	got, err := parseDIDLMetadata(xml)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.ProtocolInfo != "http-get:*:video/mp4:*" {
		t.Errorf("ProtocolInfo = %q, want first <res>", got.ProtocolInfo)
	}
	if got.Duration != 5*time.Minute {
		t.Errorf("Duration = %v, want first <res>'s 5m", got.Duration)
	}
}

func TestParseDIDLMetadata_NoItem(t *testing.T) {
	// Well-formed DIDL with no <item> — return zero, no error.
	xml := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"></DIDL-Lite>`
	got, err := parseDIDLMetadata(xml)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != (DIDLMetadata{}) {
		t.Errorf("got %+v, want zero", got)
	}
}

// ---- parseDIDLDuration ----

func TestParseDIDLDuration(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"empty", "", 0},
		{"whitespace", "   ", 0},
		{"hh:mm:ss", "01:02:03", time.Hour + 2*time.Minute + 3*time.Second},
		{"zero", "00:00:00", 0},
		{"with fractional", "00:00:30.500", 30*time.Second + 500*time.Millisecond},
		{"single-digit ms", "00:00:30.5", 30*time.Second + 500*time.Millisecond},
		{"3-digit ms", "00:00:30.123", 30*time.Second + 123*time.Millisecond},
		{"6-digit truncated", "00:00:30.123456", 30*time.Second + 123*time.Millisecond},
		{"large hours", "10:00:00", 10 * time.Hour},
		{"negative rejected", "-00:00:30", 0},
		{"out-of-range minute", "00:60:00", 0},
		{"out-of-range second", "00:00:60", 0},
		{"non-numeric", "ab:cd:ef", 0},
		{"two parts", "00:30", 0},
		{"wrong separator", "00.00.30", 0},
		{"go duration string", "1h30m", 0}, // strict — reject time.ParseDuration form
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDIDLDuration(tt.in)
			if got != tt.want {
				t.Errorf("parseDIDLDuration(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
