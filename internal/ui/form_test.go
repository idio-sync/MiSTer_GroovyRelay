package ui

import (
	"net/url"
	"testing"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

func TestParseBridgeForm_HappyPath(t *testing.T) {
	form := url.Values{}
	form.Set("mister.host", "192.168.1.42")
	form.Set("mister.port", "32100")
	form.Set("mister.source_port", "32101")
	form.Set("host_ip", "")
	form.Set("video.modeline", "NTSC_480i")
	form.Set("video.interlace_field_order", "bff")
	form.Set("video.aspect_mode", "auto")
	form.Set("video.lz4_enabled", "true")
	form.Set("audio.sample_rate", "48000")
	form.Set("audio.channels", "2")
	form.Set("ui.http_port", "32500")
	form.Set("data_dir", "/config")

	got, err := parseBridgeForm(form)
	if err != nil {
		t.Fatalf("parseBridgeForm: %v", err)
	}
	if got.MiSTer.Host != "192.168.1.42" {
		t.Errorf("Host = %q", got.MiSTer.Host)
	}
	if got.MiSTer.Port != 32100 {
		t.Errorf("Port = %d", got.MiSTer.Port)
	}
	if got.Video.InterlaceFieldOrder != "bff" {
		t.Errorf("InterlaceFieldOrder = %q", got.Video.InterlaceFieldOrder)
	}
	if !got.Video.LZ4Enabled {
		t.Error("LZ4Enabled should be true")
	}
	if got.Audio.Channels != 2 {
		t.Errorf("Channels = %d", got.Audio.Channels)
	}
}

func TestParseBridgeForm_BadInt(t *testing.T) {
	form := url.Values{}
	form.Set("mister.host", "192.168.1.42")
	form.Set("mister.port", "not-a-number")
	form.Set("mister.source_port", "32101")
	form.Set("video.modeline", "NTSC_480i")
	form.Set("video.interlace_field_order", "tff")
	form.Set("video.aspect_mode", "auto")
	form.Set("audio.sample_rate", "48000")
	form.Set("audio.channels", "2")
	form.Set("ui.http_port", "32500")
	form.Set("data_dir", "/config")

	_, err := parseBridgeForm(form)
	if err == nil {
		t.Fatal("want error on bad int")
	}
	fe, ok := err.(FormErrors)
	if !ok {
		t.Fatalf("want FormErrors, got %T", err)
	}
	if _, seen := fe["mister.port"]; !seen {
		t.Errorf("want mister.port error, got %v", fe)
	}
}

func TestParseBridgeForm_BoolFalse(t *testing.T) {
	// HTML checkboxes omit the field when unchecked — our parser
	// must treat a missing bool key as false, not a validation error.
	form := url.Values{}
	form.Set("mister.host", "192.168.1.42")
	form.Set("mister.port", "32100")
	form.Set("mister.source_port", "32101")
	form.Set("video.modeline", "NTSC_480i")
	form.Set("video.interlace_field_order", "tff")
	form.Set("video.aspect_mode", "auto")
	// no video.lz4_enabled → should default to false
	form.Set("audio.sample_rate", "48000")
	form.Set("audio.channels", "2")
	form.Set("ui.http_port", "32500")
	form.Set("data_dir", "/config")

	got, err := parseBridgeForm(form)
	if err != nil {
		t.Fatalf("parseBridgeForm: %v", err)
	}
	if got.Video.LZ4Enabled {
		t.Error("missing checkbox should parse as false, got true")
	}
	_ = got
	_ = config.BridgeConfig{} // ensure import
}
