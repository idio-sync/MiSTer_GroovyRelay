package dlna

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// set_avtransport_uri.go owns the SetAVTransportURI SOAP action handler.
//
// Spec: docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md
// §SetAVTransportURI (lines 328-349) and §Common Action Rules (the SOAP
// fault mapping table at lines 310-326).
//
// Phase 2 P2.3 scope: validate the incoming URI via the P2.2 validator,
// parse the DIDL-Lite metadata, sanity-check the metadata's protocolInfo
// MIME against the v1 SinkProtocolInfo set, then store the validated
// final URL + parsed metadata under a.mu. P2.3 explicitly does NOT
// invoke autoplay_on_set_uri or build a core.SessionRequest — those
// land in P2.4 with the Play / Stop handlers.

// setAVTransportURIArgs is the IN-argument set for the SetAVTransportURI
// SOAP action per UPnP AVTransport:1. The SOAP envelope parser flattens
// the action's child elements to a name→value map; this struct exists
// so the handler reads the three argument names from named fields rather
// than map lookups (less typo surface, same map[string]string under the
// hood).
type setAVTransportURIArgs struct {
	InstanceID         string
	CurrentURI         string
	CurrentURIMetaData string
}

// setAVTransportURITimeout bounds the validator's network I/O. Picked
// to match validatorRequestTimeout * (validatorMaxRedirects+1) ≈ 20s so
// a slow chain doesn't pin a single SOAP request indefinitely. The
// caller may also pass an r.Context()-derived context that could be
// shorter; we take the smaller of the two via context.WithTimeout.
const setAVTransportURITimeout = validatorRequestTimeout * time.Duration(validatorMaxRedirects+1)

var prepareHLSPlaybackForAdapter = prepareHLSPlayback

