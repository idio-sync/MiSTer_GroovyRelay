package dlna

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// soap.go owns SOAP envelope parsing, response writing, and UPnP fault
// writing. UPnP control points POST a soap:Envelope body to the SOAP
// control routes; the action name is carried in the SOAPACTION header
// AND inside the envelope. The handler dispatches on the SOAPACTION
// header name (the spec convention) and reads arguments out of the
// parsed body.
//
// Spec: docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md
// §Common Action Rules (lines 286-326). Error code table is normative.

// soapMaxBodyBytes caps the SOAP request body before XML parsing. UPnP
// SOAP bodies are small — the largest legitimate request is typically
// SetAVTransportURI carrying a DIDL-Lite metadata blob, and 64 KiB is
// well above realistic controller payloads. Enforced via
// http.MaxBytesReader so an oversized body returns an error before any
// XML decode allocates memory.
const soapMaxBodyBytes = 64 * 1024

// upnpErrorCode is the integer surface from the spec's SOAP fault
// mapping table. Listed here so the handler code can refer to a
// constant rather than a literal, and so descMap below is the single
// source of truth for the description string.
type upnpErrorCode int

const (
	upnpErrInvalidAction      upnpErrorCode = 401
	upnpErrInvalidArgs        upnpErrorCode = 402
	upnpErrActionFailed       upnpErrorCode = 501
	upnpErrTransitionNotAvail upnpErrorCode = 701
	upnpErrSeekModeNotSupp    upnpErrorCode = 710
	upnpErrIllegalSeekTarget  upnpErrorCode = 711
	upnpErrPlayModeNotSupp    upnpErrorCode = 712
	upnpErrIllegalMIME        upnpErrorCode = 714
	upnpErrResourceNotFound   upnpErrorCode = 716
	upnpErrPlaySpeedNotSupp   upnpErrorCode = 717
	upnpErrInvalidInstanceID  upnpErrorCode = 718
)

// upnpErrDescriptions maps each error code used by Phase 1 (and the
// codes that will be used in later phases) to its description string.
// The strings are normative — control points sometimes display them
// to users.
var upnpErrDescriptions = map[upnpErrorCode]string{
	upnpErrInvalidAction:      "Invalid Action",
	upnpErrInvalidArgs:        "Invalid Args",
	upnpErrActionFailed:       "Action Failed",
	upnpErrTransitionNotAvail: "Transition not available",
	upnpErrSeekModeNotSupp:    "Seek mode not supported",
	upnpErrIllegalSeekTarget:  "Illegal seek target",
	upnpErrPlayModeNotSupp:    "Play mode not supported",
	upnpErrIllegalMIME:        "Illegal MIME-type",
	upnpErrResourceNotFound:   "Resource not found",
	upnpErrPlaySpeedNotSupp:   "Play speed not supported",
	upnpErrInvalidInstanceID:  "Invalid InstanceID",
}

// upnpErrDescription returns the registered description string or a
// generic fallback. Used by writeSOAPFault — never by handler code,
// which uses the constants directly.
func upnpErrDescription(code upnpErrorCode) string {
	if s, ok := upnpErrDescriptions[code]; ok {
		return s
	}
	return "UPnP Error"
}

// soapEnvelope is the parsed SOAP request envelope. Only the inner
// action element and its children are needed by handlers — those are
// captured as raw XML name/value pairs in soapAction.
type soapEnvelope struct {
	XMLName xml.Name   `xml:"Envelope"`
	Body    soapBody   `xml:"Body"`
}

type soapBody struct {
	// Inner is the single action element inside the body. Its local
	// name is the action; its child elements are the arguments.
	Inner soapAction `xml:",any"`
}

// soapAction is the action element parsed out of the envelope body.
// It captures local name (which the handler cross-checks against the
// SOAPACTION header) and its child argument elements.
type soapAction struct {
	XMLName xml.Name
	Args    []soapArg `xml:",any"`
}

// soapArg is one in-argument inside an action element. Local name is
// the argument name (e.g. "InstanceID"); CharData is its value.
type soapArg struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

// parseSOAPRequest parses the SOAP envelope from r.Body. Caller must
// have already validated method (POST) and content type if desired.
// Body is bounded by soapMaxBodyBytes via http.MaxBytesReader; an
// oversized body returns errSOAPBodyTooLarge so the handler can map
// it to a 413 or a SOAP fault.
//
// Returns the action local name and a flat name→value map of args.
// Empty body or missing Body element returns errSOAPMissingBody.
func parseSOAPRequest(w http.ResponseWriter, r *http.Request) (action string, args map[string]string, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, soapMaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// http.MaxBytesReader returns *http.MaxBytesError on overflow.
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return "", nil, errSOAPBodyTooLarge
		}
		return "", nil, fmt.Errorf("read soap body: %w", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return "", nil, errSOAPMissingBody
	}

	var env soapEnvelope
	dec := xml.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&env); err != nil {
		return "", nil, fmt.Errorf("decode soap envelope: %w", err)
	}
	if env.Body.Inner.XMLName.Local == "" {
		return "", nil, errSOAPMissingAction
	}

	args = make(map[string]string, len(env.Body.Inner.Args))
	for _, a := range env.Body.Inner.Args {
		args[a.XMLName.Local] = a.Value
	}
	return env.Body.Inner.XMLName.Local, args, nil
}

