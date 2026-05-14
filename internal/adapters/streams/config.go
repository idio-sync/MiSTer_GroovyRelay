package streams

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
	"golang.org/x/net/idna"
)

type Config struct {
	Enabled                       bool                      `toml:"enabled"`
	ManifestURL                   string                    `toml:"manifest_url"`
	ManifestRefreshHours          int                       `toml:"manifest_refresh_hours"`
	CatalogRefreshHours           int                       `toml:"catalog_refresh_hours"`
	MaxManifestBytes              int64                     `toml:"max_manifest_bytes"`
	MaxCatalogBytes               int64                     `toml:"max_catalog_bytes"`
	MaxItemsPerChannel            int                       `toml:"max_items_per_channel"`
	MaxConsecutiveFailures        int                       `toml:"max_consecutive_failures"`
	ManifestRequestTimeoutSeconds int                       `toml:"manifest_request_timeout_seconds"`
	CatalogRequestTimeoutSeconds  int                       `toml:"catalog_request_timeout_seconds"`
	YoutubeFormat                 string                    `toml:"youtube_format"`
	AllowRemoteManifest           bool                      `toml:"allow_remote_manifest"`
	AllowCachedRemoteManifest     bool                      `toml:"allow_cached_remote_manifest"`
	AllowLocalManifestURLs        bool                      `toml:"allow_local_manifest_urls"`
	RemoteProviderAllowedHosts    []string                  `toml:"remote_provider_allowed_hosts"`
	Providers                     map[string]ProviderConfig `toml:"providers"`
}

type ProviderConfig struct {
	Disabled            bool                     `toml:"disabled"`
	CatalogRefreshHours int                      `toml:"catalog_refresh_hours"`
	HLSBufferDisabled   bool                     `toml:"hls_buffer_disabled"`
	Channels            map[string]ChannelConfig `toml:"channels"`
}

type ChannelConfig struct {
	HLSBufferDisabled bool `toml:"hls_buffer_disabled"`
}

type configWire struct {
	Enabled                       bool                      `toml:"enabled"`
	ManifestURL                   string                    `toml:"manifest_url"`
	ManifestRefreshHours          int                       `toml:"manifest_refresh_hours"`
	CatalogRefreshHours           int                       `toml:"catalog_refresh_hours"`
	MaxManifestBytes              int64                     `toml:"max_manifest_bytes"`
	MaxCatalogBytes               int64                     `toml:"max_catalog_bytes"`
	MaxItemsPerChannel            int                       `toml:"max_items_per_channel"`
	MaxConsecutiveFailures        int                       `toml:"max_consecutive_failures"`
	ManifestRequestTimeoutSeconds int                       `toml:"manifest_request_timeout_seconds"`
	CatalogRequestTimeoutSeconds  int                       `toml:"catalog_request_timeout_seconds"`
	YoutubeFormat                 string                    `toml:"youtube_format"`
	AllowRemoteManifest           bool                      `toml:"allow_remote_manifest"`
	AllowCachedRemoteManifest     bool                      `toml:"allow_cached_remote_manifest"`
	AllowLocalManifestURLs        bool                      `toml:"allow_local_manifest_urls"`
	RemoteProviderAllowedHosts    hostList                  `toml:"remote_provider_allowed_hosts"`
	Providers                     map[string]ProviderConfig `toml:"providers"`
}

type hostList []string

func (h *hostList) UnmarshalTOML(v any) error {
	switch value := v.(type) {
	case string:
		*h = splitHostList(value)
		return nil
	case []string:
		*h = trimHostList(value)
		return nil
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("entries must be strings")
			}
			out = append(out, s)
		}
		*h = trimHostList(out)
		return nil
	default:
		return fmt.Errorf("must be a string or string array")
	}
}

func DefaultConfig() Config {
	return Config{
		Enabled:                       false,
		ManifestURL:                   "https://raw.githubusercontent.com/idio-sync/MiSTer_GroovyRelay/main/docs/streams/providers.json",
		ManifestRefreshHours:          24,
		CatalogRefreshHours:           12,
		MaxManifestBytes:              1048576,
		MaxCatalogBytes:               10485760,
		MaxItemsPerChannel:            5000,
		MaxConsecutiveFailures:        25,
		ManifestRequestTimeoutSeconds: 10,
		CatalogRequestTimeoutSeconds:  20,
		YoutubeFormat:                 "b[height<=480]/bv*[height<=480]+ba/bv*+ba/b",
		AllowRemoteManifest:           true,
		AllowCachedRemoteManifest:     false,
		AllowLocalManifestURLs:        false,
		RemoteProviderAllowedHosts:    nil,
		Providers:                     map[string]ProviderConfig{},
	}
}