// handleSetAVTransportURI implements the SetAVTransportURI SOAP action.
// The dispatcher in handleAVTransportSOAP routes here after parsing the
// envelope and asserting InstanceID==0. We re-validate InstanceID
// defensively so this handler is correct in isolation if the dispatch
// table ever changes.
//
// Per spec the handler runs four conceptual steps:
//  1. Admission check — adapter must be enabled and not have an
//     in-flight DLNA start. Either rejection returns 701 and the
//     handler must NOT mutate any adapter state (loadedURI must read
//     the same value before and after).
//  2. URL prevalidation — runs validateMediaURL with the policy
//     derived from cfg.AllowPublicSourceURLs. Errors map to UPnP
//     faults via the table in the spec.
//  3. Metadata parse — lenient. Empty metadata is fine; malformed XML
//     is 402.
//  4. Metadata MIME check — if metadata declared a protocolInfo, the
//     MIME third field must be in sinkProtocolInfoEntries; otherwise
//  714. The renderer can't honor a content type it doesn't
//     advertise.
//
// Only after all four succeed do we acquire a.mu and store the new
// state (loadedURI = validated.FinalURL, loadedMeta = parsed,
// loadedMetaRaw = original XML, lastError = ""). Validation errors
// store a redacted lastError via setLastError under mu (mutating
// lastError is part of the "we observed this failure" surface that
// query actions use to set TransportStatus=ERROR_OCCURRED, distinct
// from the spec's "leave loadedURI unchanged" rule for busy-reject).
//
// Locking discipline: a.mu is NEVER held across validateMediaURL (it
// performs HTTP I/O) per CLAUDE.md. The handler reads cfg.Enabled,
// cfg.AllowPublicSourceURLs, and startInFlight under a brief mu
// acquisition, releases the lock, runs validation, then re-acquires
// for the state mutation.
func (a *Adapter) handleSetAVTransportURI(w http.ResponseWriter, r *http.Request, args setAVTransportURIArgs) {
	// InstanceID validation. Spec §Common Action Rules: only InstanceID=0
	// is supported; other values return 718.
	if args.InstanceID != "" && args.InstanceID != "0" {
		writeSOAPFault(w, upnpErrInvalidInstanceID)
		return
	}

	// Snapshot admission inputs under mu. Releasing the lock before
	// network I/O is mandatory (CLAUDE.md).
	a.mu.Lock()
	enabled := a.cfg.Enabled
	allowPublic := a.cfg.AllowPublicSourceURLs
	inFlight := a.startInFlight
	activeRef := a.currentRef
	a.mu.Unlock()

	// Disabled adapter: reject with 701 (transition not available).
	// Rationale: 401 (Invalid Action) implies the action does not exist
	// on the service, which is incorrect — SetAVTransportURI is always
	// in the SCPD. 701 communicates "this renderer is currently
	// unavailable for transport control," which matches "the operator
	// stopped the adapter." We deliberately do NOT touch lastError on
	// this path: the rejection is a configuration state, not a
	// validation failure observation.
	if !enabled {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// Spec §SetAVTransportURI line 332: while a DLNA start is in
	// flight we reject with 701 and leave loadedURI unchanged. The
	// adapter does NOT block waiting for the in-flight start to
	// finish — controllers retry on 701, and blocking would hold an
	// HTTP goroutine across an unbounded core.Manager call.
	if inFlight || activeRef != "" {
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}

	// URL prevalidation. The validator chases redirects under a
	// bounded context, re-classifies every Location target's resolved
	// IP, and returns the final URL. Pass the request context so a
	// controller-side cancel propagates through the validator's HTTP
	// client. context.WithTimeout caps the chain wall-clock budget.
	policy := PolicyPrivateOnly
	if allowPublic {
		policy = PolicyAllowPublic
	}
	ctx, cancel := context.WithTimeout(r.Context(), setAVTransportURITimeout)
	defer cancel()

	validated, err := validateMediaURL(ctx, args.CurrentURI, policy)
	if err != nil {
		// Map typed validator errors to UPnP fault codes per spec table.
		//   ErrInvalidURL        -> 402 Invalid Args
		//   ErrSchemeNotAllowed  -> 716 Resource not found
		//   ErrAddressNotAllowed -> 716
		//   ErrTooManyRedirects  -> 716
		//   ErrRedirectFetchFail -> 716
		//   any other            -> 716 (defensive default — the validator
		//                            should always wrap a typed sentinel,
		//                            but if it doesn't we still hide
		//                            internal-error specifics from the
		//                            controller).
		var code upnpErrorCode
		switch {
		case errors.Is(err, ErrInvalidURL):
			code = upnpErrInvalidArgs
		case errors.Is(err, ErrSchemeNotAllowed),
			errors.Is(err, ErrAddressNotAllowed),
			errors.Is(err, ErrTooManyRedirects),
			errors.Is(err, ErrRedirectFetchFail):
			code = upnpErrResourceNotFound
		default:
			code = upnpErrResourceNotFound
		}
		// Record the failure for query-action visibility (TransportStatus
		// flips to ERROR_OCCURRED). Redact the URL aggressively — DLNA
		// metadata can carry credentials in CurrentURI userinfo or query
		// strings, and lastError is surfaced to operators via the UI
		// status panel in later phases. Don't include the underlying
		// validator error string here either; sinks could reproduce
		// internal hostnames or IPs that the operator doesn't want
		// echoed back to the controller via GetTransportInfo.
		a.setLastError("SetAVTransportURI rejected URI " + redactURL(args.CurrentURI))
		a.publishAVTransportLastChange()
		writeSOAPFault(w, code)
		return
	}

	// Parse metadata. parseDIDLMetadata is lenient — empty input
	// returns a zero DIDLMetadata with nil error, well-formed but
	// non-DIDL XML returns zero with nil. Only malformed XML errors,
	// and we never echo the raw error text (DIDL XML may include
	// arbitrary controller-supplied data).
	parsed, err := parseDIDLMetadata(args.CurrentURIMetaData)
	if err != nil {
		a.setLastError("SetAVTransportURI rejected: metadata parse failed")
		a.publishAVTransportLastChange()
		writeSOAPFault(w, upnpErrInvalidArgs)
		return
	}

	// MIME / protocolInfo cross-check. If the metadata declared a
	// protocolInfo, the third colon-delimited field is the MIME type
	// (per UPnP A_ARG_TYPE_ProtocolInfo). We only render content types
	// in sinkProtocolInfoEntries. HLS is accepted only after the manifest
	// and children are validated and cached locally below; other
	// unsupported containers (DASH MPD, etc.) are rejected with 714.
	// Empty / missing protocolInfo skips the check; the renderer trusts
	// post-probe behavior to surface unsupported codecs later.
	mime := mimeFromProtocolInfo(parsed.ProtocolInfo)
	isHLS := isHLSMIME(mime) || isHLSURLPath(validated.FinalURL)
	if parsed.ProtocolInfo != "" && !protocolInfoMatchesSink(parsed.ProtocolInfo) {
		a.setLastError("SetAVTransportURI rejected: unsupported protocolInfo")
		a.publishAVTransportLastChange()
		writeSOAPFault(w, upnpErrIllegalMIME)
		return
	}
	var playbackURI string
	var hlsCleanup func() error
	canSeek := true
	if isHLS {
		hls, err := prepareHLSPlaybackForAdapter(ctx, validated.FinalURL, policy)
		if err != nil {
			a.setLastError("SetAVTransportURI rejected: HLS validation failed")
			a.publishAVTransportLastChange()
			writeSOAPFault(w, upnpErrResourceNotFound)
			return
		}
		playbackURI = hls.PlaybackURI
		hlsCleanup = hls.Cleanup
		canSeek = false
	} else {
		playbackURI = validated.FinalURL
	}

	// All checks passed. Store the validated URI + metadata under mu,
	// but re-run admission first: validation performs network I/O, so
	// Play/autoplay may have started while the lock was dropped.
	// FinalURL is what the data plane needs (post-redirect) — passing
	// the original CurrentURI would let the URL-validator's redirect
	// chase be wasted, since FFmpeg would re-fetch from the original
	// host and potentially get a different redirect target than what
	// the validator approved.
	a.mu.Lock()
	if !a.cfg.Enabled || a.startInFlight || a.currentRef != "" {
		a.mu.Unlock()
		if hlsCleanup != nil {
			_ = hlsCleanup()
		}
		writeSOAPFault(w, upnpErrTransitionNotAvail)
		return
	}
	oldCleanup := a.loadedHLSCleanup
	a.loadedURI = validated.FinalURL
	a.loadedPlaybackURI = playbackURI
	a.loadedHLSCleanup = hlsCleanup
	a.loadedCanSeek = canSeek
	a.loadedMeta = parsed
	a.loadedMetaRaw = args.CurrentURIMetaData
	a.lastError = ""
	autoplay := a.cfg.AutoplayOnSetURI
	a.mu.Unlock()
	if oldCleanup != nil {
		_ = oldCleanup()
	}
	a.publishAVTransportLastChange()

	// autoplay_on_set_uri (spec line 330 + line 395): "compatibility
	// mode for controllers that do not send Play." When enabled, kick
	// off the same fresh-start path Play uses. This is BEST EFFORT:
	// SetAVTransportURI itself returns SOAP success even if the
	// autoplay's StartSession fails — the URI is still stored, an
	// explicit Play afterward will retry, and the failure is visible
	// via lastError on the next GetTransportInfo poll.
	//
	// Why best-effort: the spec models autoplay as a controller-
	// compatibility shim, not a transactional contract. Failing the
	// SetAVTransportURI when autoplay's StartSession dies would surprise
	// controllers that DO send Play (they'd see the URI accepted but
	// the next Play would also fail because nothing is loaded — wrong
	// signal). Storing the URI and surfacing the error via
	// TransportStatus=ERROR_OCCURRED matches the model where Play and
	// SetAVTransportURI are independent.
	if autoplay {
		// startFreshSession sets lastError on failure and returns the
		// fault code we'd otherwise emit. We deliberately discard the
		// code here.
		_ = a.startFreshSession()
	}

	// SetAVTransportURI has no out-arguments per UPnP AVTransport:1.
	writeSOAPResponse(w, avTransportServiceURN, "SetAVTransportURI", nil)
}

// extractSetAVTransportURIArgs pulls the three named arguments out of
// the flat name→value map produced by parseSOAPRequest. Missing args
// default to empty string — the validator catches CurrentURI=="" via
// ErrInvalidURL ("empty URL"); CurrentURIMetaData=="" is legitimate.
// InstanceID=="" is treated as 0 by the InstanceID validation gate
// for permissive-controller compatibility (some controllers omit
// InstanceID since it's always 0 anyway).
func extractSetAVTransportURIArgs(args map[string]string) setAVTransportURIArgs {
	return setAVTransportURIArgs{
		InstanceID:         args["InstanceID"],
		CurrentURI:         args["CurrentURI"],
		CurrentURIMetaData: args["CurrentURIMetaData"],
	}
}

// protocolInfoMatchesSink reports whether the given protocolInfo string
// from CurrentURIMetaData maps to a MIME the renderer advertises in
// sinkProtocolInfoEntries. The match is on the third colon-delimited
// field (the contentFormat / MIME type) — the protocol field
// ("http-get") and the network field ("*") are not enforced here
// because controllers vary. Spec line 319 says we reject when "the
// MIME advertised in metadata is not in SinkProtocolInfo", so MIME is
// what we cross-check.
//
// The check is exact-match on the MIME string (lower-cased). Some
// controllers emit "video/MP4" or include the DLNA.ORG_PN parameter
// after a semicolon; we strip parameters and lowercase before
// comparison.
func protocolInfoMatchesSink(protocolInfo string) bool {
	mime := mimeFromProtocolInfo(protocolInfo)
	if mime == "" {
		// No discernible MIME — skip the cross-check (treat as accept).
		// Controllers that emit garbled protocolInfo are common; rejecting
		// on parse failure here would break interop with otherwise valid
		// streams.
		return true
	}
	for _, entry := range sinkProtocolInfoEntries {
		if mimeFromProtocolInfo(entry) == mime {
			return true
		}
	}
	return false
}

// mimeFromProtocolInfo extracts the lowercased MIME type from a
// protocolInfo string of the form "<protocol>:<network>:<contentFormat>:<flags>".
// Returns "" if the third field is absent. Strips any DLNA.ORG_PN
// parameter (after a semicolon) and trims surrounding whitespace.
func mimeFromProtocolInfo(s string) string {
	// Split on ':' — protocolInfo has up to four colon-delimited fields.
	parts := strings.SplitN(s, ":", 4)
	if len(parts) < 3 {
		return ""
	}
	mime := strings.TrimSpace(parts[2])
	// Strip ";param=value" tail if present (DLNA profiles use it).
	if semi := strings.IndexByte(mime, ';'); semi >= 0 {
		mime = strings.TrimSpace(mime[:semi])
	}
	return strings.ToLower(mime)
}

func isHLSURLPath(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".m3u8")
}

