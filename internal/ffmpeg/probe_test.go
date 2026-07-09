package ffmpeg

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const probeHelperEnv = "MISTER_GROOVY_PROBE_HELPER"
const probeHelperPIDFileEnv = "MISTER_GROOVY_PROBE_HELPER_PID_FILE"
const probeHelperHoldPipeFor = 15 * time.Second
const probeLeakedPipeReturnLimit = 5 * time.Second

func TestMain(m *testing.M) {
	switch os.Getenv(probeHelperEnv) {
	case "leaky-parent":
		runProbeLeakyParentHelper()
		os.Exit(0)
	default:
		os.Exit(m.Run())
	}
}

func runProbeLeakyParentHelper() {
	cmd := holdPipeCommand()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		os.Exit(2)
	}
	if pidFile := os.Getenv(probeHelperPIDFileEnv); pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	}
	_, _ = os.Stdout.Write([]byte(`{"streams":[],"format":{}}`))
}

func holdPipeCommand() *exec.Cmd {
	seconds := strconv.Itoa(int(probeHelperHoldPipeFor.Seconds()))
	if runtime.GOOS == "windows" {
		return exec.Command("cmd.exe", "/c", "ping -n "+seconds+" 127.0.0.1 >NUL")
	}
	return exec.Command("sleep", seconds)
}

func TestParseProbeOutput_ProgressiveVideoWithAudio(t *testing.T) {
	// Mimics ffprobe JSON for a 1920x1080 23.976p h264 clip with stereo AAC.
	raw := []byte(`{
		"streams": [
			{"codec_type":"video","width":1920,"height":1080,"field_order":"progressive","r_frame_rate":"24000/1001"},
			{"codec_type":"audio","sample_rate":"48000"}
		],
		"format":{"duration":"12.345"}
	}`)
	got, err := parseProbeOutput(raw)
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if got.Width != 1920 || got.Height != 1080 {
		t.Errorf("dims: %dx%d", got.Width, got.Height)
	}
	if math.Abs(got.FrameRate-23.976) > 0.01 {
		t.Errorf("frame rate: %f", got.FrameRate)
	}
	if got.Interlaced {
		t.Error("expected progressive")
	}
	if got.AudioRate != 48000 {
		t.Errorf("audio rate: %d", got.AudioRate)
	}
	if math.Abs(got.Duration-12.345) > 0.001 {
		t.Errorf("duration: %f", got.Duration)
	}
}

func TestParseProbeOutput_Interlaced(t *testing.T) {
	for _, fo := range []string{"tt", "bb", "tb", "bt"} {
		raw := []byte(`{"streams":[{"codec_type":"video","width":720,"height":480,"field_order":"` + fo + `","r_frame_rate":"30000/1001"}]}`)
		got, err := parseProbeOutput(raw)
		if err != nil {
			t.Fatalf("%s: %v", fo, err)
		}
		if !got.Interlaced {
			t.Errorf("field_order %q should be flagged interlaced", fo)
		}
	}
}

func TestParseProbeOutput_MalformedJSON(t *testing.T) {
	if _, err := parseProbeOutput([]byte("not json")); err == nil {
		t.Error("expected error for malformed json")
	}
}

func TestParseFrameRate(t *testing.T) {
	cases := map[string]float64{
		"30000/1001": 30000.0 / 1001.0,
		"24/1":       24,
		"":           0,
		"bogus":      0,
	}
	for in, want := range cases {
		got := parseFrameRate(in)
		if math.Abs(got-want) > 0.001 {
			t.Errorf("parseFrameRate(%q) = %f, want %f", in, got, want)
		}
	}
}

func TestProbe_ReturnsPromptlyWhenOutputPipeRemainsOpen(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "helper.pid")
	t.Setenv(probeHelperEnv, "leaky-parent")
	t.Setenv(probeHelperPIDFileEnv, pidFile)
	t.Cleanup(func() { killProbeHelper(t, pidFile) })

	start := time.Now()
	_, err := Probe(context.Background(), os.Args[0], "ignored", MediaInputPolicy{})
	elapsed := time.Since(start)
	if elapsed > probeLeakedPipeReturnLimit {
		t.Fatalf("Probe returned after %s; want it bounded when ffprobe leaves output pipes open", elapsed)
	}
	if err == nil {
		t.Fatal("expected Probe to return an error for a leaked ffprobe output pipe")
	}
}

func killProbeHelper(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Kill()
	_, _ = proc.Wait()
}

