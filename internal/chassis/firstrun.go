// Package chassis firstrun.go implements receiver first-run "setup mode":
// the optional FirstRunController sentinel, the configured-enough status
// checks, and the cast-initiation gate. See
// docs/superpowers/specs/2026-06-02-receiver-first-run-setup-design.md.
package chassis

import "net/http"

// FirstRunController is the optional first-run sentinel backing receiver
// setup mode. Production passes *uiserver.BridgeSaver, which satisfies it
// structurally. When nil/unwired (unit-test fixtures), setup mode is never
// active and the chassis behaves exactly as before this feature.
type FirstRunController interface {
	IsFirstRun() bool
	DismissFirstRun() error
}

// resolveFirstRun returns the BridgeSaver as a FirstRunController when its
// concrete type implements the sentinel methods, else nil. A nil
// BridgeSettingsSaver interface yields nil (assertion fails), keeping
// setup mode off by default for fixtures that do not wire a saver.
func resolveFirstRun(bs BridgeSettingsSaver) FirstRunController {
	if frc, ok := bs.(FirstRunController); ok {
		return frc
	}
	return nil
}

// firstRunActive reports whether the receiver should render/enforce setup
// mode: a first-run controller is wired AND the sentinel is still set.
func (s *Server) firstRunActive() bool {
	return s.firstRun != nil && s.firstRun.IsFirstRun()
}

// SetupStatus is the configured-enough state surfaced to the page and the
// status endpoint. Mirrors internal/ui/setup.go firstIncompleteStep.
type SetupStatus struct {
	HostSet       bool `json:"hostSet"`
	SourceEnabled bool `json:"sourceEnabled"`
}

// Complete reports whether both first-run criteria are met.
func (st SetupStatus) Complete() bool { return st.HostSet && st.SourceEnabled }

// setupStatus computes the configured-enough sub-checks. Nil-safe: a nil
// BridgeSaver yields HostSet=false; a nil Registry yields SourceEnabled=false.
func (s *Server) setupStatus() SetupStatus {
	st := SetupStatus{}
	if s.cfg.BridgeSaver != nil {
		st.HostSet = s.cfg.BridgeSaver.Current().MiSTer.Host != ""
	}
	if s.cfg.Registry != nil {
		for _, a := range s.cfg.Registry.List() {
			if a.IsEnabled() {
				st.SourceEnabled = true
				break
			}
		}
	}
	return st
}

// requireSetupComplete refuses cast-initiation actions with 409 while
// first-run setup mode is active (sentinel still set). No-op once the
// sentinel is dismissed or when no first-run controller is wired.
func (s *Server) requireSetupComplete(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.firstRunActive() {
			writeSetupGate(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeSetupGate emits the consistent 409 FINISH SETUP chip payload.
func writeSetupGate(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusConflict)
	_, _ = w.Write([]byte(`{"chip":"FINISH SETUP","message":"Finish first-run setup before casting."}`))
}
