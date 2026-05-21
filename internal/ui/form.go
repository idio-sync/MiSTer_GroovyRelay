package ui

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// FormErrors is a keyed bag of per-field parse errors. Keys match
// FieldDef.Key (dotted) so the template can hook each error onto
// its input. Distinct from adapters.FieldErrors because FormErrors
// covers syntactic failures (bad int, missing required) while
// adapters.FieldErrors covers semantic validation (port ranges,
// enum membership).
type FormErrors map[string]string

func (fe FormErrors) Error() string {
	if len(fe) == 0 {
		return ""
	}
	parts := ""
	for k, v := range fe {
		if parts != "" {
			parts += "; "
		}
		parts += fmt.Sprintf("%s: %s", k, v)
	}
	return parts
}

// stripExperimentalSuffix removes the " (experimental)" suffix that the
// settings UI dropdown appends to PAL preset names, so the persisted
// config stores the bare preset name (e.g. "PAL_576i", not
// "PAL_576i (experimental)").
func stripExperimentalSuffix(s string) string {
	const suffix = " (experimental)"
	if strings.HasSuffix(s, suffix) {
		return strings.TrimSuffix(s, suffix)
	}
	return s
}

// parseBridgeForm translates a POSTed form into a BridgeConfig.
// Returns FormErrors on any parse failure (bad integer, etc.);
// validation (port ranges, enum membership) happens downstream via
// Sectioned.Validate so error text stays consistent with boot-time
// validation.
func parseBridgeForm(form url.Values) (config.BridgeConfig, error) {
	errs := FormErrors{}
	out := config.BridgeConfig{}

	out.MiSTer.Host = form.Get("mister.host")
	out.MiSTer.Port = parseIntField(form, "mister.port", errs)
	out.MiSTer.SourcePort = parseIntField(form, "mister.source_port", errs)
	out.MiSTer.SSHUser = form.Get("mister.ssh_user")
	out.MiSTer.SSHPassword = form.Get("mister.ssh_password")
	out.HostIP = form.Get("host_ip")

	out.Video.Modeline = stripExperimentalSuffix(form.Get("video.modeline"))
	out.Video.InterlaceFieldOrder = form.Get("video.interlace_field_order")
	out.Video.AspectMode = form.Get("video.aspect_mode")
	out.Video.RGBMode = "rgb888" // v1 locked; not user-editable
	out.Video.LZ4Enabled = parseBoolField(form, "video.lz4_enabled")
	out.Video.DeltaLZ4Enabled = parseBoolField(form, "video.delta_lz4_enabled")

	out.Audio.SampleRate = parseIntField(form, "audio.sample_rate", errs)
	out.Audio.Channels = parseIntField(form, "audio.channels", errs)

	out.Visualizer.Mode = form.Get("visualizer.mode")

	out.UI.HTTPPort = parseIntField(form, "ui.http_port", errs)
	out.DataDir = form.Get("data_dir")
	out.FFmpegPath = form.Get("ffmpeg_path")
	out.FFprobePath = form.Get("ffprobe_path")
	out.YTDLPPath = form.Get("ytdlp_path")

	out.Logging.Debug = parseBoolField(form, "logging.debug")

	out.HLSBuffer.Enabled = parseBoolField(form, "hls_buffer.enabled")
	out.HLSBuffer.LiveEdgeSegments = parseIntField(form, "hls_buffer.live_edge_segments", errs)
	out.HLSBuffer.StartSegments = parseIntField(form, "hls_buffer.start_segments", errs)
	out.HLSBuffer.MaxCachedSegments = parseIntField(form, "hls_buffer.max_cached_segments", errs)
	out.HLSBuffer.MaxCacheBytes = parseInt64Field(form, "hls_buffer.max_cache_bytes", errs)
	out.HLSBuffer.MaxPlaylistBytes = parseInt64Field(form, "hls_buffer.max_playlist_bytes", errs)
	out.HLSBuffer.MaxSegmentBytes = parseInt64Field(form, "hls_buffer.max_segment_bytes", errs)
	out.HLSBuffer.SegmentTimeoutSeconds = parseIntField(form, "hls_buffer.segment_timeout_seconds", errs)
	out.HLSBuffer.PlaylistTimeoutSeconds = parseIntField(form, "hls_buffer.playlist_timeout_seconds", errs)
	out.HLSBuffer.MaxVariantHeight = parseIntField(form, "hls_buffer.max_variant_height", errs)
	out.HLSBuffer.StaleCacheReapHours = parseIntField(form, "hls_buffer.stale_cache_reap_hours", errs)

	if len(errs) > 0 {
		return out, errs
	}
	return out, nil
}

func ensureHLSBufferFormFields(form url.Values, cur config.HLSBufferConfig) {
	for key := range form {
		if strings.HasPrefix(key, "hls_buffer.") {
			return
		}
	}
	form.Set("hls_buffer.enabled", strconv.FormatBool(cur.Enabled))
	form.Set("hls_buffer.live_edge_segments", strconv.Itoa(cur.LiveEdgeSegments))
	form.Set("hls_buffer.start_segments", strconv.Itoa(cur.StartSegments))
	form.Set("hls_buffer.max_cached_segments", strconv.Itoa(cur.MaxCachedSegments))
	form.Set("hls_buffer.max_cache_bytes", strconv.FormatInt(cur.MaxCacheBytes, 10))
	form.Set("hls_buffer.max_playlist_bytes", strconv.FormatInt(cur.MaxPlaylistBytes, 10))
	form.Set("hls_buffer.max_segment_bytes", strconv.FormatInt(cur.MaxSegmentBytes, 10))
	form.Set("hls_buffer.segment_timeout_seconds", strconv.Itoa(cur.SegmentTimeoutSeconds))
	form.Set("hls_buffer.playlist_timeout_seconds", strconv.Itoa(cur.PlaylistTimeoutSeconds))
	form.Set("hls_buffer.max_variant_height", strconv.Itoa(cur.MaxVariantHeight))
	form.Set("hls_buffer.stale_cache_reap_hours", strconv.Itoa(cur.StaleCacheReapHours))
}

// parseIntField reads form[key] as int. On parse error, records an
// error in errs and returns zero. Empty string is treated as "not
// provided" and also records an error — every int field in the
// bridge schema is required.
func parseIntField(form url.Values, key string, errs FormErrors) int {
	raw := form.Get(key)
	if raw == "" {
		errs[key] = "required"
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		errs[key] = fmt.Sprintf("not a whole number: %q", raw)
		return 0
	}
	return n
}

func parseInt64Field(form url.Values, key string, errs FormErrors) int64 {
	raw := form.Get(key)
	if raw == "" {
		errs[key] = "required"
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		errs[key] = fmt.Sprintf("not a whole number: %q", raw)
		return 0
	}
	return n
}

// parseBoolField reads an HTML checkbox. Missing = false; present
// with any non-empty value = true. Never returns an error (checkboxes
// can't be malformed). Explicit "false" / "0" map to false so
// scripted callers can opt out without omitting the key.
func parseBoolField(form url.Values, key string) bool {
	v := form.Get(key)
	if v == "" {
		return false
	}
	if v == "false" || v == "0" {
		return false
	}
	return true
}
