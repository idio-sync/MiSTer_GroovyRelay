package chassis

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// AudioDSPController is the live tone/EQ runtime: read current params, apply an
// unpersisted preview, or commit a reconciled value. *core.Manager satisfies it
// (AudioDSP/PreviewAudioDSP/SetAudioDSP). SetAudioDSP is used only to reconcile
// the runtime back to persisted truth (marking persisted=true) after a failed
// commit — the normal commit path persists through the saver.
type AudioDSPController interface {
	AudioDSP() config.AudioDSP
	PreviewAudioDSP(config.AudioDSP) error
	SetAudioDSP(config.AudioDSP) error
}

// AudioDSPSaver persists + applies committed params and manages EQ memories.
// A thin main.go adapter over *uiserver.BridgeSaver satisfies it.
type AudioDSPSaver interface {
	SaveAudioDSP(config.AudioDSP) error
	SaveAudioDSPMemory(slot int, name string, voicing config.AudioDSP) error
	RecallAudioDSPMemory(slot int) (config.AudioDSPMemory, bool)
	// CurrentAudioDSP returns the persisted (on-disk) params, used to
	// reconcile the live runtime after a failed commit.
	CurrentAudioDSP() config.AudioDSP
}

// audioDSPRequest is the POST /ui/audio/dsp body. Params is a partial
// patch merged onto the current runtime params; pointer fields distinguish
// "set to zero" from "omitted".
type audioDSPRequest struct {
	Commit bool `json:"commit"`
	Params struct {
		Enabled  *bool     `json:"enabled"`
		Mono     *bool     `json:"mono"`
		Subsonic *bool     `json:"subsonic"`
		Loudness *bool     `json:"loudness"`
		Bass     *float64  `json:"bass"`
		Mid      *float64  `json:"mid"`
		Treble   *float64  `json:"treble"`
		Balance  *int      `json:"balance"`
		EQ       []float64 `json:"eq"`
	} `json:"params"`
}

func (s *Server) handleAudioDSPPost(w http.ResponseWriter, r *http.Request) {
	if s.audioDSPController == nil || s.audioDSPSaver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "audio dsp not configured")
		return
	}
	var req audioDSPRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed json body")
		return
	}
	merged := mergeAudioDSPPatch(s.audioDSPController.AudioDSP(), req)
	if err := config.ValidateAudioDSP(merged); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Commit {
		if err := s.audioDSPSaver.SaveAudioDSP(merged); err != nil {
			// Spec §Error handling: a failed commit (disk write or hot-swap
			// apply) must not leave a live preview that never landed.
			// Reconcile the runtime to the persisted truth via SetAudioDSP (the
			// committed path) so persisted is marked true again — the audioDsp
			// SSE then re-emits with persisted=true on the next tick.
			_ = s.audioDSPController.SetAudioDSP(s.audioDSPSaver.CurrentAudioDSP())
			audioDSPWriteError(w, err)
			return
		}
	} else {
		if err := s.audioDSPController.PreviewAudioDSP(merged); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "preview failed")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// mergeAudioDSPPatch overlays the request's non-nil fields onto cur. EQ, when
// present, replaces wholesale; absent EQ keeps cur's (length is validated
// downstream).
func mergeAudioDSPPatch(cur config.AudioDSP, req audioDSPRequest) config.AudioDSP {
	p := req.Params
	if p.Enabled != nil {
		cur.Enabled = *p.Enabled
	}
	if p.Mono != nil {
		cur.Mono = *p.Mono
	}
	if p.Subsonic != nil {
		cur.Subsonic = *p.Subsonic
	}
	if p.Loudness != nil {
		cur.Loudness = *p.Loudness
	}
	if p.Bass != nil {
		cur.Bass = *p.Bass
	}
	if p.Mid != nil {
		cur.Mid = *p.Mid
	}
	if p.Treble != nil {
		cur.Treble = *p.Treble
	}
	if p.Balance != nil {
		cur.Balance = *p.Balance
	}
	if p.EQ != nil {
		cur.EQ = append([]float64(nil), p.EQ...)
	}
	return cur
}

// audioDSPWriteError maps a saver error to a status. The uiserver saver wraps
// validation/preflight failures in a typed error exposing StatusCode() (e.g.
// 400 BAD INPUT, 409 PORT IN USE) — the same interface the settings handler
// matches; anything else is a 500.
func audioDSPWriteError(w http.ResponseWriter, err error) {
	var se interface{ StatusCode() int }
	if errors.As(err, &se) {
		writeJSONError(w, se.StatusCode(), err.Error())
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "save failed")
}

// audioDSPMemoryRequest is the POST /ui/audio/dsp/memory body.
type audioDSPMemoryRequest struct {
	Op   string `json:"op"`   // "store" | "recall"
	Slot int    `json:"slot"` // 1..3
	Name string `json:"name"` // store only
}

func (s *Server) handleAudioDSPMemoryPost(w http.ResponseWriter, r *http.Request) {
	if s.audioDSPController == nil || s.audioDSPSaver == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "audio dsp not configured")
		return
	}
	var req audioDSPMemoryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed json body")
		return
	}
	if req.Slot < 1 || req.Slot > 3 {
		writeJSONError(w, http.StatusBadRequest, "slot must be 1..3")
		return
	}
	cur := s.audioDSPController.AudioDSP()
	switch req.Op {
	case "store":
		if err := s.audioDSPSaver.SaveAudioDSPMemory(req.Slot, req.Name, cur); err != nil {
			audioDSPWriteError(w, err)
			return
		}
	case "recall":
		mem, ok := s.audioDSPSaver.RecallAudioDSPMemory(req.Slot)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "memory slot empty")
			return
		}
		merged := cur
		merged.Bass, merged.Mid, merged.Treble = mem.Bass, mem.Mid, mem.Treble
		merged.EQ = append([]float64(nil), mem.EQ...)
		if err := config.ValidateAudioDSP(merged); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.audioDSPSaver.SaveAudioDSP(merged); err != nil {
			audioDSPWriteError(w, err)
			return
		}
	default:
		writeJSONError(w, http.StatusBadRequest, "op must be store or recall")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
