package aux

import (
	"fmt"
	"net/url"
	"strings"
)

func validateStreamURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("stream URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse stream URL: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("stream URL must use http or https")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("stream URL host is required")
	}
	if u.User != nil {
		return nil, fmt.Errorf("stream URL must not include userinfo")
	}
	if u.Fragment != "" {
		return nil, fmt.Errorf("stream URL must not include a fragment")
	}
	return u, nil
}
