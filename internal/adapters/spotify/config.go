package spotify

import (
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

// Config is the [adapters.spotify] section.
type Config struct {
	Enabled           bool   `toml:"enabled"`
	Name              string `toml:"name"`        // advertised Spotify Connect device name
	BinaryPath        string `toml:"binary_path"` // librespot; empty = PATH lookup
	AudioOutput       string `toml:"audio_output"`
	PauseGraceSeconds int    `toml:"pause_grace_seconds"`
	Bitrate           int    `toml:"bitrate"`
	ZeroconfPort      int    `toml:"zeroconf_port"` // 0 = random
}

const (
	AudioOutputMonitor    = "monitor"
	AudioOutputVisualOnly = "visual_only"

	maxPauseGraceSeconds = 600
	maxNameLength        = 63 // mDNS instance names are capped at 63 bytes
)

func DefaultConfig() Config {
	return Config{
		Name:              "MiSTer CRT",
		AudioOutput:       AudioOutputMonitor,
		PauseGraceSeconds: 30,
		Bitrate:           320,
	}
}

func decodeConfig(raw toml.Primitive, meta toml.MetaData) (Config, error) {
	cfg := DefaultConfig()
	if reflect.ValueOf(raw).IsZero() {
		return cfg, nil
	}
	if err := meta.PrimitiveDecode(raw, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var errs adapters.FieldErrors
	name := strings.TrimSpace(c.Name)
	switch {
	case name == "":
		errs = append(errs, adapters.FieldError{Key: "name", Msg: "must not be empty"})
	case len(name) > maxNameLength:
		errs = append(errs, adapters.FieldError{Key: "name", Msg: fmt.Sprintf("must be at most %d bytes", maxNameLength)})
	case !utf8.ValidString(name):
		errs = append(errs, adapters.FieldError{Key: "name", Msg: "must be valid UTF-8"})
	}
	if strings.ContainsAny(c.BinaryPath, "\r\n") {
		errs = append(errs, adapters.FieldError{Key: "binary_path", Msg: "must be a single path"})
	}
	switch c.AudioOutput {
	case AudioOutputMonitor, AudioOutputVisualOnly:
	default:
		errs = append(errs, adapters.FieldError{Key: "audio_output", Msg: fmt.Sprintf("must be %q or %q", AudioOutputMonitor, AudioOutputVisualOnly)})
	}
	if c.PauseGraceSeconds < 0 || c.PauseGraceSeconds > maxPauseGraceSeconds {
		errs = append(errs, adapters.FieldError{Key: "pause_grace_seconds", Msg: fmt.Sprintf("must be 0 to %d", maxPauseGraceSeconds)})
	}
	switch c.Bitrate {
	case 96, 160, 320:
	default:
		errs = append(errs, adapters.FieldError{Key: "bitrate", Msg: "must be 96, 160, or 320"})
	}
	if c.ZeroconfPort < 0 || c.ZeroconfPort > 65535 {
		errs = append(errs, adapters.FieldError{Key: "zeroconf_port", Msg: "must be 0 (random) to 65535"})
	}
	return errs.Err()
}

// helperSettings are the fields baked into the librespot command line;
// changing any of them restarts the helper.
func (c Config) helperSettings() [4]any {
	return [4]any{strings.TrimSpace(c.Name), strings.TrimSpace(c.BinaryPath), c.Bitrate, c.ZeroconfPort}
}
