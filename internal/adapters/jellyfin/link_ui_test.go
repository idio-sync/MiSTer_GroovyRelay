package jellyfin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newLinkTestAdapter(t *testing.T, version string) *Adapter {
	t.Helper()
	return New(nil, t.TempDir(), "device-uuid")
}

func TestLinkUI_StartSuccess_PersistsTokenAndReturnsLinkedFragment(t *testing.T) {
	jfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"AccessToken":"tok-1","User":{"Id":"uid-1","Name":"alice"},"ServerId":"sid-1"}`))
	}))
	defer jfSrv.Close()

	a := newLinkTestAdapter(t, "0.1.0")
	a.cfg.ServerURL = jfSrv.URL

	form := url.Values{}
	form.Set("username", "alice")
	form.Set("password", "s3cret")
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/link/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	a.handleLinkStart(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "alice") {
		t.Errorf("body missing 'alice': %s", rr.Body.String())
	}
	if a.link.State() != LinkLinked {
		t.Errorf("link state = %v, want LinkLinked", a.link.State())
	}
	tok, err := LoadToken(a.tokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "tok-1" {
		t.Errorf("persisted token = %+v", tok)
	}
}

func TestLinkUI_StartBadCredentials_NoDiskWrite(t *testing.T) {
	jfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	defer jfSrv.Close()

	a := newLinkTestAdapter(t, "0.1.0")
	a.cfg.ServerURL = jfSrv.URL

	form := url.Values{}
	form.Set("username", "alice")
	form.Set("password", "wrong")
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/link/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	a.handleLinkStart(rr, req)

	if a.link.State() != LinkError {
		t.Errorf("link state = %v, want LinkError", a.link.State())
	}
	tok, _ := LoadToken(a.tokenPath())
	if tok != (Token{}) {
		t.Errorf("token persisted on auth failure: %+v", tok)
	}
}

func TestLinkUI_StartRejectsMissingServerURL(t *testing.T) {
	a := newLinkTestAdapter(t, "0.1.0")
	// cfg.ServerURL intentionally left empty: link should refuse without
	// touching the network.
	form := url.Values{}
	form.Set("username", "alice")
	form.Set("password", "s3cret")
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/link/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	a.handleLinkStart(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (form errors render as fragment)", rr.Code)
	}
	if a.link.State() != LinkError {
		t.Errorf("link state = %v, want LinkError on empty server_url", a.link.State())
	}
}

func TestLinkUI_StartRejectsEmptyCredentials(t *testing.T) {
	a := newLinkTestAdapter(t, "0.1.0")
	a.cfg.ServerURL = "https://jf.example.com"
	form := url.Values{}
	form.Set("username", "")
	form.Set("password", "")
	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/link/start", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	a.handleLinkStart(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (form errors render as fragment)", rr.Code)
	}
	if a.link.State() != LinkError {
		t.Errorf("link state = %v, want LinkError on empty creds", a.link.State())
	}
}

func TestLinkUI_Unlink_DeletesToken(t *testing.T) {
	a := newLinkTestAdapter(t, "0.1.0")
	if err := SaveToken(a.tokenPath(), Token{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	a.link.SetLinked("alice", "sid-1")

	req := httptest.NewRequest(http.MethodPost, "/ui/adapter/jellyfin/unlink", nil)
	rr := httptest.NewRecorder()
	a.handleUnlink(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if a.link.State() != LinkIdle {
		t.Errorf("link state after unlink = %v, want LinkIdle", a.link.State())
	}
	tok, _ := LoadToken(a.tokenPath())
	if tok != (Token{}) {
		t.Errorf("token still present after unlink: %+v", tok)
	}
}
