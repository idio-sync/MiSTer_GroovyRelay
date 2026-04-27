package jellyfin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildAuthHeader_WithToken(t *testing.T) {
	got := BuildAuthHeader(AuthHeaderInput{
		Token:    "abc123",
		Client:   "MiSTer_GroovyRelay",
		Device:   "MiSTer",
		DeviceID: "uuid-xyz",
		Version:  "0.1.0",
	})
	wantParts := []string{
		`MediaBrowser`,
		`Token="abc123"`,
		`Client="MiSTer_GroovyRelay"`,
		`Device="MiSTer"`,
		`DeviceId="uuid-xyz"`,
		`Version="0.1.0"`,
	}
	for _, p := range wantParts {
		if !strings.Contains(got, p) {
			t.Errorf("BuildAuthHeader missing %q in: %s", p, got)
		}
	}
}

func TestBuildAuthHeader_WithoutToken(t *testing.T) {
	got := BuildAuthHeader(AuthHeaderInput{
		Client:   "MiSTer_GroovyRelay",
		Device:   "MiSTer",
		DeviceID: "uuid-xyz",
		Version:  "0.1.0",
	})
	if strings.Contains(got, `Token=`) {
		t.Errorf("BuildAuthHeader without token should omit Token=; got: %s", got)
	}
	if !strings.Contains(got, `Client="MiSTer_GroovyRelay"`) {
		t.Errorf("BuildAuthHeader without token: %s", got)
	}
}

func TestAuthenticateByName_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/Users/AuthenticateByName" {
			t.Errorf("path = %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "MediaBrowser ") {
			t.Errorf("Authorization = %q, want prefix MediaBrowser", auth)
		}
		if strings.Contains(auth, "Token=") {
			t.Errorf("auth call should not carry Token; got: %s", auth)
		}
		var body struct {
			Username string
			Pw       string
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Username != "alice" || body.Pw != "s3cret" {
			t.Errorf("body = %+v, want alice/s3cret", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"AccessToken":"tok-1234",
			"User":{"Id":"user-id","Name":"alice"},
			"ServerId":"server-abc"
		}`))
	}))
	defer srv.Close()

	got, err := AuthenticateByName(t.Context(), AuthRequest{
		ServerURL: srv.URL,
		Username:  "alice",
		Password:  "s3cret",
		DeviceID:  "uuid-xyz",
		Version:   "0.1.0",
	})
	if err != nil {
		t.Fatalf("AuthenticateByName: %v", err)
	}
	if got.AccessToken != "tok-1234" {
		t.Errorf("AccessToken = %q", got.AccessToken)
	}
	if got.UserID != "user-id" {
		t.Errorf("UserID = %q", got.UserID)
	}
	if got.UserName != "alice" {
		t.Errorf("UserName = %q", got.UserName)
	}
	if got.ServerID != "server-abc" {
		t.Errorf("ServerID = %q", got.ServerID)
	}
}

func TestAuthenticateByName_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := AuthenticateByName(t.Context(), AuthRequest{
		ServerURL: srv.URL,
		Username:  "alice",
		Password:  "wrong",
		DeviceID:  "uuid-xyz",
		Version:   "0.1.0",
	})
	if err == nil {
		t.Fatal("AuthenticateByName(401) returned nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid credentials") {
		t.Errorf("err = %v, want substring 'invalid credentials'", err)
	}
}

func TestAuthenticateByName_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := AuthenticateByName(t.Context(), AuthRequest{
		ServerURL: srv.URL,
		Username:  "alice",
		Password:  "s3cret",
		DeviceID:  "uuid-xyz",
		Version:   "0.1.0",
	})
	if err == nil {
		t.Fatal("AuthenticateByName(500) returned nil, want error")
	}
	if !strings.Contains(err.Error(), "server unreachable") && !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want substring 'server unreachable' or '500'", err)
	}
}

func TestAuthenticateByName_NetworkError(t *testing.T) {
	_, err := AuthenticateByName(t.Context(), AuthRequest{
		ServerURL: "http://127.0.0.1:0", // guaranteed-closed port
		Username:  "alice",
		Password:  "s3cret",
		DeviceID:  "uuid-xyz",
		Version:   "0.1.0",
	})
	if err == nil {
		t.Fatal("AuthenticateByName(closed port) returned nil, want error")
	}
}
