package streams

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

const (
	reservedAdhocID      = "adhoc"
	maxManifestProviders = 128
	maxManifestGroups    = 256
	maxManifestChannels  = 1024
	maxManifestURLRules  = 128
)

var manifestIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
var manifestValidationResolver hostResolver = net.DefaultResolver

type remoteDataURLValidator func(context.Context, string, Config) error

func validateManifest(ctx context.Context, m Manifest, cfg Config) error {
	return validateManifestWithURLValidator(ctx, m, cfg, validateRemoteDataURL)
}

func validateCachedManifest(ctx context.Context, m Manifest, cfg Config) error {
	return validateManifestWithURLValidator(ctx, m, cfg, validateRemoteDataURLSyntax)
}

func validateManifestWithURLValidator(ctx context.Context, m Manifest, cfg Config, validateURL remoteDataURLValidator) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if m.Version != 1 {
		return fmt.Errorf("unsupported manifest version %d", m.Version)
	}
	if len(m.Providers) > maxManifestProviders {
		return fmt.Errorf("manifest has %d providers, max %d", len(m.Providers), maxManifestProviders)
	}

	providerIDs := map[string]struct{}{}
	for _, provider := range m.Providers {
		if isUnsupportedProviderType(provider.Type) {
			continue
		}
		if err := validateProviderDefinition(ctx, provider, cfg, validateURL); err != nil {
			return err
		}
		if _, ok := providerIDs[provider.ID]; ok {
			return fmt.Errorf("duplicate provider id %q", provider.ID)
		}
		providerIDs[provider.ID] = struct{}{}
	}
	return nil
}

func isUnsupportedProviderType(providerType string) bool {
	providerType = strings.TrimSpace(providerType)
	if providerType == "" {
		return false
	}
	_, ok := remoteProviderFactories()[providerType]
	return !ok
}

func remoteProviderFactories() map[string]ProviderFactory {
	return map[string]ProviderFactory{
		youtubeChannelJSONProviderType: func(ProviderDefinition) (Provider, error) { return struct{}{}, nil },
	}
}

func validateProviderDefinition(ctx context.Context, provider ProviderDefinition, cfg Config, validateURL remoteDataURLValidator) error {
	if err := validateManifestID("provider id", provider.ID); err != nil {
		return err
	}
	if provider.ID == reservedAdhocID {
		return fmt.Errorf("provider id %q is reserved", provider.ID)
	}
	if strings.TrimSpace(provider.Type) == "" {
		return fmt.Errorf("provider %q type is required", provider.ID)
	}
	if strings.TrimSpace(provider.DisplayName) == "" {
		return fmt.Errorf("provider %q display_name is required", provider.ID)
	}
	if len(provider.Groups) > maxManifestGroups {
		return fmt.Errorf("provider %q has %d groups, max %d", provider.ID, len(provider.Groups), maxManifestGroups)
	}
	if len(provider.Channels) > maxManifestChannels {
		return fmt.Errorf("provider %q has %d channels, max %d", provider.ID, len(provider.Channels), maxManifestChannels)
	}
	if len(provider.URLRules) > maxManifestURLRules {
		return fmt.Errorf("provider %q has %d url_rules, max %d", provider.ID, len(provider.URLRules), maxManifestURLRules)
	}
	if strings.TrimSpace(provider.PlaylistURL) == "" {
		return fmt.Errorf("provider %q playlist_url is required", provider.ID)
	}
	if err := validateURL(ctx, provider.PlaylistURL, cfg); err != nil {
		return fmt.Errorf("provider %q playlist_url: %w", provider.ID, err)
	}
	if strings.TrimSpace(provider.BaseURL) != "" {
		if err := validateURL(ctx, provider.BaseURL, cfg); err != nil {
			return fmt.Errorf("provider %q base_url: %w", provider.ID, err)
		}
	}
	if strings.TrimSpace(provider.DefaultChannel) == "" {
		return fmt.Errorf("provider %q default_channel is required", provider.ID)
	}
	if !validPlayMode(provider.DefaultPlayMode) {
		return fmt.Errorf("provider %q default_play_mode %q is unsupported", provider.ID, provider.DefaultPlayMode)
	}
	if len(provider.URLRules) == 0 {
		return fmt.Errorf("provider %q must define at least one url_rule", provider.ID)
	}

	groupIDs := map[string]struct{}{}
	for _, group := range provider.Groups {
		if err := validateManifestID("group id", group.ID); err != nil {
			return fmt.Errorf("provider %q: %w", provider.ID, err)
		}
		if _, ok := groupIDs[group.ID]; ok {
			return fmt.Errorf("provider %q duplicate group id %q", provider.ID, group.ID)
		}
		groupIDs[group.ID] = struct{}{}
	}

	channelIDs := map[string]struct{}{}
	for _, channel := range provider.Channels {
		if err := validateManifestID("channel id", channel.ID); err != nil {
			return fmt.Errorf("provider %q: %w", provider.ID, err)
		}
		if channel.ID == reservedAdhocID {
			return fmt.Errorf("provider %q channel id %q is reserved", provider.ID, channel.ID)
		}
		if _, ok := channelIDs[channel.ID]; ok {
			return fmt.Errorf("provider %q duplicate channel id %q", provider.ID, channel.ID)
		}
		if strings.TrimSpace(channel.Name) == "" {
			return fmt.Errorf("provider %q channel %q name is required", provider.ID, channel.ID)
		}
		if channel.PlayMode != "" && !validPlayMode(channel.PlayMode) {
			return fmt.Errorf("provider %q channel %q play_mode %q is unsupported", provider.ID, channel.ID, channel.PlayMode)
		}
		channelIDs[channel.ID] = struct{}{}
	}

	ruleIDs := map[string]struct{}{}
	for _, rule := range provider.URLRules {
		if err := validateURLRule(rule, cfg); err != nil {
			return fmt.Errorf("provider %q: %w", provider.ID, err)
		}
		if _, ok := ruleIDs[rule.ID]; ok {
			return fmt.Errorf("provider %q duplicate url_rule id %q", provider.ID, rule.ID)
		}
		ruleIDs[rule.ID] = struct{}{}
	}
	return nil
}

