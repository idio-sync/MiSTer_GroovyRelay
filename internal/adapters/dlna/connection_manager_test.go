package dlna

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cmSOAPRequest builds a SOAP envelope for a ConnectionManager action.
func cmSOAPRequest(action, argsXML string) (*http.Request, *httptest.ResponseRecorder) {
	body := fmt.Sprintf(
		`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">`+
			`<s:Body><u:%s xmlns:u=%q>%s</u:%s></s:Body></s:Envelope>`,
		action, connectionManagerServiceURN, argsXML, action,
	)
	req := httptest.NewRequest("POST", "/dlna/control/ConnectionManager", strings.NewReader(body))
	req.Header.Set("SOAPACTION",
		fmt.Sprintf(`"%s#%s"`, connectionManagerServiceURN, action))
	return req, httptest.NewRecorder()
}

// runCM is a small helper for tests that don't need to inspect the
// adapter post-invocation.
func runCM(t *testing.T, action, argsXML string) *httptest.ResponseRecorder {
	t.Helper()
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, rr := cmSOAPRequest(action, argsXML)
	a.handleConnectionManagerSOAP(rr, req)
	return rr
}

// ---- GetProtocolInfo ----

func TestCM_GetProtocolInfo_SinkEntries(t *testing.T) {
	rr := runCM(t, "GetProtocolInfo", "")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()

	// Source must be empty.
	if !strings.Contains(body, "<Source></Source>") {
		t.Errorf("body missing empty Source element; body=%s", body)
	}

	// Each of the eight sink entries must appear.
	for _, want := range sinkProtocolInfoEntries {
		if !strings.Contains(body, want) {
			t.Errorf("body missing sink entry %q", want)
		}
	}
}

func TestCM_GetProtocolInfo_AdvertisesCachedHLSM3U8(t *testing.T) {
	rr := runCM(t, "GetProtocolInfo", "")
	body := rr.Body.String()
	for _, want := range []string{
		"http-get:*:application/vnd.apple.mpegurl:*",
		"http-get:*:application/x-mpegurl:*",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing cached-HLS sink entry %q; body=%s", want, body)
		}
	}
}

// ---- GetCurrentConnectionIDs ----

func TestCM_GetCurrentConnectionIDs_ReturnsZero(t *testing.T) {
	rr := runCM(t, "GetCurrentConnectionIDs", "")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<ConnectionIDs>0</ConnectionIDs>") {
		t.Errorf("body missing ConnectionIDs=0: %s", rr.Body.String())
	}
}

// ---- GetCurrentConnectionInfo ----

func TestCM_GetCurrentConnectionInfo_ZeroIsValid(t *testing.T) {
	rr := runCM(t, "GetCurrentConnectionInfo", "<ConnectionID>0</ConnectionID>")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"<RcsID>0</RcsID>",
		"<AVTransportID>0</AVTransportID>",
		"<PeerConnectionID>-1</PeerConnectionID>",
		"<Direction>Input</Direction>",
		"<Status>OK</Status>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestCM_GetCurrentConnectionInfo_NonZeroReturns701(t *testing.T) {
	rr := runCM(t, "GetCurrentConnectionInfo", "<ConnectionID>5</ConnectionID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing 701: %s", rr.Body.String())
	}
}

// ---- PrepareForConnection ----

func TestCM_PrepareForConnection_NoOpReturnsZeroIDs(t *testing.T) {
	rr := runCM(t, "PrepareForConnection",
		`<RemoteProtocolInfo>http-get:*:video/mp4:*</RemoteProtocolInfo>`+
			`<PeerConnectionManager>uuid:peer/cm</PeerConnectionManager>`+
			`<PeerConnectionID>0</PeerConnectionID>`+
			`<Direction>Input</Direction>`)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"<ConnectionID>0</ConnectionID>",
		"<AVTransportID>0</AVTransportID>",
		"<RcsID>0</RcsID>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// ---- ConnectionComplete ----

func TestCM_ConnectionComplete_ZeroSucceeds(t *testing.T) {
	rr := runCM(t, "ConnectionComplete", "<ConnectionID>0</ConnectionID>")
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCM_ConnectionComplete_NonZeroReturns701(t *testing.T) {
	rr := runCM(t, "ConnectionComplete", "<ConnectionID>9</ConnectionID>")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing 701: %s", rr.Body.String())
	}
}

// ---- Unknown action ----

func TestCM_UnknownAction_Returns401(t *testing.T) {
	rr := runCM(t, "GetFeatureList", "")
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>401</errorCode>") {
		t.Errorf("body missing 401: %s", rr.Body.String())
	}
}
