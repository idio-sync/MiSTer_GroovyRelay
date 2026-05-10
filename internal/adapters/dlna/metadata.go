package dlna

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// metadata.go owns DIDL-Lite parsing for CurrentURIMetaData. UPnP
// control points pass a DIDL-Lite XML fragment alongside the URI on
// SetAVTransportURI; the renderer extracts title/duration/protocolInfo
// to drive seek advertisement and query-action responses.
//
// Spec: docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md
// §SetAVTransportURI line 349 ("parse enough DIDL-Lite to capture
// title, duration, class, and protocolInfo") and §Query Actions /
// Seek derivation (lines 366-370).
//
// Why we parse so little: DIDL-Lite is a sprawling schema (covers
// books, photos, EPG entries, DVR recordings, etc.). The bridge only
// renders direct HTTP video/audio URLs and only consumes four
// fields. Parsing more would invite XML schema drift between
// controllers without buying behavior. The parser is deliberately
// lenient — missing optional fields produce zero values rather than
// errors, since real-world controllers omit fields freely.

// didlMaxBodyBytes caps the DIDL XML decoder's reader. Same body cap
// convention as soapMaxBodyBytes — a DIDL-Lite blob in
// CurrentURIMetaData should never approach this size; legitimate
// controllers send a few hundred bytes to a couple KiB. 64 KiB
// defends against an attacker padding the metadata to exhaust memory
// during XML decode.
const didlMaxBodyBytes = 64 * 1024

// ErrInvalidMetadata wraps malformed-XML failures so callers can
// errors.Is and map to a SOAP fault if needed. P2.3 will treat this
// as 402 Invalid Args (the metadata is bad) — the URI itself may
// still be valid, and a strict reading of the spec would let us
// accept the URI and ignore broken metadata, but rejecting bad
// metadata up-front is the safer default for a Phase 2 ship.
var ErrInvalidMetadata = errors.New("dlna: invalid DIDL-Lite metadata")

// DIDLMetadata captures the subset of DIDL-Lite fields the adapter
// uses for transport state and capability decisions.
//
// Title and Class are decorative (used only for log lines and the
// future operator-facing status panel); Duration and ProtocolInfo
// are load-bearing for seek-advertisement decisions in P2.3+.
type DIDLMetadata struct {
	// Title is <dc:title>. Empty when absent.
	Title string

	// Duration is res@duration parsed as HH:MM:SS or HH:MM:SS.FFF.
	// Zero means unknown — either the field was absent, the format
	// was unparseable, or the duration was non-positive. Zero
	// duration suppresses the Seek advertisement (spec line 368).
	Duration time.Duration

	// Class is <upnp:class>, e.g. "object.item.videoItem.movie". Used
	// only for log/diag context in v1; future versions might key
	// content-type-specific behavior off it (e.g. audio-only fast
	// path).
	Class string

	// ProtocolInfo is res@protocolInfo, e.g. "http-get:*:video/mp4:*".
	// The MIME-type third field is what spec line 319 cross-checks
	// against SinkProtocolInfo: "Container/MIME advertised in metadata
	// that is not in SinkProtocolInfo" returns 714. P2.3 / P2.4
	// performs that check; the parser's job is just to surface the
	// raw string.
	ProtocolInfo string
}

// didlEnvelope is the outer DIDL-Lite element. We parse only the
// fields we use — encoding/xml's permissive defaults (ignoring
// unknown elements/attributes) means real-world DIDL fragments with
// extra fields decode cleanly.
type didlEnvelope struct {
	XMLName xml.Name   `xml:"DIDL-Lite"`
	Items   []didlItem `xml:"item"`
}

// didlItem is one <item> entry. Real CurrentURIMetaData blobs
// usually carry exactly one item; if a controller sends multiple,
// we use the first.
type didlItem struct {
	// Title under the dc: namespace. xml.Name with Space lets
	// encoding/xml match the element regardless of the namespace
	// prefix the sender used (xmlns:dc vs xmlns:foo where foo is the
	// dc URI).
	Title    string        `xml:"http://purl.org/dc/elements/1.1/ title"`
	Class    string        `xml:"urn:schemas-upnp-org:metadata-1-0/upnp/ class"`
	// Resources are <res> elements with the duration/protocolInfo
	// attributes we care about. Multiple <res> children are common
	// (e.g. one per encoding bitrate); we pick the first valid one.
	Resources []didlResource `xml:"res"`
}

// didlResource is a single <res> element. Only the two attributes
// we use are decoded; the URL inside the element body is ignored
// (the renderer trusts CurrentURI from SetAVTransportURI as the
// authoritative URL — DIDL res URLs can disagree with CurrentURI in
// the wild and the spec is explicit that CurrentURI wins).
type didlResource struct {
	Duration     string `xml:"duration,attr"`
	ProtocolInfo string `xml:"protocolInfo,attr"`
}

