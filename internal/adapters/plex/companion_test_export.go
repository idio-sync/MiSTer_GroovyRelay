package plex

import "github.com/idio-sync/MiSTer_GroovyRelay/internal/core"

// LastPlaySessionForTest is an exported alias for lastPlaySession used
// by cross-package integration tests (tests/integration/url_test.go).
// Production code uses the lowercase form.
func (c *Companion) LastPlaySessionForTest() PlayMediaRequest {
	return c.lastPlaySession()
}

// SessionRequestForTest is an exported alias for sessionRequestFor used
// by cross-package integration tests. Returns core.SessionRequest
// directly so the test can override StreamURL or other fields before
// passing to Manager.StartSession.
func (c *Companion) SessionRequestForTest(p PlayMediaRequest) core.SessionRequest {
	return c.sessionRequestFor(p)
}

// RememberPlaySessionForTest is an exported alias for rememberPlaySession.
func (c *Companion) RememberPlaySessionForTest(p PlayMediaRequest) {
	c.rememberPlaySession(p)
}
