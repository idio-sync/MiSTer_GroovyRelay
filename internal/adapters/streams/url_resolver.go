package streams

import (
	"context"
	"fmt"
	stdurl "net/url"
	"sort"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/streamhandoff"
	"golang.org/x/net/idna"
)

func (a *Adapter) ResolveStreamURL(ctx context.Context, rawURL string) (streamhandoff.Resolution, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := a.ensureStartupSnapshot(ctx); err != nil {
		return streamhandoff.Resolution{}, false, err
	}

	parsed, err := stdurl.Parse(rawURL)
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return streamhandoff.Resolution{}, false, nil
	}

	scheme := strings.ToLower(parsed.Scheme)
	host, err := normalizeParsedRuleHost(parsed)
	if err != nil {
		return streamhandoff.Resolution{}, false, nil
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}

	a.mu.Lock()
	definitions := a.definitionsInOrderLocked()
	catalogs := make(map[string]ProviderCatalog, len(a.catalogs))
	for id, cat := range a.catalogs {
		catalogs[id] = cat
	}
	a.mu.Unlock()

	for _, def := range definitions {
		for _, rule := range def.URLRules {
			if !urlRuleMatchesLocation(rule, scheme, host, path) {
				continue
			}
			res := streamhandoff.Resolution{ProviderID: def.ID}
			if parsed.User != nil {
				return res, true, invalidExtraction(def.ID, "userinfo is not allowed in provider URLs")
			}

			extracted, matched, err := extractURLRuleValue(parsed, path, rule)
			if err != nil {
				return res, true, invalidExtraction(def.ID, err.Error())
			}
			if !matched {
				continue
			}

			switch rule.Target {
			case "channel":
				res.ChannelID = extracted
			case "item":
				res.ItemID = extracted
				if !youtubeIDRE.MatchString(extracted) {
					return res, true, invalidExtraction(def.ID, fmt.Sprintf("item %q is not a valid YouTube ID", extracted))
				}
				if cat, ok := catalogs[def.ID]; ok && !catalogContainsItem(cat, extracted) {
					res.ChannelID = reservedAdhocID
				}
			default:
				continue
			}
			if err := validateStreamResolution(catalogs, res); err != nil {
				return res, true, err
			}
			return res, true, nil
		}
	}

	return streamhandoff.Resolution{}, false, nil
}

