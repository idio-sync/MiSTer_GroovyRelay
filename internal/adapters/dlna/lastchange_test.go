package dlna

import (
	"strings"
	"testing"
)

func TestBuildAVTransportLastChange_EscapesEventedValues(t *testing.T) {
	got := buildAVTransportLastChange(avTransportEventState{
		TransportState:          transportStatePlaying,
		TransportStatus:         "OK",
		CurrentTrackURI:         `http://media.local/watch?title=Tom&Jerry`,
		CurrentTrackDuration:    "01:02:03",
		RelativeTimePosition:    "00:00:07",
		CurrentTransportActions: "Pause,Stop,Seek",
	})

	for _, want := range []string{
		`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/AVT/">`,
		`<InstanceID val="0">`,
		`<TransportState val="PLAYING"></TransportState>`,
		`<CurrentTrackURI val="http://media.local/watch?title=Tom&amp;Jerry"></CurrentTrackURI>`,
		`<CurrentTransportActions val="Pause,Stop,Seek"></CurrentTransportActions>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("LastChange missing %q:\n%s", want, got)
		}
	}
}

func TestBuildRenderingControlLastChange_UsesMasterChannel(t *testing.T) {
	got := buildRenderingControlLastChange(renderingControlEventState{
		Volume: 37,
		Muted:  true,
	})

	for _, want := range []string{
		`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/">`,
		`<InstanceID val="0">`,
		`<Volume channel="Master" val="37"></Volume>`,
		`<Mute channel="Master" val="1"></Mute>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("LastChange missing %q:\n%s", want, got)
		}
	}
}

func TestBuildGENAPropertySet_EscapesLastChangeAsText(t *testing.T) {
	body := buildGENAPropertySet(eventProperties{
		"LastChange": `<Event><InstanceID val="0"></InstanceID></Event>`,
	})

	got := string(body)
	if strings.Contains(got, "<LastChange><Event>") {
		t.Fatalf("LastChange nested as raw XML; want escaped text:\n%s", got)
	}
	if !strings.Contains(got, `<LastChange>&lt;Event&gt;`) {
		t.Fatalf("LastChange not XML-escaped in propertyset:\n%s", got)
	}
	if !strings.Contains(got, `<e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0">`) {
		t.Fatalf("propertyset missing GENA namespace:\n%s", got)
	}
}

func TestBuildGENAPropertySet_ConnectionManagerVariables(t *testing.T) {
	body := buildGENAPropertySet(eventProperties{
		"SourceProtocolInfo":   "",
		"SinkProtocolInfo":     "http-get:*:video/mp4:*",
		"CurrentConnectionIDs": "0",
	})
	got := string(body)

	for _, want := range []string{
		`<SourceProtocolInfo></SourceProtocolInfo>`,
		`<SinkProtocolInfo>http-get:*:video/mp4:*</SinkProtocolInfo>`,
		`<CurrentConnectionIDs>0</CurrentConnectionIDs>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("propertyset missing %q:\n%s", want, got)
		}
	}
}
