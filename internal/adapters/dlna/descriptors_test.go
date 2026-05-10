package dlna

import (
	"encoding/xml"
	"strings"
	"testing"
)

// ---- device descriptor ----

func TestDeviceXML_RequiredElements(t *testing.T) {
	body, err := deviceXML("abcdef01-2345-6789-abcd-ef0123456789", "MiSTer-CRT")
	if err != nil {
		t.Fatalf("deviceXML: %v", err)
	}
	bodyStr := string(body)

	required := []string{
		"<deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>",
		"<UDN>uuid:abcdef01-2345-6789-abcd-ef0123456789</UDN>",
		"<friendlyName>MiSTer-CRT</friendlyName>",
		"<manufacturer>MiSTer_GroovyRelay</manufacturer>",
		// Three services, each with the SCPDURL/controlURL/eventSubURL
		// paths that match the routes table.
		"<serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>",
		"<SCPDURL>/dlna/AVTransport.xml</SCPDURL>",
		"<controlURL>/dlna/control/AVTransport</controlURL>",
		"<eventSubURL>/dlna/event/AVTransport</eventSubURL>",
		"<serviceType>urn:schemas-upnp-org:service:ConnectionManager:1</serviceType>",
		"<SCPDURL>/dlna/ConnectionManager.xml</SCPDURL>",
		"<controlURL>/dlna/control/ConnectionManager</controlURL>",
		"<eventSubURL>/dlna/event/ConnectionManager</eventSubURL>",
		"<serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>",
		"<SCPDURL>/dlna/RenderingControl.xml</SCPDURL>",
		"<controlURL>/dlna/control/RenderingControl</controlURL>",
		"<eventSubURL>/dlna/event/RenderingControl</eventSubURL>",
	}
	for _, want := range required {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("deviceXML missing %q\nbody:\n%s", want, bodyStr)
		}
	}
}

func TestDeviceXML_FriendlyNameXMLEscaped(t *testing.T) {
	// XML-special characters inside friendlyName must be escaped before
	// they reach a controller — otherwise the descriptor is malformed.
	body, err := deviceXML("uuid", "Liv & Cabinet <main>")
	if err != nil {
		t.Fatalf("deviceXML: %v", err)
	}
	bodyStr := string(body)
	if strings.Contains(bodyStr, "<friendlyName>Liv & Cabinet <main></friendlyName>") {
		t.Errorf("friendlyName not escaped; body=\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "Liv &amp; Cabinet &lt;main&gt;") {
		t.Errorf("friendlyName escape sequence missing; body=\n%s", bodyStr)
	}
}

func TestDeviceXML_ParsesAsXML(t *testing.T) {
	body, err := deviceXML("uuid", "name")
	if err != nil {
		t.Fatalf("deviceXML: %v", err)
	}
	var roundTrip deviceDescriptor
	if err := xml.Unmarshal(body, &roundTrip); err != nil {
		t.Fatalf("deviceXML output is not parseable XML: %v\nbody=\n%s", err, body)
	}
	if got := len(roundTrip.Device.ServiceList.Services); got != 3 {
		t.Errorf("services = %d, want 3", got)
	}
}

// ---- SCPD invariants ----

// TestSCPDsParseAsXML confirms each SCPD is well-formed XML and
// non-empty. `xmlns="..."` on <scpd> is required by UPnP, and the
// underlying XML decoder rejects mismatched tags or invalid character
// data. The non-empty check guards against a future const accidentally
// being nil'd out; structural invariants live in TestSCPDActionLists
// and TestSCPDEventedStateVariables.
func TestSCPDsParseAsXML(t *testing.T) {
	for name, body := range map[string]string{
		"avTransport":       avTransportSCPD,
		"connectionManager": connectionManagerSCPD,
		"renderingControl":  renderingControlSCPD,
	} {
		if len(body) == 0 {
			t.Fatalf("%s SCPD is empty", name)
		}
		var dummy struct {
			XMLName xml.Name
		}
		if err := xml.Unmarshal([]byte(body), &dummy); err != nil {
			t.Errorf("%s SCPD is not parseable XML: %v", name, err)
		}
	}
}

// TestSCPDActionLists pins the action names declared in each SCPD.
// The set must match Phase 1 disposition exactly — no missing actions
// (controllers refuse to bind) and no extras (would advertise capability
// the handler doesn't implement).
func TestSCPDActionLists(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantActions []string
	}{
		{
			name: "AVTransport",
			body: avTransportSCPD,
			wantActions: []string{
				"SetAVTransportURI",
				"SetNextAVTransportURI",
				"GetMediaInfo",
				"GetTransportInfo",
				"GetPositionInfo",
				"GetDeviceCapabilities",
				"GetTransportSettings",
				"Stop",
				"Play",
				"Pause",
				"Seek",
				"Next",
				"Previous",
				"SetPlayMode",
				"GetCurrentTransportActions",
			},
		},
		{
			name: "ConnectionManager",
			body: connectionManagerSCPD,
			wantActions: []string{
				"GetProtocolInfo",
				"PrepareForConnection",
				"ConnectionComplete",
				"GetCurrentConnectionIDs",
				"GetCurrentConnectionInfo",
			},
		},
		{
			name: "RenderingControl",
			body: renderingControlSCPD,
			wantActions: []string{
				"ListPresets",
				"SelectPreset",
				"GetMute",
				"SetMute",
				"GetVolume",
				"SetVolume",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			declared := scpdActionNames(t, tt.body)
			if len(declared) != len(tt.wantActions) {
				t.Errorf("%s declared %d actions (%v), want %d (%v)",
					tt.name, len(declared), declared, len(tt.wantActions), tt.wantActions)
			}
			wantSet := make(map[string]bool, len(tt.wantActions))
			for _, a := range tt.wantActions {
				wantSet[a] = true
			}
			gotSet := make(map[string]bool, len(declared))
			for _, a := range declared {
				gotSet[a] = true
				if !wantSet[a] {
					t.Errorf("%s declares unexpected action %q", tt.name, a)
				}
			}
			for _, a := range tt.wantActions {
				if !gotSet[a] {
					t.Errorf("%s missing required action %q", tt.name, a)
				}
			}
		})
	}
}

