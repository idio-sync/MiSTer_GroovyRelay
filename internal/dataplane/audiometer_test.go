package dataplane

import (
	"encoding/binary"
	"math"
	"testing"
)

// makeStereoPCM generates a stereo s16le PCM chunk of length frames.
// generators[ch] is called for each frame index. Returns the byte buffer.
func makeStereoPCM(frames int, generators [2]func(i int) float32) []byte {
	buf := make([]byte, frames*2*2) // frames * channels * bytesPerSample
	for i := 0; i < frames; i++ {
		for ch := 0; ch < 2; ch++ {
			v := generators[ch](i)
			if v > 1.0 {
				v = 1.0
			} else if v < -1.0 {
				v = -1.0
			}
			s := int16(v * 32767)
			binary.LittleEndian.PutUint16(buf[(i*2+ch)*2:], uint16(s))
		}
	}
	return buf
}

func TestAudioMeter_SilenceSnapshot(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
	pcm := makeStereoPCM(800, [2]func(int) float32{
		func(i int) float32 { return 0 },
		func(i int) float32 { return 0 },
	})
	m.Observe(pcm, 2, 48000)
	snap := m.AudioScopes()
	if snap == nil {
		t.Fatal("snapshot is nil after Observe")
	}
	if snap.Peak[0] != 0 || snap.Peak[1] != 0 {
		t.Errorf("Peak = %v, want [0 0]", snap.Peak)
	}
	if snap.RMS[0] != 0 || snap.RMS[1] != 0 {
		t.Errorf("RMS = %v, want [0 0]", snap.RMS)
	}
	if snap.Generation != 1 {
		t.Errorf("Generation = %d, want 1", snap.Generation)
	}
	if snap.SampleRate != 48000 || snap.Channels != 2 {
		t.Errorf("sampleRate/channels = %d/%d, want 48000/2", snap.SampleRate, snap.Channels)
	}
}

func TestAudioMeter_FullScaleSinePeakAndRMS(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
	sineL := func(i int) float32 { return float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000)) }
	for chunk := 0; chunk < 18; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{sineL, sineL})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	if math.Abs(float64(snap.Peak[0])-1.0) > 0.05 || math.Abs(float64(snap.Peak[1])-1.0) > 0.05 {
		t.Errorf("Peak = %v, want ~[1.0 1.0]", snap.Peak)
	}
	if math.Abs(float64(snap.RMS[0])-0.707) > 0.02 || math.Abs(float64(snap.RMS[1])-0.707) > 0.02 {
		t.Errorf("RMS = %v, want ~[0.707 0.707]", snap.RMS)
	}
	if math.Abs(float64(snap.PhaseCorr)-1.0) > 0.02 {
		t.Errorf("PhaseCorr = %f, want ~+1", snap.PhaseCorr)
	}
}

func TestAudioMeter_OutOfPhasePhaseCorr(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
	sineL := func(i int) float32 { return float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000)) }
	sineR := func(i int) float32 { return float32(-math.Sin(2 * math.Pi * 1000 * float64(i) / 48000)) }
	for chunk := 0; chunk < 18; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{sineL, sineR})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	if math.Abs(float64(snap.PhaseCorr)-(-1.0)) > 0.02 {
		t.Errorf("PhaseCorr = %f, want ~-1", snap.PhaseCorr)
	}
}

func TestAudioMeter_PeakDecayExponential(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
	hit := func(i int) float32 {
		if i == 0 {
			return 1.0
		}
		return 0
	}
	pcm := makeStereoPCM(1, [2]func(int) float32{hit, hit})
	m.Observe(pcm, 2, 48000)
	silent := func(i int) float32 { return 0 }
	for chunk := 0; chunk < 60; chunk++ {
		zeros := makeStereoPCM(800, [2]func(int) float32{silent, silent})
		m.Observe(zeros, 2, 48000)
	}
	snap := m.AudioScopes()
	if math.Abs(float64(snap.Peak[0])-0.1) > 0.02 {
		t.Errorf("Peak after 1 s decay = %f, want ~0.1 (-20 dB)", snap.Peak[0])
	}
}

func TestAudioMeter_GoniometerCapturesRecentSamples(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
	sineL := func(i int) float32 { return float32(math.Sin(2 * math.Pi * 100 * float64(i) / 48000)) }
	sineR := func(i int) float32 { return float32(math.Cos(2 * math.Pi * 100 * float64(i) / 48000)) }
	for chunk := 0; chunk < 4; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{sineL, sineR})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	if len(snap.Goniometer) != 256 {
		t.Fatalf("Goniometer length = %d, want 256", len(snap.Goniometer))
	}
	for i, pair := range snap.Goniometer {
		if pair[0] < -1.0 || pair[0] > 1.0 || pair[1] < -1.0 || pair[1] > 1.0 {
			t.Errorf("Goniometer[%d] = %v, out of range", i, pair)
		}
	}
}
