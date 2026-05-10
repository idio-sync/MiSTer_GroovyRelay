package dlna

import (
	"errors"
	"strings"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled {
		t.Error("DefaultConfig.Enabled = true, want false (DLNA must opt in — exposes unauthenticated LAN control)")
	}
	if c.DeviceName != "MiSTer" {
		t.Errorf("DefaultConfig.DeviceName = %q, want %q", c.DeviceName, "MiSTer")
	}
	if c.AutoplayOnSetURI {
		t.Error("DefaultConfig.AutoplayOnSetURI = true, want false")
	}
	if c.AllowPublicSourceURLs {
		t.Error("DefaultConfig.AllowPublicSourceURLs = true, want false (SSRF risk)")
	}
}

func TestValidate_Success(t *testing.T) {
	c := DefaultConfig()
	if err := c.Validate(); err != nil {
		t.Errorf("DefaultConfig().Validate() = %v, want nil", err)
	}
}

func TestValidate_DeviceNameEmpty(t *testing.T) {
	for _, name := range []string{"", " ", "\t", "   \n\t "} {
		c := DefaultConfig()
		c.DeviceName = name
		err := c.Validate()
		if err == nil {
			t.Fatalf("DeviceName=%q: want validation error, got nil", name)
		}
		var errs adapters.FieldErrors
		if !errors.As(err, &errs) {
			t.Fatalf("DeviceName=%q: error type = %T, want adapters.FieldErrors", name, err)
		}
		found := false
		for _, fe := range errs {
			if fe.Key == "device_name" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DeviceName=%q: errs = %v, want a FieldError keyed device_name", name, errs)
		}
	}
}

func TestValidate_DeviceNameTooLong(t *testing.T) {
	c := DefaultConfig()
	c.DeviceName = strings.Repeat("a", deviceNameMaxLen+1)
	err := c.Validate()
	if err == nil {
		t.Fatalf("DeviceName length = %d: want validation error, got nil", deviceNameMaxLen+1)
	}
	var errs adapters.FieldErrors
	if !errors.As(err, &errs) {
		t.Fatalf("error type = %T, want adapters.FieldErrors", err)
	}
	if errs[0].Key != "device_name" {
		t.Errorf("err.Key = %q, want device_name", errs[0].Key)
	}

	// Boundary: exactly deviceNameMaxLen runes is fine.
	c.DeviceName = strings.Repeat("a", deviceNameMaxLen)
	if err := c.Validate(); err != nil {
		t.Errorf("DeviceName length = %d (boundary): %v, want nil", deviceNameMaxLen, err)
	}
}

func TestValidate_DeviceNameUnprintable(t *testing.T) {
	for _, bad := range []string{
		"My\x00Device",
		"NUL\x00",
		"Bell\x07",
		"CR\rEnded",
		"LF\nEnded",
		"Tab\tEmbedded",
	} {
		c := DefaultConfig()
		c.DeviceName = bad
		err := c.Validate()
		if err == nil {
			t.Errorf("DeviceName=%q: want validation error, got nil", bad)
			continue
		}
		var errs adapters.FieldErrors
		if !errors.As(err, &errs) {
			t.Errorf("DeviceName=%q: error type = %T, want adapters.FieldErrors", bad, err)
			continue
		}
		found := false
		for _, fe := range errs {
			if fe.Key == "device_name" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DeviceName=%q: errs = %v, want a FieldError keyed device_name", bad, errs)
		}
	}
}

func TestValidate_DeviceName_PreservesCasing(t *testing.T) {
	// Spec note: DLNA controllers display the friendlyName verbatim;
	// Validate must not lower-case or otherwise normalize casing
	// (only the validator runs on the receiver; .DeviceName is read
	// back to confirm the operator's value survived round-trip).
	c := DefaultConfig()
	c.DeviceName = "MiSTer-Lounge-CRT"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.DeviceName != "MiSTer-Lounge-CRT" {
		t.Errorf("DeviceName mutated by Validate: got %q, want %q", c.DeviceName, "MiSTer-Lounge-CRT")
	}
}

func TestValidate_DeviceName_AcceptsUnicode(t *testing.T) {
	// The length cap is in runes, not bytes; non-ASCII friendly names
	// are part of the operator's expressive surface.
	c := DefaultConfig()
	c.DeviceName = "MiSTer-客厅" // 9 runes, 13 bytes
	if err := c.Validate(); err != nil {
		t.Errorf("Unicode DeviceName rejected: %v", err)
	}
}
