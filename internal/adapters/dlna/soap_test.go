package dlna

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- extractSOAPAction ----

func TestExtractSOAPAction(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
		wantOK bool
	}{
		{
			name:   "canonical quoted form",
			header: `"urn:schemas-upnp-org:service:AVTransport:1#Play"`,
			want:   "Play",
			wantOK: true,
		},
		{
			name:   "unquoted form",
			header: `urn:schemas-upnp-org:service:AVTransport:1#GetTransportInfo`,
			want:   "GetTransportInfo",
			wantOK: true,
		},
		{
			name:   "with surrounding whitespace",
			header: `  "urn:schemas-upnp-org:service:RenderingControl:1#GetVolume"  `,
			want:   "GetVolume",
			wantOK: true,
		},
		{
			name:   "minimal action only",
			header: "GetMute",
			want:   "GetMute",
			wantOK: true,
		},
		{
			name:   "empty",
			header: "",
			want:   "",
			wantOK: false,
		},
		{
			name:   "only quotes",
			header: `""`,
			want:   "",
			wantOK: false,
		},
		{
			name:   "trailing hash with no action",
			header: `"urn:foo#"`,
			want:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractSOAPAction(tt.header)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("extractSOAPAction(%q) = (%q, %v), want (%q, %v)",
					tt.header, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// ---- parseSOAPRequest ----

const validEnvelope = `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body>
    <u:SetVolume xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">
      <InstanceID>0</InstanceID>
      <Channel>Master</Channel>
      <DesiredVolume>42</DesiredVolume>
    </u:SetVolume>
  </s:Body>
</s:Envelope>`

func TestParseSOAPRequest_ValidRoundTrip(t *testing.T) {
	req := httptest.NewRequest("POST", "/dlna/control/RenderingControl",
		strings.NewReader(validEnvelope))
	rr := httptest.NewRecorder()
	action, args, err := parseSOAPRequest(rr, req)
	if err != nil {
		t.Fatalf("parseSOAPRequest: %v", err)
	}
	if action != "SetVolume" {
		t.Errorf("action = %q, want SetVolume", action)
	}
	wantArgs := map[string]string{
		"InstanceID":    "0",
		"Channel":       "Master",
		"DesiredVolume": "42",
	}
	for k, v := range wantArgs {
		if got := args[k]; got != v {
			t.Errorf("args[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestParseSOAPRequest_OversizedBodyRejected(t *testing.T) {
	// Build a body > 64 KiB. Padding must be inside well-formed XML so
	// the size check fires before the parser. Use whitespace inside an
	// <Other> element; the parser would otherwise short-circuit.
	body := "<?xml version=\"1.0\"?>" +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">` +
		`<s:Body><u:Big xmlns:u="urn:test"><Pad>` +
		strings.Repeat("a", soapMaxBodyBytes+1024) +
		`</Pad></u:Big></s:Body></s:Envelope>`
	req := httptest.NewRequest("POST", "/dlna/control/RenderingControl",
		strings.NewReader(body))
	rr := httptest.NewRecorder()
	_, _, err := parseSOAPRequest(rr, req)
	if !errors.Is(err, errSOAPBodyTooLarge) {
		t.Errorf("err = %v, want errSOAPBodyTooLarge", err)
	}
}

func TestParseSOAPRequest_EmptyBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/dlna/control/RenderingControl",
		strings.NewReader(""))
	rr := httptest.NewRecorder()
	_, _, err := parseSOAPRequest(rr, req)
	if !errors.Is(err, errSOAPMissingBody) {
		t.Errorf("err = %v, want errSOAPMissingBody", err)
	}
}

func TestParseSOAPRequest_MalformedXML(t *testing.T) {
	req := httptest.NewRequest("POST", "/dlna/control/RenderingControl",
		strings.NewReader("not xml"))
	rr := httptest.NewRecorder()
	_, _, err := parseSOAPRequest(rr, req)
	if err == nil {
		t.Error("err = nil, want non-nil for malformed XML")
	}
	// Specifically not the body-too-large or missing-body sentinel.
	if errors.Is(err, errSOAPBodyTooLarge) || errors.Is(err, errSOAPMissingBody) {
		t.Errorf("err = %v, want a generic decode error", err)
	}
}

func TestParseSOAPRequest_EmptyAction(t *testing.T) {
	// Envelope with a Body but no inner action element.
	body := `<?xml version="1.0"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">` +
		`<s:Body></s:Body></s:Envelope>`
	req := httptest.NewRequest("POST", "/dlna/control/RenderingControl",
		strings.NewReader(body))
	rr := httptest.NewRecorder()
	_, _, err := parseSOAPRequest(rr, req)
	if !errors.Is(err, errSOAPMissingAction) {
		t.Errorf("err = %v, want errSOAPMissingAction", err)
	}
}

// ---- writeSOAPFault ----

func TestWriteSOAPFault_Shape(t *testing.T) {
	rr := httptest.NewRecorder()
	writeSOAPFault(rr, upnpErrInvalidAction)

	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if ct != `text/xml; charset="utf-8"` {
		t.Errorf("Content-Type = %q, want %q", ct, `text/xml; charset="utf-8"`)
	}
	body := rr.Body.String()
	wantSubstrings := []string{
		`<s:Envelope`,
		`<s:Body>`,
		`<s:Fault>`,
		`<faultcode>s:Client</faultcode>`,
		`<faultstring>UPnPError</faultstring>`,
		`<UPnPError xmlns="urn:schemas-upnp-org:control-1-0">`,
		`<errorCode>401</errorCode>`,
		`<errorDescription>Invalid Action</errorDescription>`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody:\n%s", want, body)
		}
	}
}

// TestWriteSOAPFault_DescriptionsAreSpecCorrect pins each error code
// used by Phase 1 to the description string the spec mandates.
// Spec §Common Action Rules table.
func TestWriteSOAPFault_DescriptionsAreSpecCorrect(t *testing.T) {
	tests := []struct {
		code     upnpErrorCode
		wantDesc string
	}{
		{upnpErrInvalidAction, "Invalid Action"},
		{upnpErrInvalidArgs, "Invalid Args"},
		{upnpErrActionFailed, "Action Failed"},
		{upnpErrTransitionNotAvail, "Transition not available"},
		{upnpErrSeekModeNotSupp, "Seek mode not supported"},
		{upnpErrIllegalSeekTarget, "Illegal seek target"},
		{upnpErrPlayModeNotSupp, "Play mode not supported"},
		{upnpErrIllegalMIME, "Illegal MIME-type"},
		{upnpErrResourceNotFound, "Resource not found"},
		{upnpErrPlaySpeedNotSupp, "Play speed not supported"},
		{upnpErrInvalidInstanceID, "Invalid InstanceID"},
	}
	for _, tt := range tests {
		got := upnpErrDescription(tt.code)
		if got != tt.wantDesc {
			t.Errorf("upnpErrDescription(%d) = %q, want %q", tt.code, got, tt.wantDesc)
		}
	}
}

func TestWriteSOAPFault_UnknownCodeFallback(t *testing.T) {
	rr := httptest.NewRecorder()
	writeSOAPFault(rr, upnpErrorCode(999))
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>999</errorCode>") {
		t.Errorf("body missing errorCode 999")
	}
	if !strings.Contains(rr.Body.String(), "UPnP Error") {
		t.Errorf("body missing fallback description")
	}
}

// ---- writeSOAPResponse ----

func TestWriteSOAPResponse_Shape(t *testing.T) {
	rr := httptest.NewRecorder()
	writeSOAPResponse(rr,
		"urn:schemas-upnp-org:service:RenderingControl:1",
		"GetVolume",
		[]soapOutArg{{Name: "CurrentVolume", Value: "75"}},
	)

	if rr.Code != 200 {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if ct != `text/xml; charset="utf-8"` {
		t.Errorf("Content-Type = %q, want %q", ct, `text/xml; charset="utf-8"`)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<s:Envelope`,
		`<u:GetVolumeResponse xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">`,
		`<CurrentVolume>75</CurrentVolume>`,
		`</u:GetVolumeResponse>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestWriteSOAPResponse_EscapesValues(t *testing.T) {
	rr := httptest.NewRecorder()
	writeSOAPResponse(rr, "urn:test", "Echo", []soapOutArg{
		{Name: "Foo", Value: `<bad> & "quotes"`},
	})
	body := rr.Body.String()
	// Raw <bad> must not appear in the output — must be escaped.
	if strings.Contains(body, "<bad>") {
		t.Errorf("body contains raw <bad>: %s", body)
	}
	if !strings.Contains(body, "&lt;bad&gt;") {
		t.Errorf("body missing escaped &lt;bad&gt;: %s", body)
	}
	if !strings.Contains(body, "&amp;") {
		t.Errorf("body missing escaped &amp;: %s", body)
	}
}
