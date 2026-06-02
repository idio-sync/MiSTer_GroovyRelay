package streams

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// userProviderType is the ProviderDefinition.Type value for user-authored
// providers. Its Groups/Channels are inline (not fetched), and its ID is
// always userProviderIDPrefix + slug.
const userProviderType = "user"

// userProviderIDPrefix namespaces user provider IDs so they can never
// collide with or shadow bundled/remote provider IDs in the merged maps.
const userProviderIDPrefix = "user:"

// Channel kinds (ChannelDefinition.Kind) for user providers.
const (
	kindPlaylist = "playlist"
	kindSingle   = "single"
	kindDirect   = "direct"
)

// Limits (spec §10).
const (
	maxUserProviders       = 32
	maxChannelsPerProvider = 100
)

// detectChannelKind infers a channel Kind from a URL using purely syntactic
// rules (no network). First match wins (spec §4.7):
//  1. direct  — path ends in a recognized HLS/DASH manifest suffix.
//  2. playlist — YouTube list= URL.
//  3. single  — everything else (the default).
func detectChannelKind(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return kindSingle
	}
	path := strings.ToLower(u.Path)
	for _, suf := range []string{".m3u8", ".m3u", ".mpd"} {
		if strings.HasSuffix(path, suf) {
			return kindDirect
		}
	}
	host := strings.ToLower(u.Hostname())
	if isYouTubeHost(host) && u.Query().Get("list") != "" {
		return kindPlaylist
	}
	return kindSingle
}

func isYouTubeHost(host string) bool {
	host = strings.TrimPrefix(host, "www.")
	return host == "youtube.com" || host == "m.youtube.com" || host == "music.youtube.com"
}

// badgeColorTokens is the curated palette (spec §8). Each token maps to a
// CRT-tuned .ic.u-<token> / .badge.u-<token> pair defined in chassis.css.
// Stored as a token, never raw hex, so the chassis never emits inline styles.
var badgeColorTokens = map[string]struct{}{
	"amber": {}, "red": {}, "teal": {}, "blue": {},
	"purple": {}, "green": {}, "cyan": {}, "slate": {},
}

const defaultBadgeColor = "slate"

func isUserProviderID(id string) bool {
	return len(id) > len(userProviderIDPrefix) && id[:len(userProviderIDPrefix)] == userProviderIDPrefix
}

// validateBadgeColor lowercases/trims a save-time token and rejects empty,
// unknown, and raw hex values. Persisted user input must fail loudly instead of
// being silently rewritten to a different color.
func validateBadgeColor(in string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(in))
	if _, ok := badgeColorTokens[t]; ok {
		return t, nil
	}
	return "", fmt.Errorf("badge_color must be one of amber, red, teal, blue, purple, green, cyan, slate")
}

// normalizeBadgeColorForLoad is the load-time fallback for malformed persisted
// data. Save-time validation stays strict, but a hand-edited file can never
// brick rendering (spec §4.3).
func normalizeBadgeColorForLoad(in string) string {
	t, err := validateBadgeColor(in)
	if err == nil {
		return t
	}
	return defaultBadgeColor
}

// validateGlyph enforces a 2–4 character (rune-counted) non-empty glyph.
func validateGlyph(g string) error {
	g = strings.TrimSpace(g)
	n := utf8.RuneCountInString(g)
	if n < 2 || n > 4 {
		return fmt.Errorf("glyph must be 2-4 characters, got %d", n)
	}
	return nil
}

// validateUserProviderID verifies the reserved user: namespace plus the normal
// manifest ID rules for the suffix.
func validateUserProviderID(id string) error {
	if !isUserProviderID(id) {
		return fmt.Errorf("provider id %q must start with %q", id, userProviderIDPrefix)
	}
	suffix := strings.TrimPrefix(id, userProviderIDPrefix)
	if err := validateManifestID("provider id", suffix); err != nil {
		return err
	}
	if suffix == reservedAdhocID {
		return fmt.Errorf("provider id %q is reserved", id)
	}
	return nil
}

// validateUserManifestID applies the existing manifest ID rule to user group
// and channel IDs. Channel IDs reject adhoc because that value is reserved for
// quick-cast snapshots.
func validateUserManifestID(label, id string, rejectAdhoc bool) error {
	if err := validateManifestID(label, id); err != nil {
		return err
	}
	if rejectAdhoc && id == reservedAdhocID {
		return fmt.Errorf("%s %q is reserved", label, id)
	}
	return nil
}

// slugify lowercases and reduces a display name to [a-z0-9-], collapsing
// runs of other characters to single hyphens and trimming edges. Returns
// fallback when nothing usable remains (so an ID is always derivable).
func slugify(name, fallback string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return fallback
	}
	return s
}

// uniqueSlug returns base, or base-2, base-3, ... until taken() is false.
func uniqueSlug(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		candidate := base + "-" + strconv.Itoa(n)
		if !taken(candidate) {
			return candidate
		}
	}
}

// newUserProviderID derives a locked "user:"-prefixed provider ID from a
// display name, de-duped against existing IDs via taken().
func newUserProviderID(displayName string, taken func(id string) bool) string {
	base := slugify(displayName, "provider")
	return uniqueSlug(userProviderIDPrefix+base, taken)
}

// newChannelID derives a locked channel ID (no "user:" prefix; scoped to its
// provider) from a channel name, de-duped against sibling channel IDs.
func newChannelID(channelName string, taken func(id string) bool) string {
	base := slugify(channelName, "channel")
	return uniqueSlug(base, taken)
}