// TestProbe_ZeroPolicyArgvUnchanged confirms that an empty MediaInputPolicy
// produces the historical ffprobe argv shape: -v error, -print_format json,
// -show_streams, -show_format, then the URL — with NO policy flags injected.
// Run via Probe pointing at a missing binary so we never spawn anything;
// the test asserts the argv shape via the err path instead by reconstructing
// the same args slice the production code builds.
func TestProbe_ZeroPolicyArgvUnchanged(t *testing.T) {
	// Build the args slice the same way Probe does, with the zero-value
	// policy. This is a white-box check on the assembled argv.
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_streams", "-show_format",
	}
	args = MediaInputPolicy{}.Apply(args)
	args = append(args, "http://example/clip.mp4")
	want := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_streams", "-show_format",
		"http://example/clip.mp4",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("zero-policy argv:\n got %v\nwant %v", args, want)
	}
}

// TestProbe_NonZeroPolicyAppendsFlagsBeforeURL verifies the policy's argv
// flags appear AFTER the show-streams/format args and BEFORE the URL — so
// ffprobe applies them as input options on the next input it opens.
func TestProbe_NonZeroPolicyAppendsFlagsBeforeURL(t *testing.T) {
	policy := MediaInputPolicy{
		ProtocolWhitelist: []string{"file", "http", "https", "tcp", "tls", "crypto"},
		DisableReconnect:  true,
		RWTimeout:         5 * time.Second,
	}
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_streams", "-show_format",
	}
	args = policy.Apply(args)
	args = append(args, "http://example/clip.mp4")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-protocol_whitelist file,http,https,tcp,tls,crypto",
		"-reconnect 0",
		"-reconnect_at_eof 0",
		"-reconnect_streamed 0",
		"-reconnect_on_network_error 0",
		"-rw_timeout 5000000",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in argv: %s", want, joined)
		}
	}
	// URL must be the last argv element.
	if args[len(args)-1] != "http://example/clip.mp4" {
		t.Errorf("URL must be the last argv element, got %s", joined)
	}
	// Policy flags must precede the URL.
	urlIdx := strings.LastIndex(joined, "http://example/clip.mp4")
	whitelistIdx := strings.Index(joined, "-protocol_whitelist")
	if whitelistIdx < 0 || whitelistIdx >= urlIdx {
		t.Errorf("-protocol_whitelist must precede URL: %s", joined)
	}
}

func TestProbeInputURLAppliesPolicyBeforeURL(t *testing.T) {
	cmd := probeCommand("ffprobe", ProbeInputSpec{
		URL: "http://127.0.0.1:32500/internal/aux-proxy/?aux_token=probe",
		Policy: MediaInputPolicy{
			ProtocolWhitelist: []string{"http", "tcp"},
			DisableReconnect:  true,
			RWTimeout:         2 * time.Second,
		},
	})
	assertArgsContainSubsequence(t, cmd.Args, []string{
		"-protocol_whitelist", "http,tcp",
		"-reconnect", "0",
		"-reconnect_at_eof", "0",
		"-reconnect_streamed", "0",
		"-reconnect_on_network_error", "0",
		"-rw_timeout", "2000000",
		"http://127.0.0.1:32500/internal/aux-proxy/?aux_token=probe",
	})
}

func TestProbeInputURLAppliesHeadersBeforeURL(t *testing.T) {
	cmd := probeCommand("ffprobe", ProbeInputSpec{
		URL: "https://media.example/audio.m4a",
		Headers: map[string]string{
			"Referer":    "https://sound.example/",
			"User-Agent": "GroovyRelay",
		},
		Policy: MediaInputPolicy{
			ProtocolWhitelist: []string{"http", "https", "tcp", "tls"},
			BlockedHeaders:    []string{"Referer"},
		},
	})
	assertArgsContainSubsequence(t, cmd.Args, []string{
		"-protocol_whitelist", "http,https,tcp,tls",
		"-headers", "User-Agent: GroovyRelay\r\n",
		"https://media.example/audio.m4a",
	})
	for _, arg := range cmd.Args {
		if strings.Contains(arg, "Referer:") {
			t.Fatalf("blocked Referer header reached ffprobe args: %v", cmd.Args)
		}
	}
}

func TestProbeInputCaptureUsesStructuredArgs(t *testing.T) {
	cmd := probeCommand("ffprobe", ProbeInputSpec{
		Capture: CaptureInputSpec{
			Enabled:         true,
			Format:          "dshow",
			Device:          `audio=Line In (USB Audio Device)`,
			SampleRate:      48000,
			Channels:        2,
			ThreadQueueSize: 64,
			AnalyzeDuration: 100 * time.Millisecond,
			ProbeSize:       32768,
		},
	})
	assertArgsContainSubsequence(t, cmd.Args, []string{
		"-thread_queue_size", "64",
		"-f", "dshow",
		"-sample_rate", "48000",
		"-channels", "2",
		"-analyzeduration", "100000",
		"-probesize", "32768",
		"-i", `audio=Line In (USB Audio Device)`,
	})
}

