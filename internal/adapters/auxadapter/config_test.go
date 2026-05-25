package aux

import (
	"errors"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled {
		t.Fatal("DefaultConfig.Enabled = true, want false")
	}
	if c.Input.ID != "aux" {
		t.Errorf("Input.ID = %q, want aux", c.Input.ID)
	}
	if c.Input.Name != "AUX" {
		t.Errorf("Input.Name = %q, want AUX", c.Input.Name)
	}
	if c.Input.Mode != ModeStreamURL {
		t.Errorf("Input.Mode = %q, want %q", c.Input.Mode, ModeStreamURL)
	}
	if c.Input.AudioOutput != AudioOutputVisualOnly {
		t.Errorf("Input.AudioOutput = %q, want %q", c.Input.AudioOutput, AudioOutputVisualOnly)
	}
	if c.Input.ThreadQueueSize != 64 {
		t.Errorf("Input.ThreadQueueSize = %d, want 64", c.Input.ThreadQueueSize)
	}
	if c.Input.AnalyzeDurationMillis != 100 {
		t.Errorf("Input.AnalyzeDurationMillis = %d, want 100", c.Input.AnalyzeDurationMillis)
	}
	if c.Input.ProbeSize != 32768 {
		t.Errorf("Input.ProbeSize = %d, want 32768", c.Input.ProbeSize)
	}
}

func TestValidateDisabledAllowsIncompleteInput(t *testing.T) {
	c := Config{
		Enabled: false,
		Input: AUXInput{
			ID:   "",
			Name: "",
			Mode: "",
			URL:  "",
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled incomplete input should validate: %v", err)
	}
}

func TestValidateEnabledStreamURLRequiresURL(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	err := c.Validate()
	if err == nil {
		t.Fatal("enabled stream_url without input.url: want validation error")
	}
	assertFieldError(t, err, "input.url")

	c.Input.URL = "http://127.0.0.1:8080/aux.wav"
	if err := c.Validate(); err != nil {
		t.Fatalf("enabled stream_url with input.url should validate: %v", err)
	}
}

func TestValidateEnabledLocalCaptureRequiresDevice(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.Input.Mode = ModeLocalCapture
	err := c.Validate()
	if err == nil {
		t.Fatal("enabled local_capture without input.device: want validation error")
	}
	assertFieldError(t, err, "input.device")

	c.Input.Device = "Microphone (USB Audio)"
	c.Input.Format = "avfoundation"
	c.Input.SampleRate = 44100
	c.Input.Channels = 2
	if err := c.Validate(); err != nil {
		t.Fatalf("enabled local_capture with complete capture input should validate: %v", err)
	}
}

func TestValidateEnabledLocalCaptureRequiresCoreSupportedAudioShape(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.Input.Mode = ModeLocalCapture
	c.Input.Device = "Microphone (USB Audio)"

	err := c.Validate()
	if err == nil {
		t.Fatal("enabled local_capture with only input.device: want validation errors")
	}
	assertFieldError(t, err, "input.format")
	assertFieldError(t, err, "input.sample_rate")
	assertFieldError(t, err, "input.channels")

	c.Input.Format = "avfoundation"
	for _, sampleRate := range []int{22050, 44100, 48000} {
		for _, channels := range []int{1, 2} {
			c.Input.SampleRate = sampleRate
			c.Input.Channels = channels
			if err := c.Validate(); err != nil {
				t.Fatalf("sample_rate=%d channels=%d should validate: %v", sampleRate, channels, err)
			}
		}
	}

	c.Input.SampleRate = 96000
	c.Input.Channels = 2
	err = c.Validate()
	if err == nil {
		t.Fatal("unsupported sample rate accepted")
	}
	assertFieldError(t, err, "input.sample_rate")

	c.Input.SampleRate = 48000
	c.Input.Channels = 6
	err = c.Validate()
	if err == nil {
		t.Fatal("unsupported channel count accepted")
	}
	assertFieldError(t, err, "input.channels")
}

func TestValidateEnabledRequiresIdentityAndKnownEnums(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
		key  string
	}{
		{
			name: "id",
			edit: func(c *Config) {
				c.Input.ID = " "
			},
			key: "input.id",
		},
		{
			name: "name",
			edit: func(c *Config) {
				c.Input.Name = "\t"
			},
			key: "input.name",
		},
		{
			name: "mode",
			edit: func(c *Config) {
				c.Input.Mode = "alsa"
			},
			key: "input.mode",
		},
		{
			name: "audio_output",
			edit: func(c *Config) {
				c.Input.AudioOutput = "speakers"
			},
			key: "input.audio_output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DefaultConfig()
			c.Enabled = true
			c.Input.URL = "http://127.0.0.1:8080/aux.wav"
			tt.edit(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want field error %q", tt.key)
			}
			assertFieldError(t, err, tt.key)
		})
	}
}

func TestConfigTOMLDecodeUsesDottedInputFields(t *testing.T) {
	raw := `
[adapters.aux]
enabled = true

[adapters.aux.input]
id = "line-in"
name = "Line In"
mode = "local_capture"
audio_output = "monitor"
format = "avfoundation"
device = ":0"
sample_rate = 44100
channels = 1
thread_queue_size = 128
analyze_duration_ms = 250
probe_size = 65536
`
	var envelope struct {
		Adapters map[string]toml.Primitive `toml:"adapters"`
	}
	meta, err := toml.Decode(raw, &envelope)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	if err := meta.PrimitiveDecode(envelope.Adapters["aux"], &cfg); err != nil {
		t.Fatalf("PrimitiveDecode: %v", err)
	}

	if !cfg.Enabled {
		t.Fatal("Enabled not decoded")
	}
	if cfg.Input.ID != "line-in" || cfg.Input.Name != "Line In" {
		t.Fatalf("decoded identity = %q/%q, want line-in/Line In", cfg.Input.ID, cfg.Input.Name)
	}
	if cfg.Input.Mode != ModeLocalCapture {
		t.Errorf("Mode = %q, want %q", cfg.Input.Mode, ModeLocalCapture)
	}
	if cfg.Input.AudioOutput != AudioOutputMonitor {
		t.Errorf("AudioOutput = %q, want %q", cfg.Input.AudioOutput, AudioOutputMonitor)
	}
	if cfg.Input.SampleRate != 44100 || cfg.Input.Channels != 1 {
		t.Errorf("audio shape = %d/%d, want 44100/1", cfg.Input.SampleRate, cfg.Input.Channels)
	}
	if cfg.Input.AnalyzeDurationMillis != 250 {
		t.Errorf("AnalyzeDurationMillis = %d, want 250", cfg.Input.AnalyzeDurationMillis)
	}
}

func assertFieldError(t *testing.T, err error, key string) {
	t.Helper()
	var fieldErrs adapters.FieldErrors
	if !errors.As(err, &fieldErrs) {
		t.Fatalf("error type = %T, want adapters.FieldErrors", err)
	}
	for _, fieldErr := range fieldErrs {
		if fieldErr.Key == key {
			return
		}
	}
	t.Fatalf("FieldErrors = %v, want key %q", fieldErrs, key)
}
