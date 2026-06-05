package dataplane

import (
	"math"
	"sync/atomic"
	"time"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane/audiodsp"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/dataplane/fft"
)

const (
	audioTargetHzDefault = 30
	audioBytesPerSample  = 2
	// audioFFTSize is the spectrum FFT length. At 48 kHz this is a
	// 5.86 Hz bin width (≈171 ms window), fine enough that every one of
	// the 32 log-spaced bands from 20 Hz–20 kHz maps to a distinct FFT
	// bin set. A 1024-pt FFT (46.9 Hz/bin) collapsed the lowest ~8 bands
	// onto a single bin, rendering them bit-identical.
	audioFFTSize       = 8192
	audioSpectrumBands = 32
	audioGoniometerSize      = 256
	audioGoniometerWindowSec = 0.050
	audioPhaseWindowSec      = 0.300
	audioLUFSWindowSec       = 3.0
	audioLUFSSilenceFloor    = -100.0
	audioSpectrumSentinel    = -90.0
	audioPeakDecayDBPerSec   = -20.0
)

// AudioScopeSnapshot is the lock-free snapshot of audio-analysis values
// published by AudioMeter. Returned pointer is read-only — callers MUST
// NOT mutate the pointee.
type AudioScopeSnapshot struct {
	Generation    uint64
	SampleRate    int
	Channels      int
	Peak          [2]float32
	RMS           [2]float32
	PhaseCorr     float32
	LUFSShort     float32
	SpectrumBands [audioSpectrumBands]float32
	Goniometer    [audioGoniometerSize][2]float32
	PublishedAt   time.Time
}

// AudioMeter computes audio-analysis values from PCM chunks observed
// inline on the field-tick goroutine. Single-producer; many readers via
// AudioScopes(). Construction allocates the running-state buffers; Observe
// is allocation-free on non-publish ticks and allocates one snapshot
// pointer on publish ticks.
type AudioMeter struct {
	generation uint64
	sampleRate int
	channels   int
	snapshot   atomic.Pointer[AudioScopeSnapshot]

	// Bresenham cadence: Observe adds samplesInChunk * targetHz; publish
	// when publishAccum >= sampleRate, then subtract sampleRate.
	//
	// targetHz is a test seam:
	//   - production:     targetHz = 30
	//   - no-publish run: targetHz = 0 (accumulator never advances)
	//
	// forcePublishEveryObserve is a separate test seam. It publishes at
	// most once at the end of an Observe call, regardless of chunk size.
	targetHz                 int
	publishAccum             int
	forcePublishEveryObserve bool

	state              audioMeterState
	peakDecayPerSample float32
	gonioDecimStep     int
	gonioStepCount     int

	// LUFS coefficients (Task 4 will populate)
	kPreCoeffs  audiodsp.Biquad
	kHighCoeffs audiodsp.Biquad

	// FFT scratch (Task 5 will populate)
	fftPlan     *fft.Plan
	fftWindowed [audioFFTSize]float32
	fftOut      []complex64
	hannWindow  []float32
}

type audioMeterState struct {
	// peak per channel with exponential decay
	peak [2]float32

	// RMS sliding window (300 ms): ring of squared samples per channel
	rmsRingL, rmsRingR   []float32
	rmsHead              int
	rmsCount             int
	rmsSumSqL, rmsSumSqR float64

	// Phase correlation accumulators (300 ms window)
	phaseSumL, phaseSumR       float64
	phaseSumLR                 float64
	phaseSumSqL, phaseSumSqR   float64
	phaseRingL, phaseRingR     []float32
	phaseHead                  int
	phaseCount                 int

	// FFT input ring (1024 samples, mono mix-down)
	fftRing [audioFFTSize]float32
	fftHead int

	// Goniometer ring (256 decimated stereo pairs)
	gonio     [audioGoniometerSize][2]float32
	gonioHead int

	// Last published spectrum (kept across non-publish ticks)
	lastSpectrum [audioSpectrumBands]float32

	// LUFS K-weighting biquad state per channel (Task 4 will populate)
	kPreL, kPreR   audiodsp.BiquadState
	kHighL, kHighR audiodsp.BiquadState

	// LUFS sliding window (3 s) — K-weighted squared samples per channel
	lufsRingL, lufsRingR     []float32
	lufsHead                 int
	lufsCount                int
	lufsSumSqL, lufsSumSqR   float64
}

