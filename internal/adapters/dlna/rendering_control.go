package dlna

import (
	"net/http"
	"strconv"
)

// rendering_control.go owns the RenderingControl:1 SOAP control
// endpoint (POST /dlna/control/RenderingControl). Phase 1 disposition
// per spec §Service Action Surface lines 223-240: all 6 actions Impl
// (ListPresets, SelectPreset, GetMute, SetMute, GetVolume, SetVolume).
//
// Volume/Mute are virtual — they update a.volume / a.muted under mu
// but do not change FFmpeg or Groovy audio (spec §RenderingControl
// line 449). The state exists so DLNA control points (VLC,
// BubbleUPnP, Kodi) can probe and set the renderer's volume without
// their UI flow failing.

const renderingControlServiceURN = "urn:schemas-upnp-org:service:RenderingControl:1"

// rcFactoryDefaultsPreset is the only preset listed in ListPresets.
// SelectPreset with this name resets Volume=100 / Mute=false.
const rcFactoryDefaultsPreset = "FactoryDefaults"

// handleRenderingControlSOAP dispatches the RenderingControl:1 SOAP
// action.
//
// Spec §RenderingControl rules (lines 451-458):
//   - InstanceID must be 0
//   - Channel must be Master; other channels return InvalidArgs (402)
//   - Volume range is 0..100; out-of-range returns InvalidArgs (402)
//   - Mute accepts UPnP boolean forms 0/1 and false/true
//
// The adapter mutex is acquired briefly to read or update volume/muted,
// then released before writing the HTTP response — per the locking
// discipline documented on Adapter (mutex never held across HTTP I/O).
func (a *Adapter) handleRenderingControlSOAP(w http.ResponseWriter, r *http.Request) {
	action, ok := extractSOAPAction(r.Header.Get("SOAPACTION"))
	if !ok {
		writeSOAPFault(w, upnpErrInvalidAction)
		return
	}

	_, args, err := parseSOAPRequest(w, r)
	if err != nil {
		// All parse failures (oversized body, empty body, missing
		// action element, malformed XML) map to InvalidArgs — the
		// controller sent a malformed envelope. MaxBytesReader sets
		// Connection: close on the response and stops the read with
		// *http.MaxBytesError on the next Read; we still emit a SOAP
		// fault as the response body, then the connection closes.
		writeSOAPFault(w, upnpErrInvalidArgs)
		return
	}

	// All RC actions take InstanceID=0.
	if iid, ok := args["InstanceID"]; ok && iid != "" && iid != "0" {
		writeSOAPFault(w, upnpErrInvalidInstanceID)
		return
	}

	switch action {
	case "ListPresets":
		// Spec §Service Action Surface: Returns "FactoryDefaults".
		writeSOAPResponse(w, renderingControlServiceURN, action, []soapOutArg{
			{Name: "CurrentPresetNameList", Value: rcFactoryDefaultsPreset},
		})

	case "SelectPreset":
		// FactoryDefaults resets state to defaults; other presets return 701.
		preset := args["PresetName"]
		if preset != rcFactoryDefaultsPreset {
			writeSOAPFault(w, upnpErrTransitionNotAvail)
			return
		}
		a.mu.Lock()
		a.volume = 100
		a.muted = false
		a.mu.Unlock()
		writeSOAPResponse(w, renderingControlServiceURN, action, nil)

	case "GetMute":
		// Channel must be Master. Read snapshot under mu, release, write.
		if !rcChannelIsMaster(args["Channel"]) {
			writeSOAPFault(w, upnpErrInvalidArgs)
			return
		}
		a.mu.Lock()
		muted := a.muted
		a.mu.Unlock()
		writeSOAPResponse(w, renderingControlServiceURN, action, []soapOutArg{
			{Name: "CurrentMute", Value: rcBoolToWire(muted)},
		})

	case "SetMute":
		if !rcChannelIsMaster(args["Channel"]) {
			writeSOAPFault(w, upnpErrInvalidArgs)
			return
		}
		muted, ok := rcParseBool(args["DesiredMute"])
		if !ok {
			writeSOAPFault(w, upnpErrInvalidArgs)
			return
		}
		a.mu.Lock()
		a.muted = muted
		a.mu.Unlock()
		writeSOAPResponse(w, renderingControlServiceURN, action, nil)

	case "GetVolume":
		if !rcChannelIsMaster(args["Channel"]) {
			writeSOAPFault(w, upnpErrInvalidArgs)
			return
		}
		a.mu.Lock()
		vol := a.volume
		a.mu.Unlock()
		writeSOAPResponse(w, renderingControlServiceURN, action, []soapOutArg{
			{Name: "CurrentVolume", Value: strconv.Itoa(vol)},
		})

	case "SetVolume":
		if !rcChannelIsMaster(args["Channel"]) {
			writeSOAPFault(w, upnpErrInvalidArgs)
			return
		}
		raw := args["DesiredVolume"]
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 || v > 100 {
			// Spec §RenderingControl line 456: "out-of-range values
			// return invalid args instead of clamping silently."
			writeSOAPFault(w, upnpErrInvalidArgs)
			return
		}
		a.mu.Lock()
		a.volume = v
		a.mu.Unlock()
		writeSOAPResponse(w, renderingControlServiceURN, action, nil)

	default:
		// Spec §RenderingControl line 234: actions outside the v1 set
		// (brightness, sharpness, GetVolumeDB, etc.) return 401.
		writeSOAPFault(w, upnpErrInvalidAction)
	}
}

// rcChannelIsMaster returns true iff the SOAP Channel argument names
// the only channel we support. Empty is rejected — controllers must
// pass an explicit Master per the spec §RenderingControl rules.
func rcChannelIsMaster(channel string) bool {
	return channel == "Master"
}

// rcParseBool accepts the four UPnP boolean wire forms: "0"/"1" and
// "false"/"true". Other values are an error so the caller can return
// InvalidArgs. Spec §RenderingControl line 456: "Mute accepts UPnP
// boolean forms 0/1 and false/true."
func rcParseBool(s string) (bool, bool) {
	switch s {
	case "1", "true":
		return true, true
	case "0", "false":
		return false, true
	default:
		return false, false
	}
}

// rcBoolToWire returns the canonical "0" / "1" form for SOAP output.
// Most controllers accept both 0/1 and false/true on the wire, but
// 0/1 matches the dataType="boolean" canonical form in the SCPD.
func rcBoolToWire(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
