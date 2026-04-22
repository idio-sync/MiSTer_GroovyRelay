package config

import (
	"strings"
	"testing"
)

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