func TestProbeInputCaptureUsesDefaultBoundedTimeout(t *testing.T) {
	if got := probeTimeout(ProbeInputSpec{
		Capture: CaptureInputSpec{Enabled: true},
	}); got != 3*time.Second {
		t.Fatalf("capture default probe timeout = %s, want 3s", got)
	}
	if got := probeTimeout(ProbeInputSpec{URL: "http://example.test/audio.wav"}); got != 0 {
		t.Fatalf("URL probe timeout = %s, want caller context", got)
	}
	if got := probeTimeout(ProbeInputSpec{
		Capture: CaptureInputSpec{Enabled: true},
		Timeout: 500 * time.Millisecond,
	}); got != 500*time.Millisecond {
		t.Fatalf("explicit probe timeout = %s, want 500ms", got)
	}
}

// TestProbe_LiveFixture generates a 1-second synthetic test clip with ffmpeg
// then probes it. Skipped if ffmpeg / ffprobe are not findable.
func TestParseProbeOutput_MeterMetadata(t *testing.T) {
	raw := []byte(`{
		"streams": [
			{
				"codec_type": "video",
				"codec_name": "h264",
				"width": 720,
				"height": 480,
				"field_order": "tt",
				"r_frame_rate": "30000/1001",
				"sample_aspect_ratio": "8:9",
				"display_aspect_ratio": "4:3",
				"bit_rate": "1800000"
			},
			{
				"codec_type": "audio",
				"codec_name": "aac",
				"sample_rate": "48000",
				"channels": 2,
				"bit_rate": "128000"
			}
		],
		"format": {
			"duration": "60.25",
			"bit_rate": "2100000"
		}
	}`)
	got, err := parseProbeOutput(raw)
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if got.VideoCodec != "h264" || got.AudioCodec != "aac" {
		t.Fatalf("codecs = video %q audio %q", got.VideoCodec, got.AudioCodec)
	}
	if got.AudioChannels != 2 {
		t.Fatalf("AudioChannels = %d, want 2", got.AudioChannels)
	}
	if got.SampleAspectRatioNum != 8 || got.SampleAspectRatioDen != 9 {
		t.Fatalf("sample aspect = %d:%d, want 8:9", got.SampleAspectRatioNum, got.SampleAspectRatioDen)
	}
	if got.DisplayAspectRatioNum != 4 || got.DisplayAspectRatioDen != 3 {
		t.Fatalf("display aspect = %d:%d, want 4:3", got.DisplayAspectRatioNum, got.DisplayAspectRatioDen)
	}
	if got.VideoBitrateBPS != 1800000 || got.AudioBitrateBPS != 128000 || got.FormatBitrateBPS != 2100000 {
		t.Fatalf("bitrates = video %d audio %d format %d", got.VideoBitrateBPS, got.AudioBitrateBPS, got.FormatBitrateBPS)
	}
}

func TestParseProbeOutput_MissingMeterMetadataStaysZero(t *testing.T) {
	raw := []byte(`{"streams":[{"codec_type":"video","width":640,"height":360}],"format":{}}`)
	got, err := parseProbeOutput(raw)
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if got.VideoCodec != "" || got.AudioCodec != "" || got.AudioChannels != 0 {
		t.Fatalf("unexpected non-zero metadata: %+v", got)
	}
	if got.SampleAspectRatioNum != 0 || got.DisplayAspectRatioNum != 0 {
		t.Fatalf("unexpected aspect metadata: %+v", got)
	}
	if got.VideoBitrateBPS != 0 || got.AudioBitrateBPS != 0 || got.FormatBitrateBPS != 0 {
		t.Fatalf("unexpected bitrate metadata: %+v", got)
	}
}

func TestProbe_LiveFixture(t *testing.T) {
	ffmpegBin := findFFBinary("ffmpeg")
	ffprobeBin := findFFBinary("ffprobe")
	if ffmpegBin == "" || ffprobeBin == "" {
		t.Skip("ffmpeg/ffprobe not findable")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "fixture.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpegBin,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=24",
		"-pix_fmt", "yuv420p", "-y", clip,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg fixture generation failed (%v): %s", err, out)
	}
	if _, err := os.Stat(clip); err != nil {
		t.Skipf("fixture not written: %v", err)
	}

	// Use the full ffprobe path too (since `ffprobe` isn't in Windows PATH).
	probeCmd := exec.CommandContext(ctx, ffprobeBin,
		"-v", "error",
		"-print_format", "json",
		"-show_streams", "-show_format",
		clip,
	)
	out, err := probeCmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	res, err := parseProbeOutput(out)
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if res.Width != 320 || res.Height != 240 {
		t.Errorf("dims: %dx%d", res.Width, res.Height)
	}
	if math.Abs(res.FrameRate-24) > 0.01 {
		t.Errorf("frame rate: %f", res.FrameRate)
	}
	if res.Interlaced {
		t.Error("testsrc is progressive")
	}
	if res.Duration < 0.5 || res.Duration > 2.0 {
		t.Errorf("duration suspect: %f", res.Duration)
	}
}
