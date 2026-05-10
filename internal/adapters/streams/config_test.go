package streams

import (
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Enabled {
		t.Fatal("streams must default disabled")
	}
	if cfg.MaxManifestBytes != 1048576 {
		t.Fatalf("MaxManifestBytes = %d", cfg.MaxManifestBytes)
	}
	if cfg.YoutubeFormat == "" {
		t.Fatal("YoutubeFormat must have an SD-biased default")
	}
}

func TestConfigValidateRejectsBadRanges(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxManifestBytes = 0
	cfg.CatalogRequestTimeoutSeconds = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate succeeded with invalid byte/timeout ranges")
	}
}

func TestConfigValidateRejectsBlankManifestURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ManifestURL = " \t"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate succeeded with blank manifest_url")
	}
}

func TestDecodeConfigRemoteProviderAllowedHostsStringAndArray(t *testing.T) {
	rawString, metaString := decodeStreamsSection(t, `
remote_provider_allowed_hosts = "trusted.example, media.example"
`)
	cfgString, err := decodeConfig(rawString, metaString)
	if err != nil {
		t.Fatalf("decodeConfig string: %v", err)
	}
	if want := []string{"trusted.example", "media.example"}; !reflect.DeepEqual(cfgString.RemoteProviderAllowedHosts, want) {
		t.Fatalf("string RemoteProviderAllowedHosts = %#v, want %#v", cfgString.RemoteProviderAllowedHosts, want)
	}

	rawArray, metaArray := decodeStreamsSection(t, `
remote_provider_allowed_hosts = ["trusted.example", "media.example"]
`)
	cfgArray, err := decodeConfig(rawArray, metaArray)
	if err != nil {
		t.Fatalf("decodeConfig array: %v", err)
	}
	if want := []string{"trusted.example", "media.example"}; !reflect.DeepEqual(cfgArray.RemoteProviderAllowedHosts, want) {
		t.Fatalf("array RemoteProviderAllowedHosts = %#v, want %#v", cfgArray.RemoteProviderAllowedHosts, want)
	}
}

func TestScopeForField(t *testing.T) {
	cases := map[string]adapters.ApplyScope{
		"enabled":                                    adapters.ScopeHotSwap,
		"manifest_url":                               adapters.ScopeHotSwap,
		"manifest_refresh_hours":                     adapters.ScopeHotSwap,
		"catalog_refresh_hours":                      adapters.ScopeHotSwap,
		"max_manifest_bytes":                         adapters.ScopeHotSwap,
		"max_catalog_bytes":                          adapters.ScopeHotSwap,
		"max_items_per_channel":                      adapters.ScopeHotSwap,
		"max_consecutive_failures":                   adapters.ScopeHotSwap,
		"manifest_request_timeout_seconds":           adapters.ScopeHotSwap,
		"catalog_request_timeout_seconds":            adapters.ScopeHotSwap,
		"youtube_format":                             adapters.ScopeRestartCast,
		"allow_remote_manifest":                      adapters.ScopeHotSwap,
		"allow_cached_remote_manifest":               adapters.ScopeHotSwap,
		"allow_local_manifest_urls":                  adapters.ScopeHotSwap,
		"remote_provider_allowed_hosts":              adapters.ScopeHotSwap,
		"providers.mtv-rewind.disabled":              adapters.ScopeHotSwap,
		"providers.mtv-rewind.catalog_refresh_hours": adapters.ScopeHotSwap,
	}
	for key, want := range cases {
		got, ok := scopeForField(key)
		if !ok {
			t.Fatalf("scopeForField(%q) not found", key)
		}
		if got != want {
			t.Fatalf("scopeForField(%q) = %v, want %v", key, got, want)
		}
	}
	if _, ok := scopeForField("youtub_format"); ok {
		t.Fatal("unknown field should not receive an implicit scope")
	}
}

func decodeStreamsSection(t *testing.T, body string) (toml.Primitive, toml.MetaData) {
	t.Helper()
	raw := "[adapters.streams]\n" + body
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(raw, &envelope)
	if err != nil {
		t.Fatalf("toml.Decode: %v", err)
	}
	return envelope.Adapters["streams"], meta
}

func TestNormalizeConfigHost(t *testing.T) {
	cases := map[string]string{
		"Example.COM.":                 "example.com",
		"https://Example.COM:443/path": "example.com",
		"bücher.example":               "xn--bcher-kva.example",
	}
	for in, want := range cases {
		got, err := normalizeConfigHost(in)
		if err != nil {
			t.Fatalf("normalizeConfigHost(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("normalizeConfigHost(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{
		"*.example.com",
		"http://user@example.com",
		"127.0.0.1",
		"https://example.com:444/path",
		"http://example.com:8080",
	} {
		if got, err := normalizeConfigHost(bad); err == nil {
			t.Fatalf("normalizeConfigHost(%q) = %q, want error", bad, got)
		}
	}
}
