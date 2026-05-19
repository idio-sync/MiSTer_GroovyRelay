package hlsbuffer

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

type PlaylistKind int

const (
	PlaylistMedia PlaylistKind = iota + 1
	PlaylistMaster
)

type Segment struct {
	URI      string
	Duration time.Duration
	Sequence int64
	// Discontinuity is true when the source playlist placed an
	// #EXT-X-DISCONTINUITY tag immediately before this segment's
	// #EXTINF. ffmpeg needs the marker to handle PTS jumps at ad
	// breaks; without it, the decoder may stall or glitch.
	Discontinuity bool
}

type Variant struct {
	URI       string
	Bandwidth int
	Width     int
	Height    int
	Codecs    string
}

type Playlist struct {
	Kind        PlaylistKind
	Target      time.Duration
	MediaSeq    int64
	Segments    []Segment
	Variants    []Variant
	Unsupported string
}

func ParsePlaylist(body []byte) (Playlist, error) {
	lines := strings.Split(string(body), "\n")
	p := Playlist{}
	seenHeader := false
	expectVariantURI := false
	var pendingVariant Variant
	var pendingDuration *time.Duration
	pendingDiscontinuity := false
	nextSequence := int64(0)

	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if trimmed == "" {
			continue
		}
		if !seenHeader {
			if trimmed != "#EXTM3U" {
				return Playlist{}, fmt.Errorf("hls playlist: missing #EXTM3U header")
			}
			seenHeader = true
			continue
		}

		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(trimmed, "#") {
			if err := rejectUnsupportedTag(trimmed, upper); err != nil {
				return Playlist{}, err
			}
			switch {
			case strings.HasPrefix(upper, "#EXT-X-TARGETDURATION:"):
				target, err := parseHLSDuration(strings.TrimSpace(trimmed[len("#EXT-X-TARGETDURATION:"):]))
				if err != nil {
					return Playlist{}, fmt.Errorf("hls playlist: invalid #EXT-X-TARGETDURATION: %w", err)
				}
				p.Target = target
			case strings.HasPrefix(upper, "#EXT-X-MEDIA-SEQUENCE:"):
				seq, err := strconv.ParseInt(strings.TrimSpace(trimmed[len("#EXT-X-MEDIA-SEQUENCE:"):]), 10, 64)
				if err != nil {
					return Playlist{}, fmt.Errorf("hls playlist: invalid #EXT-X-MEDIA-SEQUENCE: %w", err)
				}
				p.MediaSeq = seq
				nextSequence = seq
			case strings.HasPrefix(upper, "#EXTINF:"):
				dur, err := parseEXTINFDuration(trimmed)
				if err != nil {
					return Playlist{}, err
				}
				pendingDuration = &dur
			case upper == "#EXT-X-DISCONTINUITY":
				pendingDiscontinuity = true
			case strings.HasPrefix(upper, "#EXT-X-STREAM-INF:"):
				v, err := parseStreamInf(trimmed)
				if err != nil {
					return Playlist{}, err
				}
				pendingVariant = v
				expectVariantURI = true
			}
			continue
		}

		if expectVariantURI {
			pendingVariant.URI = trimmed
			p.Variants = append(p.Variants, pendingVariant)
			pendingVariant = Variant{}
			expectVariantURI = false
			continue
		}
		if pendingDuration == nil {
			return Playlist{}, fmt.Errorf("hls playlist: media URI %q without preceding #EXTINF", trimmed)
		}
		if isAudioOnlySegmentURI(trimmed) {
			return Playlist{}, fmt.Errorf("hls playlist: audio-only media segments are not supported: %s", trimmed)
		}
		p.Segments = append(p.Segments, Segment{
			URI:           trimmed,
			Duration:      *pendingDuration,
			Sequence:      nextSequence,
			Discontinuity: pendingDiscontinuity,
		})
		nextSequence++
		pendingDuration = nil
		pendingDiscontinuity = false
	}

	if !seenHeader {
		return Playlist{}, fmt.Errorf("hls playlist: missing #EXTM3U header")
	}
	if expectVariantURI {
		return Playlist{}, fmt.Errorf("hls playlist: #EXT-X-STREAM-INF missing variant URI")
	}
	if len(p.Variants) > 0 && len(p.Segments) > 0 {
		return Playlist{}, fmt.Errorf("hls playlist: mixed master and media playlist entries are not supported")
	}
	switch {
	case len(p.Variants) > 0:
		p.Kind = PlaylistMaster
	case len(p.Segments) > 0:
		p.Kind = PlaylistMedia
	default:
		return Playlist{}, fmt.Errorf("hls playlist: no media segments or variants found")
	}
	return p, nil
}