// NewAudioMeter constructs an AudioMeter for the given session generation
// and audio format. AudioMeter assumes sampleRate is constant for its
// lifetime; source switches that change sampleRate must construct a new
// AudioMeter.
func NewAudioMeter(generation uint64, sampleRate, channels int) *AudioMeter {
	m := &AudioMeter{
		generation: generation,
		sampleRate: sampleRate,
		channels:   channels,
		targetHz:   audioTargetHzDefault,
	}
	m.peakDecayPerSample = float32(math.Pow(10, audioPeakDecayDBPerSec/20.0/float64(sampleRate)))

	rmsLen := int(audioPhaseWindowSec * float64(sampleRate))
	m.state.rmsRingL = make([]float32, rmsLen)
	m.state.rmsRingR = make([]float32, rmsLen)
	m.state.phaseRingL = make([]float32, rmsLen)
	m.state.phaseRingR = make([]float32, rmsLen)

	lufsLen := int(audioLUFSWindowSec * float64(sampleRate))
	m.state.lufsRingL = make([]float32, lufsLen)
	m.state.lufsRingR = make([]float32, lufsLen)

	m.kPreCoeffs = kWeightingPreFilter(sampleRate)
	m.kHighCoeffs = kWeightingHighShelf(sampleRate)
	m.state.kPreL.SetCoeffs(m.kPreCoeffs)
	m.state.kPreR.SetCoeffs(m.kPreCoeffs)
	m.state.kHighL.SetCoeffs(m.kHighCoeffs)
	m.state.kHighR.SetCoeffs(m.kHighCoeffs)

	m.gonioDecimStep = int(audioGoniometerWindowSec*float64(sampleRate)) / audioGoniometerSize
	if m.gonioDecimStep < 1 {
		m.gonioDecimStep = 1
	}

	m.fftPlan = fft.NewPlan(audioFFTSize)
	m.fftOut = make([]complex64, audioFFTSize/2+1)
	m.hannWindow = m.fftPlan.Hann()
	return m
}

// Observe processes one PCM chunk on the field-tick goroutine. pcm is
// s16le interleaved; len(pcm) MUST equal samplesInChunk*channels*2.
// Allocation-free on non-publish ticks.
func (m *AudioMeter) Observe(pcm []byte, channels, sampleRate int) {
	if channels != m.channels || sampleRate != m.sampleRate {
		panic("dataplane: AudioMeter.Observe sampleRate/channels mismatch")
	}
	samplesInChunk := len(pcm) / (audioBytesPerSample * channels)
	if samplesInChunk == 0 {
		return
	}

	for i := 0; i < samplesInChunk; i++ {
		var l, r float32
		l = int16ToFloat(pcm, i*channels*2)
		if channels == 2 {
			r = int16ToFloat(pcm, (i*channels+1)*2)
		} else {
			r = l
		}

		// Peak: instant attack, exponential release
		m.state.peak[0] *= m.peakDecayPerSample
		if a := abs32(l); a > m.state.peak[0] {
			m.state.peak[0] = a
		}
		m.state.peak[1] *= m.peakDecayPerSample
		if a := abs32(r); a > m.state.peak[1] {
			m.state.peak[1] = a
		}

		// RMS sliding window
		old := m.state.rmsRingL[m.state.rmsHead]
		m.state.rmsSumSqL += float64(l)*float64(l) - float64(old)*float64(old)
		m.state.rmsRingL[m.state.rmsHead] = l
		old = m.state.rmsRingR[m.state.rmsHead]
		m.state.rmsSumSqR += float64(r)*float64(r) - float64(old)*float64(old)
		m.state.rmsRingR[m.state.rmsHead] = r
		m.state.rmsHead = (m.state.rmsHead + 1) % len(m.state.rmsRingL)
		if m.state.rmsCount < len(m.state.rmsRingL) {
			m.state.rmsCount++
		}

		// Phase correlation: maintain Σl, Σr, Σlr, Σl², Σr² over window
		oldL := m.state.phaseRingL[m.state.phaseHead]
		oldR := m.state.phaseRingR[m.state.phaseHead]
		m.state.phaseSumL += float64(l - oldL)
		m.state.phaseSumR += float64(r - oldR)
		m.state.phaseSumLR += float64(l)*float64(r) - float64(oldL)*float64(oldR)
		m.state.phaseSumSqL += float64(l)*float64(l) - float64(oldL)*float64(oldL)
		m.state.phaseSumSqR += float64(r)*float64(r) - float64(oldR)*float64(oldR)
		m.state.phaseRingL[m.state.phaseHead] = l
		m.state.phaseRingR[m.state.phaseHead] = r
		m.state.phaseHead = (m.state.phaseHead + 1) % len(m.state.phaseRingL)
		if m.state.phaseCount < len(m.state.phaseRingL) {
			m.state.phaseCount++
		}

		// FFT input ring (mono mix-down)
		mono := (l + r) / 2
		m.state.fftRing[m.state.fftHead] = mono
		m.state.fftHead = (m.state.fftHead + 1) % audioFFTSize

		// Goniometer decimation
		m.gonioStepCount = (m.gonioStepCount + 1) % m.gonioDecimStep
		if m.gonioStepCount == 0 {
			m.state.gonio[m.state.gonioHead] = [2]float32{l, r}
			m.state.gonioHead = (m.state.gonioHead + 1) % audioGoniometerSize
		}

		// LUFS K-weighting: cascade pre-filter then RLB high-pass, then
		// integrate squared output over the 3 s sliding window per channel.
		kL := m.state.kHighL.Process(m.state.kPreL.Process(float64(l)))
		kR := m.state.kHighR.Process(m.state.kPreR.Process(float64(r)))
		oldKL := m.state.lufsRingL[m.state.lufsHead]
		oldKR := m.state.lufsRingR[m.state.lufsHead]
		m.state.lufsSumSqL += kL*kL - float64(oldKL)*float64(oldKL)
		m.state.lufsSumSqR += kR*kR - float64(oldKR)*float64(oldKR)
		m.state.lufsRingL[m.state.lufsHead] = float32(kL)
		m.state.lufsRingR[m.state.lufsHead] = float32(kR)
		m.state.lufsHead = (m.state.lufsHead + 1) % len(m.state.lufsRingL)
		if m.state.lufsCount < len(m.state.lufsRingL) {
			m.state.lufsCount++
		}
	}

	// Bresenham cadence
	if m.forcePublishEveryObserve {
		m.publish()
		return
	}
	if m.targetHz <= 0 {
		return
	}
	m.publishAccum += samplesInChunk * m.targetHz
	for m.publishAccum >= m.sampleRate {
		m.publishAccum -= m.sampleRate
		m.publish()
	}
}