// scpdActionNames extracts the <name> values out of <action> blocks
// inside a SCPD body. Uses the standard library XML decoder so the
// extraction is robust to whitespace differences. The XML structure is
// shallow enough that a thin Decoder loop is clearer than fully
// modelling the SCPD schema.
func scpdActionNames(t *testing.T, body string) []string {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(body))

	var (
		names      []string
		inAction   bool
		captureName bool
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch tt := tok.(type) {
		case xml.StartElement:
			if tt.Name.Local == "action" {
				inAction = true
				continue
			}
			if inAction && tt.Name.Local == "name" {
				captureName = true
				continue
			}
			if inAction && tt.Name.Local == "argumentList" {
				// Stop capturing — argument <name> elements are not action names.
				inAction = false
			}
		case xml.EndElement:
			if tt.Name.Local == "action" {
				inAction = false
			}
		case xml.CharData:
			if captureName {
				names = append(names, strings.TrimSpace(string(tt)))
				captureName = false
			}
		}
	}
	return names
}

// TestSCPDEventedStateVariables pins the sendEvents="yes" surface in
// each SCPD. Spec §Service Action Surface lines 220-253:
//   AVT: LastChange
//   RC:  LastChange
//   CM:  SourceProtocolInfo, SinkProtocolInfo, CurrentConnectionIDs
func TestSCPDEventedStateVariables(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantVars  []string
	}{
		{
			name:     "AVTransport",
			body:     avTransportSCPD,
			wantVars: []string{"LastChange"},
		},
		{
			name:     "RenderingControl",
			body:     renderingControlSCPD,
			wantVars: []string{"LastChange"},
		},
		{
			name:     "ConnectionManager",
			body:     connectionManagerSCPD,
			wantVars: []string{"SourceProtocolInfo", "SinkProtocolInfo", "CurrentConnectionIDs"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evented := evendedStateVars(t, tt.body)
			eventedSet := make(map[string]bool, len(evented))
			for _, v := range evented {
				eventedSet[v] = true
			}
			for _, want := range tt.wantVars {
				if !eventedSet[want] {
					t.Errorf("%s missing evented variable %q (have %v)", tt.name, want, evented)
				}
			}
		})
	}
}

// evendedStateVars walks the serviceStateTable and returns the names
// of any <stateVariable sendEvents="yes">. Uses the same shallow
// decoder approach as scpdActionNames.
func evendedStateVars(t *testing.T, body string) []string {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(body))

	var (
		names           []string
		inEventedVar    bool
		captureVarName  bool
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch tt := tok.(type) {
		case xml.StartElement:
			if tt.Name.Local == "stateVariable" {
				for _, attr := range tt.Attr {
					if attr.Name.Local == "sendEvents" && attr.Value == "yes" {
						inEventedVar = true
						break
					}
				}
				continue
			}
			if inEventedVar && tt.Name.Local == "name" {
				captureVarName = true
			}
		case xml.EndElement:
			if tt.Name.Local == "stateVariable" {
				inEventedVar = false
			}
		case xml.CharData:
			if captureVarName {
				names = append(names, strings.TrimSpace(string(tt)))
				captureVarName = false
			}
		}
	}
	return names
}
