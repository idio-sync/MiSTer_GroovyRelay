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

func TestAudioMeter_LUFSShortMonoCalibration(t *testing.T) {
	// BS.1770-4: 1 kHz sine at -20 dBFS RMS mono → -20.7 LUFS ±0.5
	m := NewAudioMeter(1, 48000, 1)
	m.forcePublishEveryObserve = true
	// -20 dBFS RMS sine: amplitude = sqrt(2) * 10^(-20/20) = 0.1414
	const amp = 0.1414213562
	sine := func(i int) float32 { return float32(amp * math.Sin(2*math.Pi*1000*float64(i)/48000)) }
	// Feed > 3 s to fill the LUFS window
	for chunk := 0; chunk < 180; chunk++ { // 180 * 800 = 144000 samples = 3 s
		pcm := makeMonoPCM(800, sine)
		m.Observe(pcm, 1, 48000)
	}
	snap := m.AudioScopes()
	want := -20.7
	if math.Abs(float64(snap.LUFSShort)-want) > 0.5 {
		t.Errorf("LUFSShort = %f, want %f ± 0.5", snap.LUFSShort, want)
	}
}

func TestAudioMeter_LUFSDualMonoStereoIsLouder(t *testing.T) {
	// Dual-mono stereo at -20 dBFS RMS → ~3 dB louder than mono (channel power sum)
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
	const amp = 0.1414213562
	sine := func(i int) float32 { return float32(amp * math.Sin(2*math.Pi*1000*float64(i)/48000)) }
	for chunk := 0; chunk < 180; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{sine, sine})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	want := -17.7 // -20.7 + 3.0
	if math.Abs(float64(snap.LUFSShort)-want) > 0.7 {
		t.Errorf("LUFSShort dual-mono = %f, want %f ± 0.7", snap.LUFSShort, want)
	}
}

func TestAudioMeter_LUFSSilenceReturnsSentinel(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
	silent := func(i int) float32 { return 0 }
	for chunk := 0; chunk < 180; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{silent, silent})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	if snap.LUFSShort != audioLUFSSilenceFloor {
		t.Errorf("LUFSShort silence = %f, want %f", snap.LUFSShort, audioLUFSSilenceFloor)
	}
}

func TestAudioMeter_SpectrumSilenceIsSentinel(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
	silent := func(i int) float32 { return 0 }
	for chunk := 0; chunk < 2; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{silent, silent})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	for i, v := range snap.SpectrumBands {
		if v != audioSpectrumSentinel {
			t.Errorf("SpectrumBands[%d] = %f, want sentinel %f", i, v, audioSpectrumSentinel)
		}
	}
}

func TestAudioMeter_SpectrumBinCenteredSinePeak(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
	const freq = 3000.0
	sine := func(i int) float32 { return float32(math.Sin(2 * math.Pi * freq * float64(i) / 48000)) }
	// Feed more than one full FFT window (audioFFTSize samples) so the
	// analysis ring is fully primed with the tone before the peak check.
	for chunk := 0; chunk < 16; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{sine, sine})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	targetBand := 0
	for i := 0; i < audioSpectrumBands; i++ {
		lo := 20.0 * math.Pow(1000, float64(i)/32)
		hi := 20.0 * math.Pow(1000, float64(i+1)/32)
		if freq >= lo && freq < hi {
			targetBand = i
			break
		}
	}
	if snap.SpectrumBands[targetBand] < -3 {
		t.Errorf("target band %d = %f dBFS, want > -3 (peak)", targetBand, snap.SpectrumBands[targetBand])
	}
	for i, v := range snap.SpectrumBands {
		if i == targetBand {
			continue
		}
		if v > -10 {
			t.Errorf("non-target band %d = %f dBFS, want < -10 (suppressed)", i, v)
		}
	}
}

// TestAudioMeter_SpectrumLowBandsResolveDistinctly is the regression test
// for the "leftmost 8 bars always the same level" bug: a single low tone
// must light one band, not smear identically across the bottom of the
// spectrum. With a 1024-pt FFT @48 kHz (46.9 Hz/bin) the eight lowest
// log-bands all collapse onto FFT bin 1 and read bit-identical.
func TestAudioMeter_SpectrumLowBandsResolveDistinctly(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
	const freq = 50.0
	sine := func(i int) float32 { return float32(math.Sin(2 * math.Pi * freq * float64(i) / 48000)) }
	// Feed well over one full FFT window so the analysis ring is primed
	// with the tone regardless of FFT size.
	for chunk := 0; chunk < 16; chunk++ {
		pcm := makeStereoPCM(800, [2]func(int) float32{sine, sine})
		m.Observe(pcm, 2, 48000)
	}
	snap := m.AudioScopes()
	// The eight lowest bands must not be a single flat level. A 50 Hz tone
	// lands in exactly one of them; the others should be clearly quieter.
	lo8 := snap.SpectrumBands[:8]
	mn, mx := lo8[0], lo8[0]
	for _, v := range lo8 {
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
	}
	if mx-mn < 10 {
		t.Errorf("lowest 8 bands span only %.1f dB (%v): they collapse to one level", mx-mn, lo8)
	}
}