// AudioScopes returns the latest published snapshot or nil if no
// snapshot has been published yet. Lock-free. Read-only.
func (m *AudioMeter) AudioScopes() *AudioScopeSnapshot {
	return m.snapshot.Load()
}

// publish builds a fresh snapshot from current running state and
// atomically stores it. Called inline from Observe on publish ticks.
func (m *AudioMeter) publish() {
	snap := &AudioScopeSnapshot{
		Generation:  m.generation,
		SampleRate:  m.sampleRate,
		Channels:    m.channels,
		Peak:        m.state.peak,
		PublishedAt: time.Now(),
	}
	if m.state.rmsCount > 0 {
		snap.RMS[0] = float32(math.Sqrt(m.state.rmsSumSqL / float64(m.state.rmsCount)))
		snap.RMS[1] = float32(math.Sqrt(m.state.rmsSumSqR / float64(m.state.rmsCount)))
	}
	if m.state.phaseCount > 1 {
		n := float64(m.state.phaseCount)
		num := m.state.phaseSumLR - (m.state.phaseSumL*m.state.phaseSumR)/n
		denL := m.state.phaseSumSqL - (m.state.phaseSumL*m.state.phaseSumL)/n
		denR := m.state.phaseSumSqR - (m.state.phaseSumR*m.state.phaseSumR)/n
		den := math.Sqrt(denL * denR)
		if den > 1e-12 {
			snap.PhaseCorr = float32(num / den)
		}
	}
	for i := 0; i < audioGoniometerSize; i++ {
		idx := (m.state.gonioHead + i) % audioGoniometerSize
		snap.Goniometer[i] = m.state.gonio[idx]
	}
	// Spectrum: run FFT on current ring, then copy into snapshot.
	m.computeSpectrum()
	snap.SpectrumBands = m.state.lastSpectrum
	// LUFS short-term (BS.1770-4): L_K = -0.691 + 10 * log10(Σ G_ch * meanSquare_ch).
	// G_L = G_R = 1.0 for stereo; mono is a single-channel sum.
	if m.state.lufsCount >= len(m.state.lufsRingL) {
		var totalPower float64
		meanSqL := m.state.lufsSumSqL / float64(m.state.lufsCount)
		if m.channels == 1 {
			totalPower = meanSqL
		} else {
			meanSqR := m.state.lufsSumSqR / float64(m.state.lufsCount)
			totalPower = meanSqL + meanSqR
		}
		if totalPower > 1e-12 {
			snap.LUFSShort = float32(-0.691 + 10*math.Log10(totalPower))
		} else {
			snap.LUFSShort = audioLUFSSilenceFloor
		}
	} else {
		snap.LUFSShort = audioLUFSSilenceFloor
	}
	m.snapshot.Store(snap)
}