func (a *Adapter) definitionsInOrderLocked() []ProviderDefinition {
	definitions := make([]ProviderDefinition, 0, len(a.definitions))
	seen := make(map[string]struct{}, len(a.definitions))
	for _, id := range a.definitionOrder {
		def, ok := a.definitions[id]
		if !ok {
			continue
		}
		definitions = append(definitions, def)
		seen[id] = struct{}{}
	}
	if len(seen) == len(a.definitions) {
		return definitions
	}

	missing := make([]string, 0, len(a.definitions)-len(seen))
	for id := range a.definitions {
		if _, ok := seen[id]; !ok {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	for _, id := range missing {
		definitions = append(definitions, a.definitions[id])
	}
	return definitions
}

func urlRuleMatchesLocation(rule URLRule, scheme, host, escapedPath string) bool {
	if !stringInList(scheme, rule.Schemes) {
		return false
	}
	if !hostInRuleHosts(host, rule.Hosts) {
		return false
	}
	if rule.Path != "" {
		return escapedPath == rule.Path
	}
	return rule.PathPrefix != "" && strings.HasPrefix(escapedPath, rule.PathPrefix)
}

func stringInList(needle string, haystack []string) bool {
	for _, item := range haystack {
		if strings.ToLower(strings.TrimSpace(item)) == needle {
			return true
		}
	}
	return false
}

func hostInRuleHosts(host string, ruleHosts []string) bool {
	for _, ruleHost := range ruleHosts {
		normalized, err := normalizeConfigHost(ruleHost)
		if err != nil {
			continue
		}
		if normalized == host {
			return true
		}
	}
	return false
}

func extractURLRuleValue(parsed *stdurl.URL, escapedPath string, rule URLRule) (string, bool, error) {
	if rule.Path != "" {
		values, ok := parsed.Query()[rule.QueryParam]
		if !ok {
			return "", false, nil
		}
		if len(values) != 1 {
			return "", true, fmt.Errorf("query parameter %q must appear exactly once", rule.QueryParam)
		}
		if values[0] == "" {
			return "", true, fmt.Errorf("query parameter %q must not be empty", rule.QueryParam)
		}
		return values[0], true, nil
	}

	rest := strings.TrimPrefix(escapedPath, rule.PathPrefix)
	if rest == "" {
		return "", true, fmt.Errorf("path prefix %q requires one following segment", rule.PathPrefix)
	}
	if strings.Contains(rest, "/") {
		return "", true, fmt.Errorf("path prefix %q accepts exactly one following segment", rule.PathPrefix)
	}
	value, err := stdurl.PathUnescape(rest)
	if err != nil {
		return "", true, fmt.Errorf("path segment decode: %w", err)
	}
	if value == "" {
		return "", true, fmt.Errorf("path prefix %q requires a non-empty segment", rule.PathPrefix)
	}
	return value, true, nil
}

func validateStreamResolution(catalogs map[string]ProviderCatalog, res streamhandoff.Resolution) error {
	if res.ProviderID == "" {
		return invalidExtraction("", "provider is required")
	}
	if res.ChannelID == "" && res.ItemID == "" {
		return invalidExtraction(res.ProviderID, "resolution must identify a channel or item")
	}
	if res.ChannelID != "" && res.ItemID != "" && res.ChannelID != reservedAdhocID {
		return invalidExtraction(res.ProviderID, "resolution must identify exactly one channel or item")
	}
	cat, ok := catalogs[res.ProviderID]
	if !ok {
		return invalidExtraction(res.ProviderID, fmt.Sprintf("provider %q is not cataloged", res.ProviderID))
	}
	if res.ItemID == "" {
		if cat.Channel(res.ChannelID) == nil {
			return invalidExtraction(res.ProviderID, fmt.Sprintf("channel %q is not in provider %q", res.ChannelID, res.ProviderID))
		}
		return nil
	}
	if !youtubeIDRE.MatchString(res.ItemID) {
		return invalidExtraction(res.ProviderID, fmt.Sprintf("item %q is not a valid YouTube ID", res.ItemID))
	}
	if res.ChannelID == reservedAdhocID {
		return nil
	}
	if !catalogContainsItem(cat, res.ItemID) {
		return invalidExtraction(res.ProviderID, fmt.Sprintf("item %q is not in provider %q", res.ItemID, res.ProviderID))
	}
	return nil
}

func catalogContainsItem(cat ProviderCatalog, itemID string) bool {
	for _, channel := range cat.Channels {
		for _, item := range channel.Items {
			if item.ID == itemID || item.SourceID == itemID {
				return true
			}
		}
	}
	return false
}

func invalidExtraction(providerID, message string) *StreamsError {
	if providerID != "" {
		message = fmt.Sprintf("streams provider %q: %s", providerID, message)
	}
	return &StreamsError{Kind: ErrKindInvalidExtraction, Message: message}
}

func normalizeParsedRuleHost(u *stdurl.URL) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(u.Hostname())), ".")
	if host == "" {
		return "", fmt.Errorf("host is required")
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", err
	}
	ascii = strings.TrimSuffix(strings.ToLower(ascii), ".")
	if ascii == "" {
		return "", fmt.Errorf("host is required")
	}
	port := u.Port()
	if port == "" || port == defaultPortForScheme(strings.ToLower(u.Scheme)) {
		return ascii, nil
	}
	return ascii + ":" + port, nil
}

func defaultPortForScheme(scheme string) string {
	switch scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}
