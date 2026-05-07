package plex

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/core"

// LastPlaySessionForTest is an exported alias for lastPlaySession used
// by cross-package integration tests (tests/integration/url_test.go).
// Production code uses the lowercase form.
func (c *Companion) LastPlaySessionForTest() PlayMediaRequest {
	return c.lastPlaySession()
}

// SessionRequestForTest is an exported alias for sessionRequestForPreset used
// by cross-package integration tests. Resolves the live modeline mirror and
// returns core.SessionRequest directly so the test can override StreamURL or
// other fields before passing to Manager.StartSession. Falls back to the
// default preset on resolution failure to keep test ergonomics simple — the
// production handlers HTTP-400 instead.
func (c *Companion) SessionRequestForTest(p PlayMediaRequest) core.SessionRequest {
	preset, err := c.currentPreset()
	if err != nil {
		preset, _ = core.ResolvePreset("")
	}
	return c.sessionRequestForPreset(p, preset)
}

// RememberPlaySessionForTest is an exported alias for rememberPlaySession.
func (c *Companion) RememberPlaySessionForTest(p PlayMediaRequest) {
	c.rememberPlaySession(p)
}