// computeSpectrum runs one FFT pass on the current FFT input ring,
// updating m.state.lastSpectrum with band-summed power in dBFS. Called
// on publish ticks only.
func (m *AudioMeter) computeSpectrum() {
	// Copy ring in arrival order (oldest first) into windowed scratch
	for i := 0; i < audioFFTSize; i++ {
		idx := (m.state.fftHead + i) % audioFFTSize
		m.fftWindowed[i] = m.state.fftRing[idx] * m.hannWindow[i]
	}

	// Detect all-zero input (silence) and emit sentinel without running FFT
	allZero := true
	for _, v := range m.fftWindowed {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		for i := range m.state.lastSpectrum {
			m.state.lastSpectrum[i] = audioSpectrumSentinel
		}
		return
	}

	bins := m.fftPlan.Real(m.fftWindowed[:], m.fftOut)
	nyquist := float64(m.sampleRate) / 2

	// Compute Hann-corrected per-bin power (one-sided: ×2 for bins 1..N/2-1).
	// Normalization: 1 / (N² × hannEnergyGain / 2) where hannEnergyGain =
	// sum(w²)/N = 3/8 for length-N Hann. A bin-centered full-scale sine
	// after Hann windowing produces ≈-2 dBFS at the peak band (not exactly
	// 0 dBFS), because the per-bin coherent-gain correction is folded into
	// this single energy normalization rather than a separate amplitude-
	// correction step. The test tolerance (band peak > -3 dBFS) accepts
	// this. If exact 0 dBFS is ever required, multiply normFactor by an
	// additional coherent-gain factor (~1.5) and re-tune sentinel tests.
	hannEnergyGain := 3.0 / 8.0
	normFactor := 1.0 / (float64(audioFFTSize) * float64(audioFFTSize) * hannEnergyGain / 2)

	for band := 0; band < audioSpectrumBands; band++ {
		lo := 20.0 * math.Pow(1000, float64(band)/32)
		hi := 20.0 * math.Pow(1000, float64(band+1)/32)
		if lo >= nyquist {
			m.state.lastSpectrum[band] = audioSpectrumSentinel
			continue
		}
		loBin := int(lo * float64(audioFFTSize) / float64(m.sampleRate))
		hiBin := int(hi * float64(audioFFTSize) / float64(m.sampleRate))
		if loBin < 1 {
			loBin = 1
		}
		if hiBin > audioFFTSize/2 {
			hiBin = audioFFTSize / 2
		}
		if hiBin <= loBin {
			hiBin = loBin + 1
		}
		var power float64
		for k := loBin; k < hiBin; k++ {
			re := float64(real(bins[k]))
			im := float64(imag(bins[k]))
			power += (re*re + im*im) * 2 // one-sided spectrum
		}
		power *= normFactor
		if power < 1e-9 {
			m.state.lastSpectrum[band] = audioSpectrumSentinel
		} else {
			db := 10 * math.Log10(power)
			if db < audioSpectrumSentinel {
				db = audioSpectrumSentinel
			}
			m.state.lastSpectrum[band] = float32(db)
		}
	}
}

func int16ToFloat(pcm []byte, offset int) float32 {
	lo := uint16(pcm[offset])
	hi := uint16(pcm[offset+1])
	s := int16(lo | hi<<8)
	return float32(s) / 32768
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

// kWeightingPreFilter computes the BS.1770-4 pre-filter (high-frequency
// shelving boost at ~1.68 kHz, +4 dB) for the given sample rate using
// RBJ Audio EQ Cookbook biquad design.
func kWeightingPreFilter(sampleRate int) audiodsp.Biquad {
	const (
		f0     = 1681.974450955533
		q      = 0.7071752369554196
		gainDB = 3.999843853973347
	)
	return audiodsp.DesignHighShelf(float64(sampleRate), f0, q, gainDB)
}

// kWeightingHighShelf computes the BS.1770-4 RLB filter (high-pass at
// ~38 Hz) for the given sample rate.
func kWeightingHighShelf(sampleRate int) audiodsp.Biquad {
	const (
		f0 = 38.13547087602444
		q  = 0.5003270373238773
	)
	return audiodsp.DesignHighpass(float64(sampleRate), f0, q)
}
