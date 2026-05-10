package dlna

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rcSOAPRequest builds a SOAP envelope for a RenderingControl action.
func rcSOAPRequest(action, argsXML string) (*http.Request, *httptest.ResponseRecorder) {
	body := fmt.Sprintf(
		`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">`+
			`<s:Body><u:%s xmlns:u=%q>%s</u:%s></s:Body></s:Envelope>`,
		action, renderingControlServiceURN, argsXML, action,
	)
	req := httptest.NewRequest("POST", "/dlna/control/RenderingControl", strings.NewReader(body))
	req.Header.Set("SOAPACTION",
		fmt.Sprintf(`"%s#%s"`, renderingControlServiceURN, action))
	return req, httptest.NewRecorder()
}

// ---- ListPresets ----

func TestRC_ListPresets_FactoryDefaults(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, rr := rcSOAPRequest("ListPresets", "<InstanceID>0</InstanceID>")
	a.handleRenderingControlSOAP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<CurrentPresetNameList>FactoryDefaults</CurrentPresetNameList>") {
		t.Errorf("body missing FactoryDefaults: %s", rr.Body.String())
	}
}

// ---- SelectPreset ----

func TestRC_SelectPreset_FactoryDefaults_Resets(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Mutate state away from defaults so we can prove SelectPreset resets it.
	a.mu.Lock()
	a.volume = 25
	a.muted = true
	a.mu.Unlock()

	req, rr := rcSOAPRequest("SelectPreset",
		"<InstanceID>0</InstanceID><PresetName>FactoryDefaults</PresetName>")
	a.handleRenderingControlSOAP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.volume != 100 {
		t.Errorf("volume = %d, want 100 after FactoryDefaults reset", a.volume)
	}
	if a.muted {
		t.Errorf("muted = true, want false after FactoryDefaults reset")
	}
}

func TestRC_SelectPreset_Unknown_Returns701(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, rr := rcSOAPRequest("SelectPreset",
		"<InstanceID>0</InstanceID><PresetName>HighFidelity</PresetName>")
	a.handleRenderingControlSOAP(rr, req)
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>701</errorCode>") {
		t.Errorf("body missing 701: %s", rr.Body.String())
	}
}

// ---- GetVolume ----

func TestRC_GetVolume_DefaultIs100(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, rr := rcSOAPRequest("GetVolume",
		"<InstanceID>0</InstanceID><Channel>Master</Channel>")
	a.handleRenderingControlSOAP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<CurrentVolume>100</CurrentVolume>") {
		t.Errorf("body missing CurrentVolume=100: %s", rr.Body.String())
	}
}

func TestRC_GetVolume_AfterSet(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.mu.Lock()
	a.volume = 73
	a.mu.Unlock()

	req, rr := rcSOAPRequest("GetVolume",
		"<InstanceID>0</InstanceID><Channel>Master</Channel>")
	a.handleRenderingControlSOAP(rr, req)
	if !strings.Contains(rr.Body.String(), "<CurrentVolume>73</CurrentVolume>") {
		t.Errorf("body missing 73: %s", rr.Body.String())
	}
}

// ---- SetVolume ----

func TestRC_SetVolume_UpdatesState(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, rr := rcSOAPRequest("SetVolume",
		"<InstanceID>0</InstanceID><Channel>Master</Channel><DesiredVolume>42</DesiredVolume>")
	a.handleRenderingControlSOAP(rr, req)
	if rr.Code != 200 {
		t.Errorf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	a.mu.Lock()
	got := a.volume
	a.mu.Unlock()
	if got != 42 {
		t.Errorf("volume = %d, want 42", got)
	}
}

func TestRC_SetVolume_OutOfRange_Returns402(t *testing.T) {
	for _, raw := range []string{"-1", "101", "200", "abc", ""} {
		t.Run(raw, func(t *testing.T) {
			a, err := New(validAdapterConfig())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req, rr := rcSOAPRequest("SetVolume",
				fmt.Sprintf("<InstanceID>0</InstanceID><Channel>Master</Channel><DesiredVolume>%s</DesiredVolume>", raw))
			a.handleRenderingControlSOAP(rr, req)
			if rr.Code != 500 {
				t.Errorf("raw=%q status = %d, want 500", raw, rr.Code)
			}
			if !strings.Contains(rr.Body.String(), "<errorCode>402</errorCode>") {
				t.Errorf("raw=%q body missing 402: %s", raw, rr.Body.String())
			}
			a.mu.Lock()
			got := a.volume
			a.mu.Unlock()
			if got != 100 {
				t.Errorf("volume = %d after rejected SetVolume, want 100 (unchanged)", got)
			}
		})
	}
}

func TestRC_SetVolume_Boundaries(t *testing.T) {
	for _, ok := range []string{"0", "1", "50", "99", "100"} {
		t.Run(ok, func(t *testing.T) {
			a, err := New(validAdapterConfig())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req, rr := rcSOAPRequest("SetVolume",
				fmt.Sprintf("<InstanceID>0</InstanceID><Channel>Master</Channel><DesiredVolume>%s</DesiredVolume>", ok))
			a.handleRenderingControlSOAP(rr, req)
			if rr.Code != 200 {
				t.Errorf("raw=%s status = %d, want 200", ok, rr.Code)
			}
		})
	}
}

// ---- GetMute / SetMute ----

func TestRC_GetMute_DefaultIsZero(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, rr := rcSOAPRequest("GetMute",
		"<InstanceID>0</InstanceID><Channel>Master</Channel>")
	a.handleRenderingControlSOAP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<CurrentMute>0</CurrentMute>") {
		t.Errorf("body missing CurrentMute=0: %s", rr.Body.String())
	}
}

