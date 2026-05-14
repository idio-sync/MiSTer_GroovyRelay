package hlsbuffer

import "testing"

func TestVariantSelectionUsesHighestAvailableBelowOutput(t *testing.T) {
	variants := []Variant{
		{URI: "360p.m3u8", Bandwidth: 800000, Width: 640, Height: 360, Codecs: "avc1.64001f,mp4a.40.2"},
		{URI: "540p-high.m3u8", Bandwidth: 1600000, Width: 960, Height: 540, Codecs: "avc1.64001f,mp4a.40.2"},
		{URI: "540p-low.m3u8", Bandwidth: 1200000, Width: 960, Height: 540, Codecs: "avc1.64001f,mp4a.40.2"},
	}

	got, err := SelectVariant(variants, 720, 720)
	if err != nil {
		t.Fatalf("SelectVariant: %v", err)
	}
	if got.URI != "540p-low.m3u8" {
		t.Fatalf("selected URI = %q, want 540p-low.m3u8", got.URI)
	}
}

func TestVariantSelectionRejectsAudioOnlyCodecs(t *testing.T) {
	_, err := SelectVariant([]Variant{
		{URI: "audio.m3u8", Bandwidth: 96000, Codecs: "mp4a.40.2"},
	}, 480, 720)
	if err == nil {
		t.Fatal("SelectVariant error = nil, want audio-only rejection")
	}
}

func TestVariantChangeRequiresRestart(t *testing.T) {
	base := Variant{
		URI:       "480p.m3u8",
		Bandwidth: 900000,
		Width:     854,
		Height:    480,
		Codecs:    "avc1.64001f,mp4a.40.2",
	}
	if !VariantCompatible(base, Variant{
		URI:       base.URI,
		Bandwidth: 1000000,
		Width:     base.Width,
		Height:    base.Height,
		Codecs:    base.Codecs,
	}) {
		t.Fatal("bandwidth-only variant metadata change should not require restart")
	}
	if VariantCompatible(base, Variant{
		URI:       "720p.m3u8",
		Bandwidth: base.Bandwidth,
		Width:     base.Width,
		Height:    base.Height,
		Codecs:    base.Codecs,
	}) {
		t.Fatal("URI change should require restart")
	}
	if VariantCompatible(base, Variant{
		URI:       base.URI,
		Bandwidth: base.Bandwidth,
		Width:     1280,
		Height:    720,
		Codecs:    base.Codecs,
	}) {
		t.Fatal("resolution change should require restart")
	}
	if VariantCompatible(base, Variant{
		URI:       base.URI,
		Bandwidth: base.Bandwidth,
		Width:     base.Width,
		Height:    base.Height,
		Codecs:    "hvc1.1.6.L93.B0,mp4a.40.2",
	}) {
		t.Fatal("codec change should require restart")
	}
}
