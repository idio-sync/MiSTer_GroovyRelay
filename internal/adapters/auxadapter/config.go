package aux

import (
	"fmt"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

type Config struct {
	Enabled bool     `toml:"enabled"`
	Input   AUXInput `toml:"input"`
}

type AUXInput struct {
	ID                    string `toml:"id"`
	Name                  string `toml:"name"`
	Mode                  string `toml:"mode"`
	AudioOutput           string `toml:"audio_output"`
	URL                   string `toml:"url"`
	Format                string `toml:"format"`
	Device                string `toml:"device"`
	SampleRate            int    `toml:"sample_rate"`
	Channels              int    `toml:"channels"`
	ThreadQueueSize       int    `toml:"thread_queue_size"`
	AnalyzeDurationMillis int    `toml:"analyze_duration_ms"`
	ProbeSize             int    `toml:"probe_size"`
}

const (
	ModeStreamURL    = "stream_url"
	ModeLocalCapture = "local_capture"

	AudioOutputVisualOnly = "visual_only"
	AudioOutputMonitor    = "monitor"
)

func DefaultConfig() Config {
	return Config{
		Enabled: false,
		Input: AUXInput{
			ID:                    "aux",
			Name:                  "AUX",
			Mode:                  ModeStreamURL,
			AudioOutput:           AudioOutputVisualOnly,
			ThreadQueueSize:       64,
			AnalyzeDurationMillis: 100,
			ProbeSize:             32768,
		},
	}
}

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}

	var errs adapters.FieldErrors
	if strings.TrimSpace(c.Input.ID) == "" {
		errs = append(errs, adapters.FieldError{Key: "input.id", Msg: "must not be empty"})
	}
	if strings.TrimSpace(c.Input.Name) == "" {
		errs = append(errs, adapters.FieldError{Key: "input.name", Msg: "must not be empty"})
	}

	switch c.Input.Mode {
	case ModeStreamURL:
		if strings.TrimSpace(c.Input.URL) == "" {
			errs = append(errs, adapters.FieldError{Key: "input.url", Msg: "must be configured when mode is stream_url"})
		}
	case ModeLocalCapture:
		if strings.TrimSpace(c.Input.Format) == "" {
			errs = append(errs, adapters.FieldError{Key: "input.format", Msg: "must be configured when mode is local_capture"})
		}
		if strings.TrimSpace(c.Input.Device) == "" {
			errs = append(errs, adapters.FieldError{Key: "input.device", Msg: "must be configured when mode is local_capture"})
		}
		switch c.Input.SampleRate {
		case 22050, 44100, 48000:
		default:
			errs = append(errs, adapters.FieldError{Key: "input.sample_rate", Msg: "must be 22050, 44100, or 48000"})
		}
		switch c.Input.Channels {
		case 1, 2:
		default:
			errs = append(errs, adapters.FieldError{Key: "input.channels", Msg: "must be 1 or 2"})
		}
	default:
		errs = append(errs, adapters.FieldError{
			Key: "input.mode",
			Msg: fmt.Sprintf("must be %q or %q", ModeStreamURL, ModeLocalCapture),
		})
	}

	switch c.Input.AudioOutput {
	case "", AudioOutputVisualOnly, AudioOutputMonitor:
	default:
		errs = append(errs, adapters.FieldError{
			Key: "input.audio_output",
			Msg: fmt.Sprintf("must be %q or %q", AudioOutputVisualOnly, AudioOutputMonitor),
		})
	}

	return errs.Err()
}