func (c *Config) Validate() error {
	var errs adapters.FieldErrors
	if strings.TrimSpace(c.ManifestURL) == "" {
		errs = append(errs, adapters.FieldError{Key: "manifest_url", Msg: "must not be empty"})
	}
	if c.ManifestRefreshHours < 1 || c.ManifestRefreshHours > 168 {
		errs = append(errs, adapters.FieldError{Key: "manifest_refresh_hours", Msg: "must be in [1, 168]"})
	}
	if c.CatalogRefreshHours < 1 || c.CatalogRefreshHours > 168 {
		errs = append(errs, adapters.FieldError{Key: "catalog_refresh_hours", Msg: "must be in [1, 168]"})
	}
	if c.MaxManifestBytes < 1024 || c.MaxManifestBytes > 8*1024*1024 {
		errs = append(errs, adapters.FieldError{Key: "max_manifest_bytes", Msg: "must be in [1024, 8388608]"})
	}
	if c.MaxCatalogBytes < 1024 || c.MaxCatalogBytes > 64*1024*1024 {
		errs = append(errs, adapters.FieldError{Key: "max_catalog_bytes", Msg: "must be in [1024, 67108864]"})
	}
	if c.MaxItemsPerChannel < 1 || c.MaxItemsPerChannel > 50000 {
		errs = append(errs, adapters.FieldError{Key: "max_items_per_channel", Msg: "must be in [1, 50000]"})
	}
	if c.MaxConsecutiveFailures < 1 || c.MaxConsecutiveFailures > 100 {
		errs = append(errs, adapters.FieldError{Key: "max_consecutive_failures", Msg: "must be in [1, 100]"})
	}
	if c.ManifestRequestTimeoutSeconds < 1 || c.ManifestRequestTimeoutSeconds > 60 {
		errs = append(errs, adapters.FieldError{Key: "manifest_request_timeout_seconds", Msg: "must be in [1, 60]"})
	}
	if c.CatalogRequestTimeoutSeconds < 1 || c.CatalogRequestTimeoutSeconds > 120 {
		errs = append(errs, adapters.FieldError{Key: "catalog_request_timeout_seconds", Msg: "must be in [1, 120]"})
	}
	if strings.TrimSpace(c.YoutubeFormat) == "" {
		errs = append(errs, adapters.FieldError{Key: "youtube_format", Msg: "must not be empty"})
	}
	if _, err := normalizeHostSet(c.RemoteProviderAllowedHosts); err != nil {
		errs = append(errs, adapters.FieldError{
			Key: "remote_provider_allowed_hosts",
			Msg: err.Error(),
		})
	}
	for id, provider := range c.Providers {
		if provider.CatalogRefreshHours != 0 &&
			(provider.CatalogRefreshHours < 1 || provider.CatalogRefreshHours > 168) {
			errs = append(errs, adapters.FieldError{
				Key: fmt.Sprintf("providers.%s.catalog_refresh_hours", id),
				Msg: "must be in [1, 168]",
			})
		}
		for channelID := range provider.Channels {
			if strings.TrimSpace(channelID) == "" {
				errs = append(errs, adapters.FieldError{
					Key: fmt.Sprintf("providers.%s.channels", id),
					Msg: "channel id must not be empty",
				})
			}
		}
	}
	return errs.Err()
}

func configToWire(cfg Config) configWire {
	return configWire{
		Enabled:                       cfg.Enabled,
		ManifestURL:                   cfg.ManifestURL,
		ManifestRefreshHours:          cfg.ManifestRefreshHours,
		CatalogRefreshHours:           cfg.CatalogRefreshHours,
		MaxManifestBytes:              cfg.MaxManifestBytes,
		MaxCatalogBytes:               cfg.MaxCatalogBytes,
		MaxItemsPerChannel:            cfg.MaxItemsPerChannel,
		MaxConsecutiveFailures:        cfg.MaxConsecutiveFailures,
		ManifestRequestTimeoutSeconds: cfg.ManifestRequestTimeoutSeconds,
		CatalogRequestTimeoutSeconds:  cfg.CatalogRequestTimeoutSeconds,
		YoutubeFormat:                 cfg.YoutubeFormat,
		AllowRemoteManifest:           cfg.AllowRemoteManifest,
		AllowCachedRemoteManifest:     cfg.AllowCachedRemoteManifest,
		AllowLocalManifestURLs:        cfg.AllowLocalManifestURLs,
		RemoteProviderAllowedHosts:    hostList(cfg.RemoteProviderAllowedHosts),
		Providers:                     cfg.Providers,
	}
}