func validateRemoteDataURLSyntax(ctx context.Context, raw string, cfg Config) error {
	_ = ctx
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.User != nil {
		return fmt.Errorf("userinfo is not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("host is required")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && !(cfg.AllowLocalManifestURLs && scheme == "http") {
		return fmt.Errorf("scheme %q is not allowed", scheme)
	}
	if _, err := netip.ParseAddr(host); err == nil {
		if !cfg.AllowLocalManifestURLs {
			return fmt.Errorf("IP literal hosts are not allowed")
		}
		return nil
	}
	if _, err := normalizeConfigHost(host); err != nil {
		return err
	}
	return nil
}

func validateManifestID(label, id string) error {
	if !manifestIDPattern.MatchString(id) {
		return fmt.Errorf("%s %q must match %s", label, id, manifestIDPattern.String())
	}
	return nil
}

func validateURLRule(rule URLRule, cfg Config) error {
	if err := validateManifestID("url_rule id", rule.ID); err != nil {
		return err
	}
	if len(rule.Schemes) == 0 {
		return fmt.Errorf("url_rule %q schemes are required", rule.ID)
	}
	for _, scheme := range rule.Schemes {
		scheme = strings.ToLower(strings.TrimSpace(scheme))
		if scheme == "" {
			return fmt.Errorf("url_rule %q has blank scheme", rule.ID)
		}
		if scheme != "https" && !(cfg.AllowLocalManifestURLs && scheme == "http") {
			return fmt.Errorf("url_rule %q scheme %q is not allowed", rule.ID, scheme)
		}
	}
	if len(rule.Hosts) == 0 {
		return fmt.Errorf("url_rule %q hosts are required", rule.ID)
	}
	for _, host := range rule.Hosts {
		if _, err := normalizeConfigHost(host); err != nil {
			return fmt.Errorf("url_rule %q host %q: %w", rule.ID, host, err)
		}
	}
	if (rule.Path == "") == (rule.PathPrefix == "") {
		return fmt.Errorf("url_rule %q must set exactly one of path or path_prefix", rule.ID)
	}
	if rule.Target != "channel" && rule.Target != "item" {
		return fmt.Errorf("url_rule %q target %q is unsupported", rule.ID, rule.Target)
	}
	if rule.Path != "" && strings.TrimSpace(rule.QueryParam) == "" {
		return fmt.Errorf("url_rule %q query_param is required for path rules", rule.ID)
	}
	return nil
}

func validateRemoteDataURL(ctx context.Context, raw string, cfg Config) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.User != nil {
		return fmt.Errorf("userinfo is not allowed")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("host is required")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && !(cfg.AllowLocalManifestURLs && scheme == "http") {
		return fmt.Errorf("scheme %q is not allowed", scheme)
	}
	if _, err := resolveValidatedIP(ctx, manifestValidationResolver, u.Hostname(), cfg.AllowLocalManifestURLs); err != nil {
		return err
	}
	return nil
}

func validPlayMode(mode PlayMode) bool {
	switch mode {
	case PlaySequential, PlayShuffle, PlayFirstThenShuffle:
		return true
	default:
		return false
	}
}

func mergeManifests(cfg Config, bundled Manifest, cached *Manifest, remote *Manifest, factories map[string]ProviderFactory) Manifest {
	out := Manifest{Version: 1}
	index := map[string]int{}
	bundledTypes := map[string]string{}

	addProvider := func(provider ProviderDefinition, remoteOverlay bool) {
		if provider.ID == "" || provider.ID == reservedAdhocID {
			return
		}
		if remoteOverlay {
			if factories != nil {
				if _, ok := factories[provider.Type]; !ok {
					return
				}
			}
			if bundledTypes[provider.ID] == directStreamsProviderType {
				return
			}
			if bundledTypes[provider.ID] != "" && bundledTypes[provider.ID] != provider.Type {
				return
			}
			if provider.Type == directStreamsProviderType {
				return
			}
		}
		if existingIndex, exists := index[provider.ID]; exists {
			out.Providers[existingIndex] = provider
			return
		}
		out.Providers = append(out.Providers, provider)
		index[provider.ID] = len(out.Providers) - 1
	}

	for _, provider := range bundled.Providers {
		bundledTypes[provider.ID] = provider.Type
		addProvider(provider, false)
	}
	if cached != nil && (cfg.AllowRemoteManifest || cfg.AllowCachedRemoteManifest) {
		for _, provider := range cached.Providers {
			addProvider(provider, true)
		}
	}
	if remote != nil && cfg.AllowRemoteManifest {
		for _, provider := range remote.Providers {
			addProvider(provider, true)
		}
	}

	if len(cfg.Providers) != 0 {
		filtered := out.Providers[:0]
		for _, provider := range out.Providers {
			if override, ok := cfg.Providers[provider.ID]; ok && override.Disabled {
				delete(index, provider.ID)
				continue
			}
			filtered = append(filtered, provider)
		}
		out.Providers = filtered
	}
	return out
}