// redactURL returns a controller-safe summary of a URL. Per the P2.3
// spec ("redacted to its scheme+host, no userinfo or query"), we keep
// only scheme + host (with port). Path, query, fragment, and userinfo
// are all dropped because each is an attacker-controllable surface
// that could leak through a UI status panel, a log line, or a future
// LastChange event.
//
// Special cases:
//   - file:// URLs have no host. We emit "file://" alone — useful
//     enough for the operator to see "this was a bad scheme" without
//     leaking the path.
//   - Empty input returns "<empty>".
//   - url.Parse failures return "<unparseable>". The validator already
//     classified the URL as ErrInvalidURL so we don't need to be
//     precise about the failure mode.
func redactURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "<empty>"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable>"
	}
	var b strings.Builder
	if u.Scheme != "" {
		b.WriteString(u.Scheme)
		b.WriteString("://")
	}
	// Note: u.Host carries the port if present (host:port) but never
	// the userinfo (that's u.User). For schemes without an authority
	// (file:, data:) Host is empty, so we just emit "scheme://".
	b.WriteString(u.Host)
	return b.String()
}

// setLastError stores msg in a.lastError under mu. Callers should
// already have built a redacted message; this helper just centralizes
// the locking. Kept separate from the storage in handleSetAVTransportURI
// so query-action handlers don't need to reach into the field.
func (a *Adapter) setLastError(msg string) {
	a.mu.Lock()
	a.lastError = msg
	a.mu.Unlock()
}
