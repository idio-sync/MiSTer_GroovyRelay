package plex

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompanion_RootReturns200(t *testing.T) {
	// Task 7.1 sanity: /resources must return 200 even without a wired-in
	// SessionManager — the resources endpoint advertises capabilities and does
	// not call into core. We pass nil here; other handlers (playMedia, etc.)
	// get a fakeCore in their own tests.
	c := NewCompanion(CompanionConfig{DeviceName: "MiSTer", DeviceUUID: "abc-123"}, nil)
	ts := httptest.NewServer(c.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/resources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}