func rejectUnsupportedTag(line, upper string) error {
	unsupportedPrefixes := []string{
		"#EXT-X-KEY",
		"#EXT-X-BYTERANGE",
		"#EXT-X-MAP",
		"#EXT-X-PART",
		"#EXT-X-PRELOAD-HINT",
	}
	for _, prefix := range unsupportedPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return fmt.Errorf("hls playlist: unsupported tag %s", prefix)
		}
	}
	if strings.HasPrefix(upper, "#EXT-X-MEDIA:") {
		return fmt.Errorf("hls playlist: alternate media tag is not supported: %s", line)
	}
	if strings.HasPrefix(upper, "#EXT-X-STREAM-INF:") {
		attrs := parseAttrList(line[len("#EXT-X-STREAM-INF:"):])
		if _, ok := attrs["URI"]; ok {
			return fmt.Errorf("hls playlist: #EXT-X-STREAM-INF URI attribute is not supported")
		}
		return nil
	}
	if hasURIAttribute(upper) {
		return fmt.Errorf("hls playlist: unsupported URI-bearing tag: %s", line)
	}
	return nil
}

func parseEXTINFDuration(line string) (time.Duration, error) {
	raw := strings.TrimSpace(line[len("#EXTINF:"):])
	if idx := strings.Index(raw, ","); idx >= 0 {
		raw = raw[:idx]
	}
	dur, err := parseHLSDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("hls playlist: invalid #EXTINF duration: %w", err)
	}
	return dur, nil
}

func parseHLSDuration(raw string) (time.Duration, error) {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func parseStreamInf(line string) (Variant, error) {
	attrs := parseAttrList(line[len("#EXT-X-STREAM-INF:"):])
	var v Variant
	if raw := attrs["BANDWIDTH"]; raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Variant{}, fmt.Errorf("invalid BANDWIDTH %q: %w", raw, err)
		}
		v.Bandwidth = n
	}
	if raw := attrs["RESOLUTION"]; raw != "" {
		parts := strings.Split(raw, "x")
		if len(parts) != 2 {
			return Variant{}, fmt.Errorf("invalid RESOLUTION %q", raw)
		}
		w, err := strconv.Atoi(parts[0])
		if err != nil {
			return Variant{}, fmt.Errorf("invalid RESOLUTION width %q: %w", parts[0], err)
		}
		h, err := strconv.Atoi(parts[1])
		if err != nil {
			return Variant{}, fmt.Errorf("invalid RESOLUTION height %q: %w", parts[1], err)
		}
		v.Width = w
		v.Height = h
	}
	v.Codecs = attrs["CODECS"]
	return v, nil
}

func parseAttrList(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range splitHLSAttrs(raw) {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(k))
		val := strings.TrimSpace(v)
		val = strings.Trim(val, `"`)
		out[key] = val
	}
	return out
}

func splitHLSAttrs(raw string) []string {
	var parts []string
	start := 0
	inQuote := false
	for i, r := range raw {
		switch r {
		case '"':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				parts = append(parts, strings.TrimSpace(raw[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(raw[start:]))
	return parts
}

func hasURIAttribute(upper string) bool {
	return strings.Contains(upper, "URI=")
}

func isAudioOnlySegmentURI(uri string) bool {
	ext := strings.ToLower(path.Ext(strings.Split(uri, "?")[0]))
	switch ext {
	case ".aac", ".mp3", ".m4a":
		return true
	default:
		return false
	}
}