// parseDIDLMetadata parses a CurrentURIMetaData string. Empty or
// whitespace-only input returns a zero DIDLMetadata and a nil error
// — controllers legitimately omit metadata and that's not an error
// at this layer.
//
// Malformed XML returns (zero, error wrapping ErrInvalidMetadata).
// Other shapes (well-formed XML that doesn't match DIDL-Lite, or
// DIDL-Lite with no <item>) decode to a zero DIDLMetadata and nil
// — the parser is lenient, not strict.
//
// Multiple <res> elements: we walk Resources and use the first one
// that has either a duration or a protocolInfo attribute set. This
// matches the common controller pattern of emitting a "main" <res>
// followed by alternate-bitrate <res>es, where the main one is
// listed first.
func parseDIDLMetadata(metaXML string) (DIDLMetadata, error) {
	trimmed := strings.TrimSpace(metaXML)
	if trimmed == "" {
		return DIDLMetadata{}, nil
	}

	// Bound the decoder's reader. Use io.LimitReader rather than
	// http.MaxBytesReader because we don't have an http.ResponseWriter
	// here and we don't need MaxBytesReader's connection-close
	// behavior — silently truncating an oversized blob is fine and
	// the resulting parse will fail with a clear "unexpected EOF"
	// that maps to ErrInvalidMetadata.
	dec := xml.NewDecoder(io.LimitReader(strings.NewReader(trimmed), didlMaxBodyBytes))

	var env didlEnvelope
	if err := dec.Decode(&env); err != nil {
		return DIDLMetadata{}, fmt.Errorf("%w: %v", ErrInvalidMetadata, err)
	}

	if len(env.Items) == 0 {
		// Well-formed XML but no <item> — return zero metadata.
		// Controllers sometimes send an empty DIDL-Lite envelope to
		// "clear" the previous metadata; treat as a no-op.
		return DIDLMetadata{}, nil
	}
	item := env.Items[0]

	// Pick the first resource that has any field we care about. If
	// no <res> has either attribute, primaryRes stays zero — which
	// produces a zero Duration and empty ProtocolInfo on output, the
	// same shape we'd return for "no res elements at all." Real
	// controllers list the primary stream first and put alternates
	// (thumbnails, lower-bitrate copies) after, so this rule gives
	// them what they meant to say.
	var primaryRes didlResource
	for _, r := range item.Resources {
		if r.Duration != "" || r.ProtocolInfo != "" {
			primaryRes = r
			break
		}
	}

	return DIDLMetadata{
		Title:        strings.TrimSpace(item.Title),
		Duration:     parseDIDLDuration(primaryRes.Duration),
		Class:        strings.TrimSpace(item.Class),
		ProtocolInfo: strings.TrimSpace(primaryRes.ProtocolInfo),
	}, nil
}

// parseDIDLDuration parses UPnP duration time format. Two shapes are
// accepted:
//
//   "HH:MM:SS"          — integer seconds
//   "HH:MM:SS.FFF"      — fractional seconds, milliseconds precision
//
// Returns 0 on any parse failure or a non-positive result. Per spec
// "treat zero as unknown" — callers compare Duration > 0 to decide
// whether to advertise Seek.
//
// The lenient handling here is deliberate: a controller that sends a
// malformed duration shouldn't fail the whole SetAVTransportURI; we
// just lose the seekability hint and rely on the post-probe duration
// from core.Status() instead (spec line 369).
func parseDIDLDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// Reject leading sign — UPnP duration is unsigned. A leading "-"
	// is the only obvious way to inject a negative; "+1:00:00" is
	// also non-canonical but we tolerate it via stdlib parsing
	// downstream returning 0 (so it just degrades to "unknown").
	if strings.HasPrefix(s, "-") {
		return 0
	}

	// Split on the first dot for the optional fractional seconds.
	// Splitting on "." rather than parsing time.Duration via
	// time.ParseDuration keeps the format strict — "1h30m" would
	// otherwise parse and we don't want that.
	dotIdx := strings.IndexByte(s, '.')
	mainPart := s
	fracPart := ""
	if dotIdx >= 0 {
		mainPart = s[:dotIdx]
		fracPart = s[dotIdx+1:]
	}

	parts := strings.Split(mainPart, ":")
	if len(parts) != 3 {
		return 0
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 {
		return 0
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m >= 60 {
		return 0
	}
	sec, err := strconv.Atoi(parts[2])
	if err != nil || sec < 0 || sec >= 60 {
		return 0
	}
	total := time.Duration(h)*time.Hour +
		time.Duration(m)*time.Minute +
		time.Duration(sec)*time.Second

	if fracPart != "" {
		// Pad or truncate to 3 digits (milliseconds). UPnP says
		// milliseconds explicitly; we accept fewer or more digits
		// gracefully so a controller sending "00:00:30.5" parses as
		// 500 ms rather than zero.
		switch {
		case len(fracPart) > 3:
			fracPart = fracPart[:3]
		case len(fracPart) < 3:
			fracPart = fracPart + strings.Repeat("0", 3-len(fracPart))
		}
		ms, err := strconv.Atoi(fracPart)
		if err != nil || ms < 0 {
			return 0
		}
		total += time.Duration(ms) * time.Millisecond
	}

	if total <= 0 {
		// Zero or negative result is treated as "unknown" per spec.
		return 0
	}
	return total
}
