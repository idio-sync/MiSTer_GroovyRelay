package hlsbuffer

import (
	"net/url"
	"strings"
)

func safeURIForError(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "<empty>"
	}
	if u, err := url.Parse(s); err == nil {
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		if safe := strings.TrimSpace(u.String()); safe != "" {
			return safe
		}
	}
	if idx := strings.IndexAny(s, "?#"); idx >= 0 {
		s = s[:idx]
	}
	if at := strings.LastIndex(s, "@"); at >= 0 {
		prefix := s[:at]
		if strings.Contains(prefix, "://") {
			schemeEnd := strings.Index(prefix, "://") + len("://")
			s = s[:schemeEnd] + s[at+1:]
		} else if strings.HasPrefix(prefix, "//") {
			s = "//" + s[at+1:]
		}
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "<redacted uri>"
	}
	return s
}