// makeMonoPCM is a helper paralleling makeStereoPCM but for 1-channel input.
func makeMonoPCM(frames int, gen func(i int) float32) []byte {
	buf := make([]byte, frames*2)
	for i := 0; i < frames; i++ {
		v := gen(i)
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}
		s := int16(v * 32767)
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}

func TestAudioMeter_NoPublishHotPathZeroAllocs(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 0 // disable publishes entirely
	pcm := makeStereoPCM(800, [2]func(int) float32{
		func(i int) float32 { return 0.5 },
		func(i int) float32 { return -0.5 },
	})
	// Warm the meter so RMS/phase rings are populated
	for i := 0; i < 30; i++ {
		m.Observe(pcm, 2, 48000)
	}
	allocs := testing.AllocsPerRun(100, func() {
		m.Observe(pcm, 2, 48000)
	})
	if allocs != 0 {
		t.Errorf("AllocsPerRun = %f, want 0 (no-publish hot path)", allocs)
	}
}

func TestAudioMeter_AlwaysPublishExactlyOneAlloc(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.forcePublishEveryObserve = true
	pcm := makeStereoPCM(800, [2]func(int) float32{
		func(i int) float32 { return 0.5 },
		func(i int) float32 { return -0.5 },
	})
	// Warm
	for i := 0; i < 30; i++ {
		m.Observe(pcm, 2, 48000)
	}
	allocs := testing.AllocsPerRun(100, func() {
		m.Observe(pcm, 2, 48000)
	})
	if allocs != 1 {
		t.Errorf("AllocsPerRun = %f, want exactly 1 (snapshot pointer)", allocs)
	}
}

func TestAudioMeter_BresenhamCadenceNTSC(t *testing.T) {
	// 48 kHz @ 59.94 Hz field rate: 800 frames per chunk.
	// 10 s of audio = 600 chunks → expect 300 publishes ± 1.
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 30
	pcm := makeStereoPCM(800, [2]func(int) float32{
		func(i int) float32 { return 0 },
		func(i int) float32 { return 0 },
	})
	count := 0
	prevSnap := m.AudioScopes()
	for i := 0; i < 600; i++ {
		m.Observe(pcm, 2, 48000)
		curr := m.AudioScopes()
		if curr != prevSnap {
			count++
			prevSnap = curr
		}
	}
	if count < 299 || count > 301 {
		t.Errorf("NTSC publish count over 10 s = %d, want 300 ± 1", count)
	}
}

func TestAudioMeter_BresenhamCadencePAL(t *testing.T) {
	// 48 kHz @ 50 Hz field rate: 960 frames per chunk.
	// 10 s of audio = 500 chunks → expect 300 publishes ± 1.
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 30
	pcm := makeStereoPCM(960, [2]func(int) float32{
		func(i int) float32 { return 0 },
		func(i int) float32 { return 0 },
	})
	count := 0
	prevSnap := m.AudioScopes()
	for i := 0; i < 500; i++ {
		m.Observe(pcm, 2, 48000)
		curr := m.AudioScopes()
		if curr != prevSnap {
			count++
			prevSnap = curr
		}
	}
	if count < 299 || count > 301 {
		t.Errorf("PAL publish count over 10 s = %d, want 300 ± 1", count)
	}
}

func TestAudioMeter_ConcurrentReadersUnderRace(t *testing.T) {
	m := NewAudioMeter(1, 48000, 2)
	m.targetHz = 30
	pcm := makeStereoPCM(800, [2]func(int) float32{
		func(i int) float32 { return 0.3 },
		func(i int) float32 { return -0.3 },
	})
	done := make(chan struct{})
	// 5 concurrent readers
	for r := 0; r < 5; r++ {
		go func() {
			for i := 0; i < 10000; i++ {
				snap := m.AudioScopes()
				if snap != nil {
					_ = snap.Peak[0]
					_ = snap.SpectrumBands[0]
				}
			}
			done <- struct{}{}
		}()
	}
	// Single writer (this goroutine)
	for i := 0; i < 1000; i++ {
		m.Observe(pcm, 2, 48000)
	}
	for r := 0; r < 5; r++ {
		<-done
	}
}