func wireToConfig(w configWire) Config {
	providers := w.Providers
	if providers == nil {
		providers = map[string]ProviderConfig{}
	}
	return Config{
		Enabled:                       w.Enabled,
		ManifestURL:                   w.ManifestURL,
		ManifestRefreshHours:          w.ManifestRefreshHours,
		CatalogRefreshHours:           w.CatalogRefreshHours,
		MaxManifestBytes:              w.MaxManifestBytes,
		MaxCatalogBytes:               w.MaxCatalogBytes,
		MaxItemsPerChannel:            w.MaxItemsPerChannel,
		MaxConsecutiveFailures:        w.MaxConsecutiveFailures,
		ManifestRequestTimeoutSeconds: w.ManifestRequestTimeoutSeconds,
		CatalogRequestTimeoutSeconds:  w.CatalogRequestTimeoutSeconds,
		YoutubeFormat:                 w.YoutubeFormat,
		AllowRemoteManifest:           w.AllowRemoteManifest,
		AllowCachedRemoteManifest:     w.AllowCachedRemoteManifest,
		AllowLocalManifestURLs:        w.AllowLocalManifestURLs,
		RemoteProviderAllowedHosts:    []string(w.RemoteProviderAllowedHosts),
		Providers:                     providers,
	}
}

func splitHostList(in string) []string {
	return trimHostList(strings.FieldsFunc(in, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}))
}

func trimHostList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func formatHostList(in []string) string {
	return strings.Join(trimHostList(in), ", ")
}

var fieldScopes = map[string]adapters.ApplyScope{
	"enabled":                          adapters.ScopeHotSwap,
	"manifest_url":                     adapters.ScopeHotSwap,
	"manifest_refresh_hours":           adapters.ScopeHotSwap,
	"catalog_refresh_hours":            adapters.ScopeHotSwap,
	"max_manifest_bytes":               adapters.ScopeHotSwap,
	"max_catalog_bytes":                adapters.ScopeHotSwap,
	"max_items_per_channel":            adapters.ScopeHotSwap,
	"max_consecutive_failures":         adapters.ScopeHotSwap,
	"manifest_request_timeout_seconds": adapters.ScopeHotSwap,
	"catalog_request_timeout_seconds":  adapters.ScopeHotSwap,
	"youtube_format":                   adapters.ScopeRestartCast,
	"allow_remote_manifest":            adapters.ScopeHotSwap,
	"allow_cached_remote_manifest":     adapters.ScopeHotSwap,
	"allow_local_manifest_urls":        adapters.ScopeHotSwap,
	"remote_provider_allowed_hosts":    adapters.ScopeHotSwap,
}

func scopeForField(key string) (adapters.ApplyScope, bool) {
	if scope, ok := fieldScopes[key]; ok {
		return scope, true
	}
	if strings.HasPrefix(key, "providers.") &&
		(strings.HasSuffix(key, ".disabled") ||
			strings.HasSuffix(key, ".catalog_refresh_hours") ||
			strings.HasSuffix(key, ".hls_buffer_disabled")) {
		return adapters.ScopeHotSwap, true
	}
	if strings.HasPrefix(key, "providers.") &&
		strings.Contains(key, ".channels.") &&
		strings.HasSuffix(key, ".hls_buffer_disabled") {
		return adapters.ScopeHotSwap, true
	}
	return 0, false
}

func normalizeHostSet(in []string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	for _, h := range in {
		normalized, err := normalizeConfigHost(h)
		if err != nil {
			return nil, fmt.Errorf("invalid hostname %q: %w", h, err)
		}
		if normalized != "" {
			out[normalized] = struct{}{}
		}
	}
	return out, nil
}

func normalizeConfigHost(in string) (string, error) {
	raw := strings.TrimSpace(in)
	if raw == "" {
		return "", fmt.Errorf("host must not be empty")
	}

	host := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		if u.User != nil {
			return "", fmt.Errorf("userinfo is not allowed")
		}
		host = u.Hostname()
		if host == "" {
			return "", fmt.Errorf("host must not be empty")
		}
		if port := u.Port(); port != "" {
			switch {
			case u.Scheme == "https" && port == "443":
			case u.Scheme == "http" && port == "80":
			default:
				return "", fmt.Errorf("non-default ports are not allowed")
			}
		}
	} else if strings.ContainsAny(raw, "/?#@") {
		return "", fmt.Errorf("host must not contain URL syntax")
	}

	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return "", fmt.Errorf("host must not be empty")
	}
	if strings.Contains(host, "*") {
		return "", fmt.Errorf("wildcard hosts are not allowed")
	}
	if strings.Contains(host, ":") {
		return "", fmt.Errorf("host must not include a port")
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", fmt.Errorf("IP literals are not allowed")
	}

	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", err
	}
	ascii = strings.TrimSuffix(strings.ToLower(ascii), ".")
	if ascii == "" {
		return "", fmt.Errorf("host must not be empty")
	}
	if _, err := netip.ParseAddr(ascii); err == nil {
		return "", fmt.Errorf("IP literals are not allowed")
	}
	return ascii, nil
}
