package httpserver

import "net/http"

func (s *Server) details(w http.ResponseWriter, r *http.Request) {
	details, err := s.files.DetailsWithFolderSize(r.Context(), queryPath(r), r.URL.Query().Get("refresh") != "false")
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, details)
}

func (s *Server) chmod(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.files.Chmod(r.Context(), req.Path, req.Mode); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) chown(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path  string `json:"path"`
		Owner string `json:"owner"`
		Group string `json:"group"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.files.Chown(r.Context(), req.Path, req.Owner, req.Group); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
