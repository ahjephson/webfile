package httpserver

import (
	"encoding/json"
	"net/http"
	"webfile/internal/filesystem"
)

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var request filesystem.SearchRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, err)
		return
	}
	if err := request.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if _, _, err := s.files.Resolver().ResolveExisting(request.Path); err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(w)
	emit := func(update filesystem.SearchUpdate) error {
		if err := encoder.Encode(update); err != nil {
			return err
		}
		return http.NewResponseController(w).Flush()
	}
	if err := s.files.Search(r.Context(), request, emit); err != nil && r.Context().Err() == nil {
		_ = emit(filesystem.SearchUpdate{Done: true, Error: err.Error()})
	}
}
