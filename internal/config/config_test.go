package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DeviceName != "MiSTer" {
		t.Errorf("DeviceName default = %q, want %q", cfg.DeviceName, "MiSTer")
	}
	if cfg.MisterPort != 32100 {
		t.Errorf("MisterPort default = %d, want 32100", cfg.MisterPort)
	}
	if !cfg.LZ4Enabled {
		t.Error("LZ4Enabled default should be true")
	}
	if cfg.InterlaceFieldOrder != "tff" {
		t.Errorf("InterlaceFieldOrder default = %q, want tff", cfg.InterlaceFieldOrder)
	}
	if cfg.AspectMode != "auto" {
		t.Errorf("AspectMode default = %q, want auto", cfg.AspectMode)
	}
}

func TestLoadOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
device_name = "LivingRoomMiSTer"
mister_host = "192.168.1.42"
lz4_enabled = false
interlace_field_order = "bff"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DeviceName != "LivingRoomMiSTer" {
		t.Errorf("DeviceName = %q, want LivingRoomMiSTer", cfg.DeviceName)
	}
	if cfg.MisterHost != "192.168.1.42" {
		t.Errorf("MisterHost = %q", cfg.MisterHost)
	}
	if cfg.LZ4Enabled {
		t.Error("LZ4Enabled should be false")
	}
	if cfg.InterlaceFieldOrder != "bff" {
		t.Errorf("InterlaceFieldOrder = %q, want bff", cfg.InterlaceFieldOrder)
	}
}

func TestValidateBadFieldOrder(t *testing.T) {
	cfg := &Config{InterlaceFieldOrder: "diagonal", AspectMode: "auto"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for bad field order")
	}
}

func TestValidateBadAspectMode(t *testing.T) {
	cfg := &Config{InterlaceFieldOrder: "tff", AspectMode: "stretch"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for bad aspect mode")
	}
}

func TestValidate_RejectsNonRGB888(t *testing.T) {
	for _, mode := range []string{"rgba8888", "rgb565", "rgb16"} {
		c := defaults()
		c.RGBMode = mode
		err := c.Validate()
		if err == nil {
			t.Errorf("rgb_mode=%q: expected validation error, got nil", mode)
			continue
		}
		if !strings.Contains(err.Error(), "rgb888") {
			t.Errorf("rgb_mode=%q: error %q should mention 'rgb888'", mode, err)
		}
	}
}

func TestValidate_AcceptsRGB888(t *testing.T) {
	c := defaults()
	c.RGBMode = "rgb888"
	if err := c.Validate(); err != nil {
		t.Errorf("rgb_mode=rgb888: expected OK, got %v", err)
	}
}

func TestLoad_HostIPRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := []byte(`mister_host = "192.168.1.50"` + "\n" + `host_ip = "192.168.1.20"` + "\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostIP != "192.168.1.20" {
		t.Errorf("host_ip = %q, want %q", cfg.HostIP, "192.168.1.20")
	}
}

func TestLoad_HostIPDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`mister_host = "192.168.1.50"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostIP != "" {
		t.Errorf("host_ip default = %q, want empty", cfg.HostIP)
	}
}

func TestValidate_RejectsMalformedHostIP(t *testing.T) {
	bad := []string{
		"not-an-ip",
		"192.168.1.20/24",     // CIDR typo
		"http://192.168.1.20", // URL typo
		"192.168.1",           // truncated
		"256.0.0.1",           // invalid octet
		"192.168.1.20:32500",  // with port
	}
	for _, v := range bad {
		t.Run(v, func(t *testing.T) {
			c := defaults()
			c.HostIP = v
			err := c.Validate()
			if err == nil {
				t.Errorf("host_ip=%q: expected validation error, got nil", v)
				return
			}
			if !strings.Contains(err.Error(), "host_ip") {
				t.Errorf("error should mention host_ip: %v", err)
			}
		})
	}
}

func TestValidate_AcceptsValidHostIP(t *testing.T) {
	for _, v := range []string{"192.168.1.20", "10.0.0.1", "::1", "fe80::1"} {
		t.Run(v, func(t *testing.T) {
			c := defaults()
			c.HostIP = v
			if err := c.Validate(); err != nil {
				t.Errorf("host_ip=%q: expected OK, got %v", v, err)
			}
		})
	}
}

func TestValidate_AcceptsEmptyHostIP(t *testing.T) {
	c := defaults()
	c.HostIP = ""
	if err := c.Validate(); err != nil {
		t.Errorf("empty host_ip (auto-detect fallback): expected OK, got %v", err)
	}
}
