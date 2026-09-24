package adapters

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	errTextURLPattern = regexp.MustCompile(`(?i)\b(?:https?|wss?)://[^\s"'<>]+`)
	// Credential key=value pairs that appear outside a parseable URL
	// (e.g. a truncated URL or a hand-built message).
	errTextTokenPairPattern = regexp.MustCompile(`(?i)\b(api_key|apikey|x-plex-token|x-emby-token|access_?token|auth_token|token|password)=[^&\s"'<>]+`)
)

// RedactErrorText strips credentials from free-form error text before it
// is surfaced in Status.LastError, which the UI renders into tooltips,
// aria-labels, and SSE frames readable by anyone on the LAN. Embedded
// URLs lose their userinfo, query, and fragment (where tokens such as
// Jellyfin's ?api_key= live); stray credential key=value pairs have
// their values replaced. Messages without either pass through unchanged.
func RedactErrorText(msg string) string {
	if msg == "" {
		return msg
	}
	msg = errTextURLPattern.ReplaceAllStringFunc(msg, func(raw string) string {
		// Sentence punctuation directly after a URL ("dial wss://h/x: EOF")
		// is matched by the pattern; keep it outside the parsed URL.
		trimmed := strings.TrimRight(raw, ":;,.)")
		tail := raw[len(trimmed):]
		u, err := url.Parse(trimmed)
		if err != nil {
			return "<redacted url>" + tail
		}
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		u.RawFragment = ""
		return u.String() + tail
	})
	return errTextTokenPairPattern.ReplaceAllString(msg, "${1}=REDACTED")
}
