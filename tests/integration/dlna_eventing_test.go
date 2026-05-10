//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/dlna"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/core"
)

type dlnaCapturedNotify struct {
	SID  string
	SEQ  string
	Body string
}

type dlnaNotifyCapture struct {
	server *httptest.Server
	ch     chan dlnaCapturedNotify

	mu  sync.Mutex
	all []dlnaCapturedNotify
}

func newDLNANotifyCapture(t *testing.T) *dlnaNotifyCapture {
	t.Helper()

	c := &dlnaNotifyCapture{
		ch: make(chan dlnaCapturedNotify, 16),
	}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "NOTIFY" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		notify := dlnaCapturedNotify{
			SID:  r.Header.Get("SID"),
			SEQ:  r.Header.Get("SEQ"),
			Body: string(body),
		}
		c.mu.Lock()
		c.all = append(c.all, notify)
		c.mu.Unlock()
		c.ch <- notify

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.server.Close)

	return c
}

func (c *dlnaNotifyCapture) URL() string {
	return c.server.URL
}

func (c *dlnaNotifyCapture) wait(t *testing.T, wantSeq string) dlnaCapturedNotify {
	t.Helper()

	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()

	for {
		select {
		case notify := <-c.ch:
			if notify.SEQ == wantSeq {
				return notify
			}
		case <-timer.C:
			c.mu.Lock()
			all := append([]dlnaCapturedNotify(nil), c.all...)
			c.mu.Unlock()
			t.Fatalf("timed out waiting for NOTIFY SEQ %s; captured=%+v", wantSeq, all)
		}
	}
}

func TestDLNA_Eventing_SubscribeAndRenderingControlNotify(t *testing.T) {
	restore := dlna.SetDNSResolverForTesting(dlna.StaticIPResolver("192.168.99.1"))
	t.Cleanup(restore)

	callback := newDLNANotifyCapture(t)

	a, err := dlna.New(dlna.AdapterConfig{
		DeviceUUID: "44444444-5555-6666-7777-888888888888",
		HostIP:     "127.0.0.1",
		HTTPPort:   32500,
		Core: &dlnaStubCore{statusFn: func() core.SessionStatus {
			return core.SessionStatus{}
		}},
	})
	if err != nil {
		t.Fatalf("dlna.New: %v", err)
	}
	a.SetEnabled(true)

	mux := http.NewServeMux()
	a.MountPublicRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest("SUBSCRIBE", srv.URL+"/dlna/event/RenderingControl", nil)
	if err != nil {
		t.Fatalf("new subscribe request: %v", err)
	}
	req.Header.Set("CALLBACK", "<"+callback.URL()+">")
	req.Header.Set("NT", "upnp:event")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("subscribe do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("subscribe status = %d, want 200; body=%s", resp.StatusCode, snippet(string(body)))
	}
	sid := resp.Header.Get("SID")
	if sid == "" {
		t.Fatal("subscribe SID header is empty")
	}

	initial := callback.wait(t, "0")
	if initial.SID != sid {
		t.Fatalf("initial NOTIFY SID = %q, want %q", initial.SID, sid)
	}
	for _, want := range []string{"Volume", "100"} {
		if !strings.Contains(initial.Body, want) {
			t.Fatalf("initial NOTIFY body missing %q:\n%s", want, snippet(initial.Body))
		}
	}

	status, body := postRenderingControlSOAP(t, srv.URL, "SetVolume",
		"<InstanceID>0</InstanceID>"+
			"<Channel>Master</Channel>"+
			"<DesiredVolume>25</DesiredVolume>")
	if status != http.StatusOK {
		t.Fatalf("SetVolume status = %d, want 200; body=%s", status, snippet(body))
	}

	update := callback.wait(t, "1")
	if update.SID != sid {
		t.Fatalf("update NOTIFY SID = %q, want %q", update.SID, sid)
	}
	for _, want := range []string{"Volume", "25"} {
		if !strings.Contains(update.Body, want) {
			t.Fatalf("update NOTIFY body missing %q:\n%s", want, snippet(update.Body))
		}
	}
}

func postRenderingControlSOAP(t *testing.T, baseURL, action, args string) (int, string) {
	t.Helper()

	envelope := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" `+
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">`+
		`<s:Body><u:%[1]s xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">`+
		`%[2]s`+
		`</u:%[1]s></s:Body></s:Envelope>`, action, args)

	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/dlna/control/RenderingControl",
		strings.NewReader(envelope))
	if err != nil {
		t.Fatalf("new SOAP request: %v", err)
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION",
		`"urn:schemas-upnp-org:service:RenderingControl:1#`+action+`"`)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SOAP do: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read SOAP body: %v", err)
	}
	return resp.StatusCode, string(body)
}
