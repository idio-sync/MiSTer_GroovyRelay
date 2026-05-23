package chassis

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestHandleTransportAction_SuccessDispatchesRequestAndReturnsNoContent(t *testing.T) {
	controller := &fakeTransportController{}
	s := &Server{transportController: controller}

	w := postTransportAction(t, s, url.Values{
		"action":      {" pause "},
		"adapter_ref": {" url:abc "},
		"generation":  {"42"},
	}.Encode())

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", w.Body.String())
	}
	assertNoStore(t, w)

	want := []adapters.PlaybackActionRequest{{
		Action:     adapters.PlaybackActionPause,
		AdapterRef: "url:abc",
		Generation: 42,
	}}
	if !reflect.DeepEqual(controller.calls, want) {
		t.Fatalf("controller calls = %#v, want %#v", controller.calls, want)
	}
}

func TestHandleTransportAction_StaleGenerationReturnsConflict(t *testing.T) {
	controller := &fakeTransportController{err: adapters.ErrActiveSessionChanged}
	s := &Server{transportController: controller}

	w := postTransportAction(t, s, validTransportForm(adapters.PlaybackActionStop))

	assertTransportJSONError(t, w, http.StatusConflict, adapters.ErrActiveSessionChangedMessage)
}

func TestHandleTransportAction_UnsupportedActionReturnsUnprocessableEntity(t *testing.T) {
	unsupportedErr := adapters.UnsupportedPlaybackActionError(`unknown playback action "previous"`)
	controller := &fakeTransportController{err: unsupportedErr}
	s := &Server{transportController: controller}

	w := postTransportAction(t, s, validTransportForm(adapters.PlaybackActionPrevious))

	assertTransportJSONError(t, w, http.StatusUnprocessableEntity, unsupportedErr.Error())
}

func TestHandleTransportAction_GenericProviderErrorReturnsGenericInternalError(t *testing.T) {
	providerErr := errors.New("provider secret token 12345 failed")
	controller := &fakeTransportController{err: providerErr}
	s := &Server{transportController: controller}

	w := postTransportAction(t, s, validTransportForm(adapters.PlaybackActionStop))

	assertTransportJSONError(t, w, http.StatusInternalServerError, "internal dispatch failure")
	if strings.Contains(w.Body.String(), providerErr.Error()) || strings.Contains(w.Body.String(), "token 12345") {
		t.Fatalf("client body leaked provider error: %q", w.Body.String())
	}
}

func TestHandleTransportAction_MalformedAndMissingGenerationReturnBadRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		msg  string
	}{
		{name: "malformed form", body: "%zz", msg: "malformed form body"},
		{name: "missing generation", body: url.Values{
			"action":      {adapters.PlaybackActionPause},
			"adapter_ref": {"url:abc"},
		}.Encode(), msg: "missing generation field"},
		{name: "invalid generation", body: url.Values{
			"action":      {adapters.PlaybackActionPause},
			"adapter_ref": {"url:abc"},
			"generation":  {"not-a-number"},
		}.Encode(), msg: "invalid generation field"},
		{name: "negative generation", body: url.Values{
			"action":      {adapters.PlaybackActionPause},
			"adapter_ref": {"url:abc"},
			"generation":  {"-1"},
		}.Encode(), msg: "invalid generation field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := &fakeTransportController{}
			s := &Server{transportController: controller}

			w := postTransportAction(t, s, tc.body)

			assertTransportJSONError(t, w, http.StatusBadRequest, tc.msg)
			if len(controller.calls) != 0 {
				t.Fatalf("controller calls = %#v, want none", controller.calls)
			}
		})
	}
}

func TestHandleTransportAction_RejectsSeekRoute(t *testing.T) {
	controller := &fakeTransportController{}
	s := &Server{transportController: controller}

	w := postTransportAction(t, s, validTransportForm(adapters.PlaybackActionSeek))

	assertTransportJSONError(t, w, http.StatusBadRequest, "seek must use the /receiver/transport/seek route")
	if len(controller.calls) != 0 {
		t.Fatalf("controller calls = %#v, want none", controller.calls)
	}
}

