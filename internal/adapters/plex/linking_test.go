package plex

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestRequestPIN_PostsFormAndParsesResponse validates that the PIN request
// hits the documented endpoint, sets the required X-Plex-Client-Identifier
// form field, and decodes the JSON body into PinResponse.
func TestRequestPIN_PostsFormAndParsesResponse(t *testing.T) {
	var gotPath, gotClientID, gotDeviceName, gotContentType string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotClientID = r.PostForm.Get("X-Plex-Client-Identifier")
		gotDeviceName = r.PostForm.Get("X-Plex-Device-Name")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":42,"code":"ABCD","authToken":""}`)
	}))
	defer srv.Close()

	restore := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restore })

	pr, err := RequestPIN("client-xyz", "MiSTer-Test")
	if err != nil {
		t.Fatalf("RequestPIN: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/api/v2/pins" {
		t.Errorf("expected /api/v2/pins, got %s", gotPath)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("wrong content-type: %q", gotContentType)
	}
	if gotClientID != "client-xyz" {
		t.Errorf("wrong client id: %q", gotClientID)
	}
	if gotDeviceName != "MiSTer-Test" {
		t.Errorf("wrong device name: %q", gotDeviceName)
	}
	if pr.ID != 42 || pr.Code != "ABCD" {
		t.Errorf("unexpected PinResponse: %+v", pr)
	}
}

// TestRequestPIN_HTTPErrorSurfacesError ensures non-2xx responses produce an
// error rather than a zero-value PinResponse.
func TestRequestPIN_HTTPErrorSurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	restore := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restore })

	if _, err := RequestPIN("cid", "dn"); err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}

// TestPollPIN_ReturnsTokenOnSecondPoll stands up a stub that returns an empty
// authToken the first time and the real token the second time, validating
// that PollPIN retries until the token materializes.
func TestPollPIN_ReturnsTokenOnSecondPoll(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n < 2 {
			fmt.Fprint(w, `{"id":42,"code":"ABCD","authToken":""}`)
			return
		}
		fmt.Fprint(w, `{"id":42,"code":"ABCD","authToken":"real-token"}`)
	}))
	defer srv.Close()

	restore := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restore })

	// Shrink the poll interval for the test so we don't wait 2s between
	// attempts. The package-level var is in linking.go.
	restoreInterval := pollInterval
	pollInterval = 50 * time.Millisecond
	t.Cleanup(func() { pollInterval = restoreInterval })

	token, err := PollPIN(42, "client-xyz", 5*time.Second)
	if err != nil {
		t.Fatalf("PollPIN: %v", err)
	}
	if token != "real-token" {
		t.Errorf("expected real-token, got %q", token)
	}
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("expected >=2 polls, got %d", got)
	}
}

// TestPollPIN_TimesOut confirms the deadline path returns an error when the
// token never arrives.
func TestPollPIN_TimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":42,"code":"ABCD","authToken":""}`)
	}))
	defer srv.Close()

	restore := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restore })

	restoreInterval := pollInterval
	pollInterval = 20 * time.Millisecond
	t.Cleanup(func() { pollInterval = restoreInterval })

	if _, err := PollPIN(42, "cid", 100*time.Millisecond); err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
