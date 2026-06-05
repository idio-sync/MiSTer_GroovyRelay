package chassis

import (
	"errors"
	"net/http"
	"strings"
)

func (s *Server) handleHistoryPlayPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
		return
	}
	id := strings.TrimSpace(r.PostFormValue("id"))
	if id == "" {
		writeCastJSON(w, http.StatusBadRequest, false, "BAD INPUT")
		return
	}

	player, ok := s.historyPlayProviderForID(id)
	if !ok {
		writeCastJSON(w, http.StatusNotFound, false, "NOT FOUND")
		return
	}

	if _, err := player.CompanionHistoryPlay(r.Context(), id); err != nil {
		writeCastJSON(w, historyPlayHTTPStatus(err), false, historyPlayChip(err))
		return
	}
	writeCastJSON(w, http.StatusOK, true, "")
}

func (s *Server) historyPlayProviderForID(id string) (companionHistoryPlayProvider, bool) {
	if s.cfg.Registry == nil {
		return nil, false
	}
	for _, adapter := range s.cfg.Registry.List() {
		history, ok := adapter.(companionHistoryProvider)
		if !ok {
			continue
		}
		player, ok := adapter.(companionHistoryPlayProvider)
		if !ok {
			continue
		}
		for _, entry := range history.CompanionHistory() {
			if strings.TrimSpace(entry.ID) == id {
				return player, true
			}
		}
	}
	return nil, false
}

func historyPlayHTTPStatus(err error) int {
	var httpErr interface{ HTTPStatus() int }
	if errors.As(err, &httpErr) {
		status := httpErr.HTTPStatus()
		if status >= 400 && status <= 599 {
			return status
		}
	}
	return http.StatusInternalServerError
}

func historyPlayChip(err error) string {
	switch historyPlayHTTPStatus(err) {
	case http.StatusBadRequest:
		return "BAD INPUT"
	case http.StatusNotFound:
		return "NOT FOUND"
	default:
		return "CAST FAILED"
	}
}