// extractSOAPAction returns the action name from a SOAPACTION header.
// The header is canonically `"<service-type>#<action>"` (with quotes).
// We tolerate missing quotes — some controllers omit them — and an
// empty value or unparseable input returns false.
func extractSOAPAction(hdr string) (string, bool) {
	s := strings.TrimSpace(hdr)
	s = strings.Trim(s, `"`)
	if s == "" {
		return "", false
	}
	hash := strings.LastIndexByte(s, '#')
	if hash < 0 {
		// No service-type prefix — accept the whole token as the action
		// name. Unusual but seen in the wild from minimal controllers.
		return s, true
	}
	action := s[hash+1:]
	if action == "" {
		return "", false
	}
	return action, true
}

// Sentinel errors returned by parseSOAPRequest. Handler code maps
// these to UPnP fault codes; they do not leak strings to controllers.
var (
	errSOAPBodyTooLarge  = errors.New("dlna: soap body exceeds maximum size")
	errSOAPMissingBody   = errors.New("dlna: soap body is empty")
	errSOAPMissingAction = errors.New("dlna: soap envelope missing action element")
)

// writeSOAPResponse writes a successful SOAP response envelope. The
// handler supplies the service URN and action name; out-args is a
// slice (not a map) so the encode order is stable for golden tests.
//
// Output is a fully-formed soap:Envelope body with HTTP status 200,
// Content-Type text/xml; charset="utf-8" (the UPnP convention — the
// trailing semicolon and quoted charset are part of the canonical
// header value), and an empty EXT header (UPnP requires it).
func writeSOAPResponse(w http.ResponseWriter, serviceURN, action string, outArgs []soapOutArg) {
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	buf.WriteString(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">`)
	buf.WriteString(`<s:Body>`)
	// The action response is named "<action>Response" wrapped in a
	// per-service namespace. Use the unprefixed namespace declaration
	// `xmlns="..."` so all child elements inherit it without a prefix.
	fmt.Fprintf(&buf, `<u:%sResponse xmlns:u=%q>`, action, serviceURN)
	for _, arg := range outArgs {
		// Argument values must be XML-escaped. xml.EscapeText writes to
		// an io.Writer which we have via &buf.
		fmt.Fprintf(&buf, "<%s>", arg.Name)
		if arg.Value != "" {
			_ = xml.EscapeText(&buf, []byte(arg.Value))
		}
		fmt.Fprintf(&buf, "</%s>", arg.Name)
	}
	fmt.Fprintf(&buf, `</u:%sResponse>`, action)
	buf.WriteString(`</s:Body></s:Envelope>`)

	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.Header().Set("EXT", "")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// soapOutArg is a single output argument in a SOAP response. Slice of
// these is what handler functions hand to writeSOAPResponse.
type soapOutArg struct {
	Name  string
	Value string
}

// writeSOAPFault writes a UPnP-shaped SOAP fault envelope with HTTP
// status 500. Body wraps a <UPnPError> element carrying errorCode and
// errorDescription as the spec requires.
//
// Spec §Common Action Rules: every UPnP error returns 500 with this
// envelope shape — never plain text and never a non-500 status.
func writeSOAPFault(w http.ResponseWriter, code upnpErrorCode) {
	desc := upnpErrDescription(code)
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	buf.WriteString(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">`)
	buf.WriteString(`<s:Body>`)
	buf.WriteString(`<s:Fault>`)
	buf.WriteString(`<faultcode>s:Client</faultcode>`)
	buf.WriteString(`<faultstring>UPnPError</faultstring>`)
	buf.WriteString(`<detail>`)
	buf.WriteString(`<UPnPError xmlns="urn:schemas-upnp-org:control-1-0">`)
	fmt.Fprintf(&buf, `<errorCode>%d</errorCode>`, int(code))
	buf.WriteString(`<errorDescription>`)
	_ = xml.EscapeText(&buf, []byte(desc))
	buf.WriteString(`</errorDescription>`)
	buf.WriteString(`</UPnPError>`)
	buf.WriteString(`</detail>`)
	buf.WriteString(`</s:Fault>`)
	buf.WriteString(`</s:Body></s:Envelope>`)

	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write(buf.Bytes())
}
