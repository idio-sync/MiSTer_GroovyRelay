package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Format describes the shape of a raw config.toml prior to decoding.
type Format int

const (
	FormatEmpty      Format = iota // no relevant keys (fresh install)
	FormatLegacy                   // flat keys, no [bridge] table
	FormatSectioned                // [bridge] present, no flat keys
	FormatPartial                  // both — hand-edited mid-migration, abort
	FormatInvalid                  // syntactically invalid TOML, abort
)

// legacyKeys is the set of top-level keys that existed in the pre-UI
// (pre-2026-04-20) flat schema. A decoder seeing any of these at the
// top level of a config file is looking at a legacy (or
// partially-migrated) document. Design §5.2 authoritative source.
var legacyKeys = []string{
	"device_name",
	"device_uuid",
	"mister_host",
	"mister_port",
	"source_port",
	"http_port",
	"host_ip",
	"modeline",
	"interlace_field_order",
	"aspect_mode",
	"rgb_mode",
	"lz4_enabled",
	"audio_sample_rate",
	"audio_channels",
	"plex_profile_name",
	"plex_server_url",
	"data_dir",
}

// Detect classifies raw config bytes into one of the Format values.
// The classification drives the Load flow: Empty → proceed with
// defaults; Legacy → migrate; Sectioned → decode; Partial/Invalid → abort.
func Detect(raw []byte) Format {
	format, _ := detectFormat(raw)
	return format
}

func detectFormat(raw []byte) (Format, error) {
	// legacyProbe: undecoded into map to check presence of top-level
	// flat keys. We decode into a generic map rather than the Config
	// struct so unknown sections (e.g., [bridge]) don't cause a parse
	// failure.
	var probe map[string]any
	if err := toml.Unmarshal(raw, &probe); err != nil {
		return FormatInvalid, fmt.Errorf("parse config: %w", err)
	}

	hasLegacy := false
	for _, k := range legacyKeys {
		if _, ok := probe[k]; ok {
			hasLegacy = true
			break
		}
	}
	_, hasBridge := probe["bridge"]

	switch {
	case hasLegacy && hasBridge:
		return FormatPartial, nil
	case hasLegacy:
		return FormatLegacy, nil
	case hasBridge:
		return FormatSectioned, nil
	default:
		return FormatEmpty, nil
	}
}

