package dlna

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// avtSOAPRequest builds a SOAP envelope for an AVTransport action call.
// argsXML is the inner argument fragment (e.g. "<InstanceID>0</InstanceID>").
func avtSOAPRequest(t *testing.T, action, argsXML string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	body := fmt.Sprintf(
		`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">`+
			`<s:Body><u:%s xmlns:u=%q>%s</u:%s></s:Body></s:Envelope>`,
		action, avTransportServiceURN, argsXML, action,
	)
	req := httptest.NewRequest("POST", "/dlna/control/AVTransport", strings.NewReader(body))
	req.Header.Set("SOAPACTION",
		fmt.Sprintf(`"%s#%s"`, avTransportServiceURN, action))
	return req, httptest.NewRecorder()
}

// runAVT helper invokes handleAVTransportSOAP and returns the recorder.
func runAVT(t *testing.T, action, argsXML string) *httptest.ResponseRecorder {
	t.Helper()
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, rr := avtSOAPRequest(t, action, argsXML)
	a.handleAVTransportSOAP(rr, req)
	return rr
}

// ---- Impl actions return constant defaults ----

func TestAVT_GetMediaInfo_Defaults(t *testing.T) {
	rr := runAVT(t, "GetMediaInfo", "<InstanceID>0</InstanceID>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"<NrTracks>0</NrTracks>",
		"<MediaDuration>00:00:00</MediaDuration>",
		"<CurrentURI></CurrentURI>",
		"<NextURI></NextURI>",
		"<PlayMedium>NONE</PlayMedium>",
		"<RecordMedium>NOT_IMPLEMENTED</RecordMedium>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestAVT_GetTransportInfo_Defaults(t *testing.T) {
	rr := runAVT(t, "GetTransportInfo", "<InstanceID>0</InstanceID>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	for _, want := range []string{
		"<CurrentTransportState>STOPPED</CurrentTransportState>",
		"<CurrentTransportStatus>OK</CurrentTransportStatus>",
		"<CurrentSpeed>1</CurrentSpeed>",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestAVT_GetPositionInfo_Defaults(t *testing.T) {
	rr := runAVT(t, "GetPositionInfo", "<InstanceID>0</InstanceID>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	for _, want := range []string{
		"<Track>0</Track>",
		"<TrackDuration>00:00:00</TrackDuration>",
		"<RelTime>00:00:00</RelTime>",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestAVT_GetDeviceCapabilities(t *testing.T) {
	rr := runAVT(t, "GetDeviceCapabilities", "<InstanceID>0</InstanceID>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	for _, want := range []string{
		"<PlayMedia>NETWORK</PlayMedia>",
		"<RecMedia>NOT_IMPLEMENTED</RecMedia>",
		"<RecQualityModes>NOT_IMPLEMENTED</RecQualityModes>",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestAVT_GetTransportSettings(t *testing.T) {
	rr := runAVT(t, "GetTransportSettings", "<InstanceID>0</InstanceID>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	for _, want := range []string{
		"<PlayMode>NORMAL</PlayMode>",
		"<RecQualityMode>NOT_IMPLEMENTED</RecQualityMode>",
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestAVT_GetCurrentTransportActions_Empty(t *testing.T) {
	rr := runAVT(t, "GetCurrentTransportActions", "<InstanceID>0</InstanceID>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<Actions></Actions>") {
		t.Errorf("expected empty Actions element; body=\n%s", rr.Body.String())
	}
}

// ---- SetNextAVTransportURI ----

func TestAVT_SetNextAVTransportURI_Noop(t *testing.T) {
	rr := runAVT(t, "SetNextAVTransportURI",
		`<InstanceID>0</InstanceID><NextURI>http://example.com/next.mp4</NextURI><NextURIMetaData></NextURIMetaData>`)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// ---- SetPlayMode ----

func TestAVT_SetPlayMode_Normal(t *testing.T) {
	rr := runAVT(t, "SetPlayMode",
		"<InstanceID>0</InstanceID><NewPlayMode>NORMAL</NewPlayMode>")
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestAVT_SetPlayMode_NonNormalReturns712(t *testing.T) {
	for _, mode := range []string{"REPEAT_ONE", "SHUFFLE", "RANDOM", "INTRO"} {
		rr := runAVT(t, "SetPlayMode",
			fmt.Sprintf("<InstanceID>0</InstanceID><NewPlayMode>%s</NewPlayMode>", mode))
		if rr.Code != 500 {
			t.Errorf("mode=%s status = %d, want 500", mode, rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "<errorCode>712</errorCode>") {
			t.Errorf("mode=%s body missing errorCode 712: %s", mode, rr.Body.String())
		}
	}
}

// ---- Stub actions return 501 ----

func TestAVT_StubActions_Return501(t *testing.T) {
	// SetAVTransportURI moved out of the stub set in P2.3 (real
	// validate-and-store). Play/Stop moved out in P2.4 (real
	// handlers). The remaining four — Pause, Seek, Next, Previous —
	// still 501. Pause/Seek will flip in Phase 3; Next/Previous stay
	// stubs because v1 has no queue model.
	stubs := map[string]string{
		"Pause":    "<InstanceID>0</InstanceID>",
		"Seek":     "<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>00:00:30</Target>",
		"Next":     "<InstanceID>0</InstanceID>",
		"Previous": "<InstanceID>0</InstanceID>",
	}
	for action, argsXML := range stubs {
		t.Run(action, func(t *testing.T) {
			rr := runAVT(t, action, argsXML)
			if rr.Code != 500 {
				t.Errorf("status = %d, want 500", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), "<errorCode>501</errorCode>") {
				t.Errorf("body missing errorCode 501: %s", rr.Body.String())
			}
		})
	}
}

// ---- Unknown action returns 401 ----

func TestAVT_UnknownAction_Returns401(t *testing.T) {
	rr := runAVT(t, "DoesNotExist", "<InstanceID>0</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>401</errorCode>") {
		t.Errorf("body missing errorCode 401: %s", rr.Body.String())
	}
}

// ---- InstanceID validation on Impl actions ----

func TestAVT_NonZeroInstanceID_Returns718(t *testing.T) {
	// GetMediaInfo is an Impl action — InstanceID validation runs.
	rr := runAVT(t, "GetMediaInfo", "<InstanceID>5</InstanceID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>718</errorCode>") {
		t.Errorf("body missing errorCode 718: %s", rr.Body.String())
	}
}

// ---- Missing SOAPACTION header ----

func TestAVT_MissingSOAPActionHeader_Returns401(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">` +
		`<s:Body><u:Play xmlns:u="urn:test"/></s:Body></s:Envelope>`
	req := httptest.NewRequest("POST", "/dlna/control/AVTransport", strings.NewReader(body))
	// No SOAPACTION header.
	rr := httptest.NewRecorder()
	a.handleAVTransportSOAP(rr, req)
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>401</errorCode>") {
		t.Errorf("body missing errorCode 401: %s", rr.Body.String())
	}
}
