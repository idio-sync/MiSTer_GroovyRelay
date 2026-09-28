package chassis

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/calibration"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/config"
)

// CalibrationController runs the CRT picture calibration (test pattern +
// draft geometry). *calibration.Controller satisfies it.
type CalibrationController interface {
	Snapshot() calibration.Snapshot
	Start() error
	Preview(config.PictureGeometry) error
	Save() error
	Cancel()
}

// calibrationPreviewRequest is the POST /ui/calibration/preview body. All
// four values are required so a partial body cannot silently reset one.
type calibrationPreviewRequest struct {
	HSize   *float64 `json:"hSize"`
	VSize   *float64 `json:"vSize"`
	HOffset *int     `json:"hOffset"`
	VOffset *int     `json:"vOffset"`
}

func (s *Server) calibrationReady(w http.ResponseWriter) bool {
	if s.calibration == nil {
		writeSettingsChip(w, http.StatusServiceUnavailable, "NOT READY")
		return false
	}
	return true
}

func (s *Server) writeCalibrationSnapshot(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "calibration": s.calibration.Snapshot()})
}

func (s *Server) handleCalibrationStart(w http.ResponseWriter, r *http.Request) {
	if !s.calibrationReady(w) {
		return
	}
	if err := s.calibration.Start(); err != nil {
		if errors.Is(err, calibration.ErrBusy) {
			writeSettingsChip(w, http.StatusConflict, "BUSY")
			return
		}
		writeSettingsChip(w, http.StatusInternalServerError, "START FAILED")
		return
	}
	s.writeCalibrationSnapshot(w)
}

func (s *Server) handleCalibrationPreview(w http.ResponseWriter, r *http.Request) {
	if !s.calibrationReady(w) {
		return
	}
	var req calibrationPreviewRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	g, errs := decodeCalibrationGeometry(req)
	if len(errs) > 0 {
		writeSettingsFieldErrors(w, http.StatusBadRequest, errs)
		return
	}
	if err := s.calibration.Preview(g); err != nil {
		if errors.Is(err, calibration.ErrNotActive) {
			writeSettingsChip(w, http.StatusConflict, "NOT ACTIVE")
			return
		}
		writeSettingsChip(w, http.StatusBadRequest, "BAD INPUT")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodeCalibrationGeometry validates each value with the same bounds and
// messages as the settings fields, keyed by the JSON field name.
func decodeCalibrationGeometry(req calibrationPreviewRequest) (config.PictureGeometry, map[string]string) {
	var g config.PictureGeometry
	errs := map[string]string{}
	size := func(key string, v *float64, dst *float64) {
		if v == nil {
			errs[key] = "required"
			return
		}
		f, err := decodePictureSize(strconv.FormatFloat(*v, 'f', -1, 64))
		if err != nil {
			errs[key] = err.Error()
			return
		}
		*dst = f
	}
	offset := func(key string, v *int, limit int, dst *int) {
		if v == nil {
			errs[key] = "required"
			return
		}
		if *v < -limit || *v > limit {
			errs[key] = fmt.Sprintf("must be in [%d, %d]", -limit, limit)
			return
		}
		*dst = *v
	}
	size("hSize", req.HSize, &g.HSize)
	size("vSize", req.VSize, &g.VSize)
	offset("hOffset", req.HOffset, config.MaxPictureHOffset, &g.HOffset)
	offset("vOffset", req.VOffset, config.MaxPictureVOffset, &g.VOffset)
	return g, errs
}

func (s *Server) handleCalibrationSave(w http.ResponseWriter, r *http.Request) {
	if !s.calibrationReady(w) {
		return
	}
	if err := s.calibration.Save(); err != nil {
		if errors.Is(err, calibration.ErrNotActive) {
			writeSettingsChip(w, http.StatusConflict, "NOT ACTIVE")
			return
		}
		var ce settingsChipError
		if errors.As(err, &ce) {
			writeSettingsChip(w, ce.StatusCode(), ce.Chip())
			return
		}
		writeSettingsChip(w, http.StatusInternalServerError, "WRITE FAILED")
		return
	}
	s.writeCalibrationSnapshot(w)
}

func (s *Server) handleCalibrationCancel(w http.ResponseWriter, r *http.Request) {
	if !s.calibrationReady(w) {
		return
	}
	s.calibration.Cancel()
	w.WriteHeader(http.StatusNoContent)
}

// calibrationSnapshot reads the controller for the SSE loop; ok is false
// when calibration is not wired.
func (s *Server) calibrationSnapshot() (calibration.Snapshot, bool) {
	if s.calibration == nil {
		return calibration.Snapshot{}, false
	}
	return s.calibration.Snapshot(), true
}