// Migrate takes legacy flat TOML bytes and returns equivalent
// sectioned TOML bytes. Field mapping is authoritative per spec §5.2.
// Missing legacy keys are filled with defaults.
func Migrate(legacy []byte) ([]byte, error) {
	if Detect(legacy) != FormatLegacy {
		return nil, fmt.Errorf("migrate: input is not legacy flat format")
	}

	// Decode legacy flat TOML into the old Config shape.
	old := defaults()
	if err := toml.Unmarshal(legacy, old); err != nil {
		return nil, fmt.Errorf("migrate: parse legacy: %w", err)
	}

	// Build sectioned equivalent.
	sec := struct {
		Bridge   BridgeConfig                      `toml:"bridge"`
		Adapters map[string]map[string]interface{} `toml:"adapters"`
	}{
		Bridge: BridgeConfig{
			DataDir: old.DataDir,
			HostIP:  old.HostIP,
			Video: VideoConfig{
				Modeline:            old.Modeline,
				InterlaceFieldOrder: old.InterlaceFieldOrder,
				AspectMode:          old.AspectMode,
				RGBMode:             old.RGBMode,
				LZ4Enabled:          old.LZ4Enabled,
			},
			Audio: AudioConfig{
				SampleRate: old.AudioSampleRate,
				Channels:   old.AudioChannels,
			},
			MiSTer: MisterConfig{
				Host:       old.MisterHost,
				Port:       old.MisterPort,
				SourcePort: old.SourcePort,
			},
			UI: UIConfig{
				HTTPPort: old.HTTPPort,
			},
		},
		Adapters: map[string]map[string]interface{}{
			"plex": {
				"enabled":      true,
				"device_name":  old.DeviceName,
				"device_uuid":  old.DeviceUUID,
				"profile_name": old.PlexProfileName,
				"server_url":   old.PlexServerURL,
			},
		},
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(sec); err != nil {
		return nil, fmt.Errorf("migrate: encode: %w", err)
	}

	// Prepend a header comment so a user inspecting the migrated file
	// immediately sees provenance. Encoder output is already
	// well-formatted TOML; this just adds preamble.
	header := "# config.toml — migrated from flat schema to sectioned on load.\n" +
		"# Original backed up as config.toml.pre-ui-migration.\n" +
		"# Shape documented in docs/specs/2026-04-20-settings-ui-design.md.\n\n"
	return []byte(header + buf.String()), nil
}

// LoadSectioned reads path, detects format, runs migration if needed
// (with a backup), and returns the decoded Sectioned config.
//
// On FormatPartial it returns a diagnostic error listing the residual
// top-level keys that must be removed by hand — silent-ignore would
// leave the user editing fields the bridge no longer reads.
func LoadSectioned(path string) (*Sectioned, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if wErr := writeDefaultConfig(path); wErr != nil {
				return nil, wErr
			}
			return nil, &ErrConfigCreated{Path: path}
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	format, err := detectFormat(data)
	if err != nil {
		return nil, err
	}

	switch format {
	case FormatPartial:
		residuals := listResidualKeys(data)
		return nil, fmt.Errorf(
			"config at %s is partially migrated: it has both a [bridge] "+
				"section and legacy top-level keys (%s). Either remove the "+
				"top-level keys (the [bridge] section is authoritative) or "+
				"delete the [bridge] section to re-migrate from the flat format",
			path, strings.Join(residuals, ", "))

	case FormatLegacy:
		// Back up, migrate, rewrite.
		backup := path + ".pre-ui-migration"
		if err := os.WriteFile(backup, data, 0644); err != nil {
			return nil, fmt.Errorf("write migration backup: %w", err)
		}
		migrated, err := Migrate(data)
		if err != nil {
			return nil, fmt.Errorf("migrate legacy config: %w", err)
		}
		if err := WriteAtomic(path, migrated); err != nil {
			return nil, fmt.Errorf("write migrated config: %w", err)
		}
		data = migrated
		// fall through to sectioned-decode path

	case FormatEmpty:
		// Existing but empty/blank file: decode nothing and layer defaults.
		s := &Sectioned{
			Bridge:   defaultBridge(),
			Adapters: map[string]toml.Primitive{},
		}
		return s, nil
	}

	s, meta, err := loadSectionedFromBytes(data)
	if err != nil {
		return nil, err
	}
	s.meta = meta
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("config invalid: %w", err)
	}
	return s, nil
}

// loadSectionedFromBytes decodes sectioned-format bytes into a
// Sectioned value. Exposed package-private for test use.
func loadSectionedFromBytes(data []byte) (*Sectioned, toml.MetaData, error) {
	s := &Sectioned{
		Bridge:   defaultBridge(),
		Adapters: map[string]toml.Primitive{},
	}
	meta, err := toml.Decode(string(data), s)
	if err != nil {
		return nil, toml.MetaData{}, fmt.Errorf("parse sectioned config: %w", err)
	}
	return s, meta, nil
}

// defaultBridge returns a BridgeConfig populated with the same values
// the old flat defaults() returned for the bridge-level fields.
func defaultBridge() BridgeConfig {
	d := defaults()
	return BridgeConfig{
		DataDir: d.DataDir,
		HostIP:  d.HostIP,
		Video: VideoConfig{
			Modeline:            d.Modeline,
			InterlaceFieldOrder: d.InterlaceFieldOrder,
			AspectMode:          d.AspectMode,
			RGBMode:             d.RGBMode,
			LZ4Enabled:          d.LZ4Enabled,
		},
		Audio: AudioConfig{
			SampleRate: d.AudioSampleRate,
			Channels:   d.AudioChannels,
		},
		MiSTer: MisterConfig{
			Port:       d.MisterPort,
			SourcePort: d.SourcePort,
			SSHUser:    "root",
		},
		UI: UIConfig{
			HTTPPort: d.HTTPPort,
		},
	}
}

// listResidualKeys returns the top-level legacy keys present in raw,
// in declaration order. Used to build the partially-migrated error
// message with actionable detail.
func listResidualKeys(raw []byte) []string {
	var probe map[string]any
	_ = toml.Unmarshal(raw, &probe)
	var out []string
	for _, k := range legacyKeys {
		if _, ok := probe[k]; ok {
			out = append(out, k)
		}
	}
	return out
}
