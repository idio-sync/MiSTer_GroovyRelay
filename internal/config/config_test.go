package config

import (
	"fmt"
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

func TestValidate_RejectsBadAudioSampleRate(t *testing.T) {
	for _, rate := range []int{0, 16000, 96000} {
		t.Run(fmt.Sprintf("%d", rate), func(t *testing.T) {
			c := defaults()
			c.AudioSampleRate = rate
			err := c.Validate()
			if err == nil {
				t.Fatalf("audio_sample_rate=%d: expected validation error, got nil", rate)
			}
			if !strings.Contains(err.Error(), "audio_sample_rate") {
				t.Errorf("audio_sample_rate=%d: error %q should mention audio_sample_rate", rate, err)
			}
		})
	}
}

func TestValidate_AcceptsSupportedAudioSampleRates(t *testing.T) {
	for _, rate := range []int{22050, 44100, 48000} {
		t.Run(fmt.Sprintf("%d", rate), func(t *testing.T) {
			c := defaults()
			c.AudioSampleRate = rate
			if err := c.Validate(); err != nil {
				t.Errorf("audio_sample_rate=%d: expected OK, got %v", rate, err)
			}
		})
	}
}

func TestValidate_RejectsBadAudioChannels(t *testing.T) {
	for _, chans := range []int{0, 3, 99} {
		t.Run(fmt.Sprintf("%d", chans), func(t *testing.T) {
			c := defaults()
			c.AudioChannels = chans
			err := c.Validate()
			if err == nil {
				t.Fatalf("audio_channels=%d: expected validation error, got nil", chans)
			}
			if !strings.Contains(err.Error(), "audio_channels") {
				t.Errorf("audio_channels=%d: error %q should mention audio_channels", chans, err)
			}
		})
	}
}

func TestValidate_AcceptsSupportedAudioChannels(t *testing.T) {
	for _, chans := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d", chans), func(t *testing.T) {
			c := defaults()
			c.AudioChannels = chans
			if err := c.Validate(); err != nil {
				t.Errorf("audio_channels=%d: expected OK, got %v", chans, err)
			}
		})
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

// TestSectioned_RoundTripSSHFields confirms the new SSH credential
// fields decode + re-encode through BurntSushi/toml without loss.
// Catches a forgotten struct tag or a missed migration helper if
// either drifts in a future refactor.
func TestSectioned_RoundTripSSHFields(t *testing.T) {
	const input = `
[bridge]
[bridge.mister]
host = "192.168.1.42"
port = 32100
source_port = 32101
ssh_user = "alice"
ssh_password = "hunter2"
`
	s, _, err := loadSectionedFromBytes([]byte(input))
	if err != nil {
		t.Fatalf("loadSectionedFromBytes: %v", err)
	}
	if s.Bridge.MiSTer.SSHUser != "alice" {
		t.Errorf("SSHUser = %q, want alice", s.Bridge.MiSTer.SSHUser)
	}
	if s.Bridge.MiSTer.SSHPassword != "hunter2" {
		t.Errorf("SSHPassword = %q, want hunter2", s.Bridge.MiSTer.SSHPassword)
	}
}

// TestDefaultBridge_SSHUserIsRoot pins the default user so a future
// refactor of defaultBridge can't silently change it.
func TestDefaultBridge_SSHUserIsRoot(t *testing.T) {
	b := defaultBridge()
	if b.MiSTer.SSHUser != "root" {
		t.Errorf("default SSHUser = %q, want root", b.MiSTer.SSHUser)
	}
	if b.MiSTer.SSHPassword != "" {
		t.Errorf("default SSHPassword = %q, want empty", b.MiSTer.SSHPassword)
	}
}
