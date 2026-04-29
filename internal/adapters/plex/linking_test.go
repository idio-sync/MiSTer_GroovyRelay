package plex

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRequestPIN_PostsFormAndParsesResponse validates that the PIN request
// hits the documented endpoint, sets the required X-Plex-Client-Identifier
// form field, and decodes the JSON body into PinResponse.
func TestRequestPIN_PostsFormAndParsesResponse(t *testing.T) {
	var gotPath, gotClientID, gotDeviceName, gotContentType, gotStrong string
	var strongWasSet bool
	var gotMethod string
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotClientID = r.PostForm.Get("X-Plex-Client-Identifier")
		gotDeviceName = r.PostForm.Get("X-Plex-Device-Name")
		_, strongWasSet = r.PostForm["strong"]
		gotStrong = r.PostForm.Get("strong")
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
	// Regression guard: strong=true makes plex.tv return a ~25-char opaque
	// token, which plex.tv/link refuses. The flow must request the default
	// short (4-char) human code — so strong must be unset or explicitly false.
	if strongWasSet && gotStrong != "false" {
		t.Errorf("strong form field must be unset or false for plex.tv/link flow, got %q", gotStrong)
	}
	if pr.ID != 42 || pr.Code != "ABCD" {
		t.Errorf("unexpected PinResponse: %+v", pr)
	}
}

// TestRequestPIN_HTTPErrorSurfacesError ensures non-2xx responses produce an
// error rather than a zero-value PinResponse.
func TestRequestPIN_HTTPErrorSurfacesError(t *testing.T) {
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// TestRegisterDevice_PutsConnectionURI verifies the PUT path, token query
// parameter, and form body used to refresh the plex.tv device record.
func TestRegisterDevice_PutsConnectionURI(t *testing.T) {
	var gotMethod, gotPath, gotToken, gotContentType, gotURI, gotDeviceName string
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotToken = r.URL.Query().Get("X-Plex-Token")
		gotContentType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotURI = r.PostForm.Get("Connection[][uri]")
		gotDeviceName = r.PostForm.Get("X-Plex-Device-Name")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	restore := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restore })

	if err := RegisterDevice("uuid-xyz", "tok-123", "10.0.0.5", 32500, "MiSTer"); err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", gotMethod)
	}
	if gotPath != "/devices/uuid-xyz" {
		t.Errorf("expected /devices/uuid-xyz, got %s", gotPath)
	}
	if gotToken != "tok-123" {
		t.Errorf("expected token tok-123, got %s", gotToken)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("wrong content-type: %q", gotContentType)
	}
	if gotURI != "http://10.0.0.5:32500" {
		t.Errorf("wrong connection uri: %q", gotURI)
	}
	if gotDeviceName != "MiSTer" {
		t.Errorf("wrong device name: %q", gotDeviceName)
	}
}

// TestRunRegistrationLoop_FiresImmediatelyAndOnTick verifies both the eager
// first call and the ticker-driven refresh. The registerInterval package
// var is shortened so we don't wait the production 60s cadence.
func TestRunRegistrationLoop_FiresImmediatelyAndOnTick(t *testing.T) {
	var calls int32
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	restoreBase := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restoreBase })

	restoreInterval := registerInterval
	registerInterval = 100 * time.Millisecond
	t.Cleanup(func() { registerInterval = restoreInterval })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunRegistrationLoop(ctx, "uuid", "tok", "127.0.0.1", 32500, "MiSTer")
		close(done)
	}()

	// Give the loop time for immediate call + >=1 ticker fire.
	time.Sleep(350 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunRegistrationLoop did not return after context cancel")
	}

	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("expected >=2 register calls, got %d", got)
	}
}

// TestRegisterDevice_Returns4xxAsError verifies I9: a plex.tv 401 (expired
// token) surfaces as an error so the caller / ticker loop can log it,
// instead of being silently dropped.
func TestRegisterDevice_Returns4xxAsError(t *testing.T) {
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	oldBase := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = oldBase })

	err := RegisterDevice("uuid-x", "stale-token", "10.0.0.1", 32500, "MiSTer")
	if err == nil {
		t.Fatal("expected error from 401 response; got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

// TestRevokeDevice_DeletesViaLegacyXMLEndpoint verifies the unlink token-revoke
// path hits plex.tv's legacy /devices/{uuid}.xml endpoint with the auth token
// as a query parameter, matching python-plexapi's reference implementation
// and RegisterDevice's wire-format conventions in this same file. Plex's v2
// device endpoints are undocumented; the legacy path is what working Plex
// client SDKs actually use in production.
func TestRevokeDevice_DeletesViaLegacyXMLEndpoint(t *testing.T) {
	var gotMethod, gotPath, gotTokenHeader, gotTokenQuery string
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotTokenHeader = r.Header.Get("X-Plex-Token")
		gotTokenQuery = r.URL.Query().Get("X-Plex-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	restore := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restore })

	if err := RevokeDevice("uuid-xyz", "tok-123"); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotPath != "/devices/uuid-xyz.xml" {
		t.Errorf("expected /devices/uuid-xyz.xml, got %s", gotPath)
	}
	// Legacy convention: token in query string, matching RegisterDevice and
	// python-plexapi. Pinning this so a future v2 attempt doesn't accidentally
	// land in tree without explicit migration of the whole device path family.
	if gotTokenQuery != "tok-123" {
		t.Errorf("expected X-Plex-Token query=tok-123, got %q", gotTokenQuery)
	}
	if gotTokenHeader != "" {
		t.Errorf("token must not appear as header; got %q", gotTokenHeader)
	}
}

// TestRevokeDevice_Returns4xxAsError ensures plex.tv 401 (already-revoked
// token, expired token) surfaces as an error so handleUnlink can log it.
// Local cleanup proceeds regardless; the error is informational.
func TestRevokeDevice_Returns4xxAsError(t *testing.T) {
	srv := newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	restore := PlexAPIBase
	PlexAPIBase = srv.URL
	t.Cleanup(func() { PlexAPIBase = restore })

	err := RevokeDevice("uuid-x", "stale-token")
	if err == nil {
		t.Fatal("expected error from 401 response; got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

// TestRevokeDevice_TransportErrorSurfacesError pins behavior for the offline
// case: if plex.tv is unreachable, RevokeDevice returns an error rather than
// silently succeeding. handleUnlink will log and continue with local cleanup.
func TestRevokeDevice_TransportErrorSurfacesError(t *testing.T) {
	restore := PlexAPIBase
	// Unroutable address forces a transport failure inside the bounded
	// plexHTTPClient.Timeout window.
	PlexAPIBase = "http://127.0.0.1:1"
	t.Cleanup(func() { PlexAPIBase = restore })

	if err := RevokeDevice("uuid", "tok"); err == nil {
		t.Fatal("expected transport error, got nil")
	}
}

// TestPlexHTTPClient_HasTimeout verifies the shared client is configured
// with a bounded timeout so a hanging plex.tv call cannot wedge a ticker
// or caller.
func TestPlexHTTPClient_HasTimeout(t *testing.T) {
	if plexHTTPClient.Timeout <= 0 {
		t.Errorf("plexHTTPClient.Timeout = %v; must be > 0", plexHTTPClient.Timeout)
	}
	if plexHTTPClient.Timeout > 30*time.Second {
		t.Errorf("plexHTTPClient.Timeout = %v; too generous", plexHTTPClient.Timeout)
	}
}