func TestHandleTransportAction_RejectsZeroGeneration(t *testing.T) {
	controller := &fakeTransportController{}
	s := &Server{transportController: controller}

	w := postTransportAction(t, s, url.Values{
		"action":      {adapters.PlaybackActionPause},
		"adapter_ref": {"url:abc"},
		"generation":  {"0"},
	}.Encode())

	assertTransportJSONError(t, w, http.StatusBadRequest, "generation must be greater than zero")
	if len(controller.calls) != 0 {
		t.Fatalf("controller calls = %#v, want none", controller.calls)
	}
}

func TestHandleTransportAction_RejectsMissingFieldsAndUnknownAction(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		msg  string
	}{
		{name: "missing adapter ref", body: url.Values{
			"action":     {adapters.PlaybackActionPause},
			"generation": {"42"},
		}.Encode(), msg: "missing adapter_ref field"},
		{name: "missing action", body: url.Values{
			"adapter_ref": {"url:abc"},
			"generation":  {"42"},
		}.Encode(), msg: "missing action field"},
		{name: "unknown action", body: url.Values{
			"action":      {"shuffle"},
			"adapter_ref": {"url:abc"},
			"generation":  {"42"},
		}.Encode(), msg: "unknown action"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := &fakeTransportController{}
			s := &Server{transportController: controller}

			w := postTransportAction(t, s, tc.body)

			assertTransportJSONError(t, w, http.StatusBadRequest, tc.msg)
			if len(controller.calls) != 0 {
				t.Fatalf("controller calls = %#v, want none", controller.calls)
			}
		})
	}
}

func TestHandleTransportAction_NilTransportControllerReturnsInternalServerError(t *testing.T) {
	s := &Server{}

	w := postTransportAction(t, s, validTransportForm(adapters.PlaybackActionStop))

	assertTransportJSONError(t, w, http.StatusInternalServerError, "transport controller not configured")
}

func TestHandleTransportSeek_SuccessDispatchesRequestAndReturnsNoContent(t *testing.T) {
	controller := &fakeTransportController{}
	s := &Server{transportController: controller}

	w := postTransportSeek(t, s, url.Values{
		"adapter_ref": {" url:abc "},
		"generation":  {"42"},
		"offset_ms":   {" 15000 "},
	}.Encode())

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%q", w.Code, http.StatusNoContent, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", w.Body.String())
	}
	assertNoStore(t, w)

	want := []adapters.PlaybackActionRequest{{
		Action:     adapters.PlaybackActionSeek,
		AdapterRef: "url:abc",
		Generation: 42,
		OffsetMS:   15000,
	}}
	if !reflect.DeepEqual(controller.calls, want) {
		t.Fatalf("controller calls = %#v, want %#v", controller.calls, want)
	}
}

func TestHandleTransportSeek_NonIntegerOffsetReturnsBadRequest(t *testing.T) {
	controller := &fakeTransportController{}
	s := &Server{transportController: controller}

	w := postTransportSeek(t, s, url.Values{
		"adapter_ref": {"url:abc"},
		"generation":  {"42"},
		"offset_ms":   {"twelve"},
	}.Encode())

	assertTransportJSONError(t, w, http.StatusBadRequest, "offset_ms must be an integer")
	if len(controller.calls) != 0 {
		t.Fatalf("controller calls = %#v, want none", controller.calls)
	}
}

func TestHandleTransportSeek_StaleGenerationReturnsConflict(t *testing.T) {
	controller := &fakeTransportController{err: adapters.ErrActiveSessionChanged}
	s := &Server{transportController: controller}

	w := postTransportSeek(t, s, validTransportSeekForm())

	assertTransportJSONError(t, w, http.StatusConflict, adapters.ErrActiveSessionChangedMessage)
}

