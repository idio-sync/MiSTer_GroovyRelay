package plex

import (
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestConfig_Defaults(t *testing.T) {
	c := DefaultConfig()
	if !c.Enabled {
		t.Error("DefaultConfig.Enabled should be true")
	}
	if c.DeviceName != "MiSTer" {
		t.Errorf("DeviceName = %q, want MiSTer", c.DeviceName)
	}
	if c.ProfileName != "Plex Home Theater" {
		t.Errorf("ProfileName = %q", c.ProfileName)
	}
	if c.MaxVideoBitrateKbps != 1500 {
		t.Errorf("MaxVideoBitrateKbps = %d, want 1500", c.MaxVideoBitrateKbps)
	}
}

func TestConfig_Validate_HappyPath(t *testing.T) {
	c := DefaultConfig()
	if err := c.Validate(); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestConfig_Validate_RequiresDeviceName(t *testing.T) {
	c := DefaultConfig()
	c.DeviceName = ""
	err := c.Validate()
	if err == nil {
		t.Fatal("want error")
	}
	fe, ok := err.(adapters.FieldErrors)
	if !ok || len(fe) == 0 || fe[0].Key != "device_name" {
		t.Errorf("want device_name FieldError, got %v", err)
	}
}

func TestConfig_Validate_ServerURLMustBeURL(t *testing.T) {
	c := DefaultConfig()
	c.ServerURL = "not a url"
	err := c.Validate()
	if err == nil {
		t.Fatal("want error")
	}
	fe, _ := err.(adapters.FieldErrors)
	for _, e := range fe {
		if e.Key == "server_url" {
			return
		}
	}
	t.Errorf("want server_url error: %v", fe)
}

func TestConfig_Validate_AcceptsEmptyServerURL(t *testing.T) {
	c := DefaultConfig()
	c.ServerURL = ""
	if err := c.Validate(); err != nil {
		t.Errorf("empty server_url should auto-discover: %v", err)
	}
}

func TestConfig_Validate_BitrateBounds(t *testing.T) {
	cases := []struct {
		name    string
		val     int
		wantErr bool
	}{
		{"zero rejected", 0, true},
		{"below min rejected", 50, true},
		{"min accepted", 100, false},
		{"typical accepted", 5000, false},
		{"max accepted", 50000, false},
		{"above max rejected", 50001, true},
		{"negative rejected", -100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			c.MaxVideoBitrateKbps = tc.val
			err := c.Validate()
			if tc.wantErr && err == nil {
				t.Errorf("MaxVideoBitrateKbps=%d: want error, got nil", tc.val)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("MaxVideoBitrateKbps=%d: unexpected error %v", tc.val, err)
			}
			if tc.wantErr && err != nil {
				fe, ok := err.(adapters.FieldErrors)
				if !ok {
					t.Fatalf("expected FieldErrors, got %T", err)
				}
				found := false
				for _, e := range fe {
					if e.Key == "max_video_bitrate_kbps" {
						found = true
					}
				}
				if !found {
					t.Errorf("expected max_video_bitrate_kbps FieldError, got %v", fe)
				}
			}
		})
	}
}
