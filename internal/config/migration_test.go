package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyTOML is a representative pre-migration config covering every
// known flat key.
const legacyTOML = `
device_name = "LivingRoomMiSTer"
mister_host = "192.168.1.42"
mister_port = 32100
source_port = 32101
http_port = 32500
host_ip = "192.168.1.20"
modeline = "NTSC_480i"
interlace_field_order = "bff"
aspect_mode = "zoom"
rgb_mode = "rgb888"
lz4_enabled = false
audio_sample_rate = 44100
audio_channels = 1
plex_profile_name = "Plex Home Theater"
plex_server_url = "http://192.168.1.100:32400"
data_dir = "/config"
`

const sectionedTOML = `
[bridge]
data_dir = "/config"

[bridge.video]
modeline = "NTSC_480i"
interlace_field_order = "tff"
aspect_mode = "auto"
rgb_mode = "rgb888"
lz4_enabled = true

[bridge.audio]
sample_rate = 48000
channels = 2

[bridge.mister]
host = "192.168.1.50"
port = 32100
source_port = 32101

[bridge.ui]
http_port = 32500

[adapters.plex]
enabled = true
device_name = "MiSTer"
`

const invalidTOML = `
[bridge
http_port = 32500
`

func TestDetect_LegacyOnly(t *testing.T) {
	got := Detect([]byte(legacyTOML))
	if got != FormatLegacy {
		t.Errorf("Detect(legacy) = %v, want FormatLegacy", got)
	}
}

func TestDetect_SectionedOnly(t *testing.T) {
	got := Detect([]byte(sectionedTOML))
	if got != FormatSectioned {
		t.Errorf("Detect(sectioned) = %v, want FormatSectioned", got)
	}
}

func TestDetect_PartiallyMigrated(t *testing.T) {
	mixed := legacyTOML + "\n" + sectionedTOML
	got := Detect([]byte(mixed))
	if got != FormatPartial {
		t.Errorf("Detect(mixed) = %v, want FormatPartial", got)
	}
}

func TestDetect_Empty(t *testing.T) {
	got := Detect([]byte(""))
	if got != FormatEmpty {
		t.Errorf("Detect(empty) = %v, want FormatEmpty", got)
	}
}

func TestDetect_Invalid(t *testing.T) {
	got := Detect([]byte(invalidTOML))
	if got != FormatInvalid {
		t.Errorf("Detect(invalid) = %v, want FormatInvalid", got)
	}
}

func TestMigrate_FullRoundTrip(t *testing.T) {
	out, err := Migrate([]byte(legacyTOML))
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// After migration, Detect should say sectioned (no residual flat keys).
	if got := Detect(out); got != FormatSectioned {
		t.Errorf("post-migrate Detect = %v, want FormatSectioned", got)
	}

	// The migrated bytes must parse cleanly into the Sectioned type.
	s, _, err := loadSectionedFromBytes(out)
	if err != nil {
		t.Fatalf("load migrated: %v", err)
	}
	if s.Bridge.MiSTer.Host != "192.168.1.42" {
		t.Errorf("mister.host = %q, want 192.168.1.42", s.Bridge.MiSTer.Host)
	}
	if s.Bridge.Video.InterlaceFieldOrder != "bff" {
		t.Errorf("interlace = %q, want bff", s.Bridge.Video.InterlaceFieldOrder)
	}
	if s.Bridge.Audio.SampleRate != 44100 {
		t.Errorf("audio.sample_rate = %d, want 44100", s.Bridge.Audio.SampleRate)
	}
	if s.Bridge.Audio.Channels != 1 {
		t.Errorf("audio.channels = %d, want 1", s.Bridge.Audio.Channels)
	}
	if s.Bridge.Video.LZ4Enabled {
		t.Error("lz4_enabled should have round-tripped as false")
	}
}

func TestMigrate_RejectsSectioned(t *testing.T) {
	_, err := Migrate([]byte(sectionedTOML))
	if err == nil {
		t.Fatal("want error on sectioned input")
	}
	if !strings.Contains(err.Error(), "not legacy") {
		t.Errorf("error should mention 'not legacy': %v", err)
	}
}