func TestHandleTransportSeek_UnsupportedActionReturnsUnprocessableEntity(t *testing.T) {
	unsupportedErr := adapters.UnsupportedPlaybackActionError(`seek unavailable`)
	controller := &fakeTransportController{err: unsupportedErr}
	s := &Server{transportController: controller}

	w := postTransportSeek(t, s, validTransportSeekForm())

	assertTransportJSONError(t, w, http.StatusUnprocessableEntity, unsupportedErr.Error())
}

func TestHandleTransportSeek_GenericProviderErrorReturnsGenericInternalError(t *testing.T) {
	providerErr := errors.New("provider secret token 12345 failed")
	controller := &fakeTransportController{err: providerErr}
	s := &Server{transportController: controller}

	w := postTransportSeek(t, s, validTransportSeekForm())

	assertTransportJSONError(t, w, http.StatusInternalServerError, "internal dispatch failure")
	if strings.Contains(w.Body.String(), providerErr.Error()) || strings.Contains(w.Body.String(), "token 12345") {
		t.Fatalf("client body leaked provider error: %q", w.Body.String())
	}
}

func TestHandleTransportSeek_NilTransportControllerReturnsInternalServerError(t *testing.T) {
	s := &Server{}

	w := postTransportSeek(t, s, validTransportSeekForm())

	assertTransportJSONError(t, w, http.StatusInternalServerError, "transport controller not configured")
}

func TestHandleTransportSeek_MalformedAndMissingFieldsReturnBadRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		msg  string
	}{
		{name: "malformed form", body: "%zz", msg: "malformed form body"},
		{name: "missing adapter ref", body: url.Values{
			"generation": {"42"},
			"offset_ms":  {"15000"},
		}.Encode(), msg: "missing adapter_ref field"},
		{name: "missing generation", body: url.Values{
			"adapter_ref": {"url:abc"},
			"offset_ms":   {"15000"},
		}.Encode(), msg: "missing generation field"},
		{name: "invalid generation", body: url.Values{
			"adapter_ref": {"url:abc"},
			"generation":  {"not-a-number"},
			"offset_ms":   {"15000"},
		}.Encode(), msg: "invalid generation field"},
		{name: "zero generation", body: url.Values{
			"adapter_ref": {"url:abc"},
			"generation":  {"0"},
			"offset_ms":   {"15000"},
		}.Encode(), msg: "generation must be greater than zero"},
		{name: "missing offset", body: url.Values{
			"adapter_ref": {"url:abc"},
			"generation":  {"42"},
		}.Encode(), msg: "offset_ms must be an integer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := &fakeTransportController{}
			s := &Server{transportController: controller}

			w := postTransportSeek(t, s, tc.body)

			assertTransportJSONError(t, w, http.StatusBadRequest, tc.msg)
			if len(controller.calls) != 0 {
				t.Fatalf("controller calls = %#v, want none", controller.calls)
			}
		})
	}
}

func postTransportAction(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleTransportAction(w, req)
	return w
}

func validTransportForm(action string) string {
	return url.Values{
		"action":      {action},
		"adapter_ref": {"url:abc"},
		"generation":  {"42"},
	}.Encode()
}

func assertTransportJSONError(t *testing.T, w *httptest.ResponseRecorder, status int, msg string) {
	t.Helper()
	assertJSONError(t, w, status, msg)
	assertNoStore(t, w)
}

func assertNoStore(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
	}
}

func postTransportSeek(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/receiver/transport/seek", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleTransportSeek(w, req)
	return w
}

func validTransportSeekForm() string {
	return url.Values{
		"adapter_ref": {"url:abc"},
		"generation":  {"42"},
		"offset_ms":   {"15000"},
	}.Encode()
}

type fakeTransportController struct {
	calls []adapters.PlaybackActionRequest
	err   error
}

func (f *fakeTransportController) HandlePlaybackAction(ctx context.Context, req adapters.PlaybackActionRequest) (adapters.PlaybackActionResult, error) {
	f.calls = append(f.calls, req)
	return adapters.PlaybackActionResult{}, f.err
}