func TestRC_SetMute_AcceptsAllUPnPBoolForms(t *testing.T) {
	tests := []struct {
		input    string
		wantBool bool
	}{
		{"0", false},
		{"1", true},
		{"false", false},
		{"true", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			a, err := New(validAdapterConfig())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req, rr := rcSOAPRequest("SetMute",
				fmt.Sprintf("<InstanceID>0</InstanceID><Channel>Master</Channel><DesiredMute>%s</DesiredMute>", tt.input))
			a.handleRenderingControlSOAP(rr, req)
			if rr.Code != 200 {
				t.Errorf("input=%q status = %d, want 200; body=%s", tt.input, rr.Code, rr.Body.String())
			}
			a.mu.Lock()
			got := a.muted
			a.mu.Unlock()
			if got != tt.wantBool {
				t.Errorf("input=%q: muted = %v, want %v", tt.input, got, tt.wantBool)
			}
		})
	}
}

func TestRC_SetMute_RejectsBogusValue(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, rr := rcSOAPRequest("SetMute",
		"<InstanceID>0</InstanceID><Channel>Master</Channel><DesiredMute>maybe</DesiredMute>")
	a.handleRenderingControlSOAP(rr, req)
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>402</errorCode>") {
		t.Errorf("body missing 402: %s", rr.Body.String())
	}
}

// ---- Channel != Master rejection ----

func TestRC_NonMasterChannel_Returns402(t *testing.T) {
	tests := []struct {
		action  string
		argsXML string
	}{
		{"GetVolume", "<InstanceID>0</InstanceID><Channel>LF</Channel>"},
		{"GetMute", "<InstanceID>0</InstanceID><Channel>RF</Channel>"},
		{"SetVolume", "<InstanceID>0</InstanceID><Channel>LFE</Channel><DesiredVolume>50</DesiredVolume>"},
		{"SetMute", "<InstanceID>0</InstanceID><Channel></Channel><DesiredMute>1</DesiredMute>"},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			a, err := New(validAdapterConfig())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			req, rr := rcSOAPRequest(tt.action, tt.argsXML)
			a.handleRenderingControlSOAP(rr, req)
			if rr.Code != 500 {
				t.Errorf("status = %d, want 500", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), "<errorCode>402</errorCode>") {
				t.Errorf("body missing 402: %s", rr.Body.String())
			}
		})
	}
}

// ---- Unknown action ----

func TestRC_UnknownAction_Returns401(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, rr := rcSOAPRequest("GetBrightness", "<InstanceID>0</InstanceID>")
	a.handleRenderingControlSOAP(rr, req)
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>401</errorCode>") {
		t.Errorf("body missing 401: %s", rr.Body.String())
	}
}

// ---- InstanceID validation ----

func TestRC_NonZeroInstanceID_Returns718(t *testing.T) {
	a, err := New(validAdapterConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, rr := rcSOAPRequest("GetVolume", "<InstanceID>3</InstanceID><Channel>Master</Channel>")
	a.handleRenderingControlSOAP(rr, req)
	if rr.Code != 500 {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<errorCode>718</errorCode>") {
		t.Errorf("body missing 718: %s", rr.Body.String())
	}
}