func TestLoad_AutoMigratesLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(legacyTOML), 0644); err != nil {
		t.Fatal(err)
	}

	s, err := LoadSectioned(path)
	if err != nil {
		t.Fatalf("LoadSectioned: %v", err)
	}
	if s.Bridge.MiSTer.Host != "192.168.1.42" {
		t.Errorf("mister.host = %q, want migrated value", s.Bridge.MiSTer.Host)
	}

	// Backup exists.
	backup := filepath.Join(dir, "config.toml.pre-ui-migration")
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(data) != legacyTOML {
		t.Error("backup does not match original legacy bytes")
	}

	// On-disk file is now sectioned.
	if got := Detect(mustRead(t, path)); got != FormatSectioned {
		t.Errorf("post-load disk format = %v, want FormatSectioned", got)
	}
}

func TestLoad_AbortsPartiallyMigrated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	mixed := legacyTOML + "\n" + sectionedTOML
	if err := os.WriteFile(path, []byte(mixed), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSectioned(path)
	if err == nil {
		t.Fatal("want error on partial config")
	}
	if !strings.Contains(err.Error(), "partially migrated") {
		t.Errorf("error should mention 'partially migrated': %v", err)
	}
}

func TestLoad_InvalidTOMLFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(invalidTOML), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSectioned(path)
	if err == nil {
		t.Fatal("want error on invalid TOML")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Errorf("error should mention parse config: %v", err)
	}
}

func TestLoad_MissingWritesSectionedDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "config.toml")

	_, err := LoadSectioned(path)
	var created *ErrConfigCreated
	if !errors.As(err, &created) {
		t.Fatalf("first LoadSectioned: want *ErrConfigCreated, got %v", err)
	}
	if created.Path != path {
		t.Errorf("ErrConfigCreated.Path = %q, want %q", created.Path, path)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("seed file not written: %v", readErr)
	}
	if string(data) != string(ExampleTOML()) {
		t.Error("seed file does not match embedded example")
	}
	if got := Detect(data); got != FormatSectioned {
		t.Errorf("seeded format = %v, want FormatSectioned", got)
	}

	s, err := LoadSectioned(path)
	if err != nil {
		t.Fatalf("second LoadSectioned (file now exists): %v", err)
	}
	if s.Bridge.MiSTer.Host != "192.168.1.50" {
		t.Errorf("bridge.mister.host = %q, want %q", s.Bridge.MiSTer.Host, "192.168.1.50")
	}
	if len(s.Adapters) == 0 {
		t.Error("expected seeded adapters to decode")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSectioned_Validate_HappyPath(t *testing.T) {
	s := &Sectioned{Bridge: defaultBridge()}
	s.Bridge.MiSTer.Host = "192.168.1.42" // required
	if err := s.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestSectioned_Validate_MissingMisterHost(t *testing.T) {
	s := &Sectioned{Bridge: defaultBridge()}
	// host deliberately empty
	err := s.Validate()
	if err == nil {
		t.Fatal("want validation error for empty mister host")
	}
	if !strings.Contains(err.Error(), "bridge.mister.host") {
		t.Errorf("error should mention bridge.mister.host: %v", err)
	}
}

func TestSectioned_Validate_BadPort(t *testing.T) {
	for _, bad := range []int{0, -1, 65536, 99999} {
		t.Run(fmt.Sprintf("port=%d", bad), func(t *testing.T) {
			s := &Sectioned{Bridge: defaultBridge()}
			s.Bridge.MiSTer.Host = "192.168.1.42"
			s.Bridge.UI.HTTPPort = bad
			if err := s.Validate(); err == nil {
				t.Error("want validation error for bad port")
			}
		})
	}
}

func TestSectioned_Validate_BadInterlaceOrder(t *testing.T) {
	s := &Sectioned{Bridge: defaultBridge()}
	s.Bridge.MiSTer.Host = "192.168.1.42"
	s.Bridge.Video.InterlaceFieldOrder = "sideways"
	if err := s.Validate(); err == nil {
		t.Error("want validation error for bad interlace order")
	}
}
