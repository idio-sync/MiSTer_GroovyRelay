package jellyfin

import "strings"

// linkVersion is the build version sent in the MediaBrowser auth
// header. Overridden in tests if needed; populated by main.go via
// SetVersion().
var linkVersion = "dev"

// SetVersion is called once at startup from main.go to thread the
// build version through to JF auth headers.
func (a *Adapter) SetVersion(v string) { linkVersion = v }

// configuredServerURL returns the saved Server URL from cfg under the
// adapter mutex. Used by StartLink/linkSnapshot so callers don't have
// to reach into cfg directly under their own locking.
func (a *Adapter) configuredServerURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.TrimSpace(a.cfg.ServerURL)
}
