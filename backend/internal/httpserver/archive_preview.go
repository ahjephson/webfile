package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"webfile/internal/archive"
	"webfile/internal/clienterror"
)

func (s *Server) previewArchive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Path     string `json:"path"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil || !supportedArchivePath(req.Path) || len(req.Password) > 4096 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Select a supported archive and a password of at most 4096 characters."})
		return
	}
	if s.extractor == nil || !s.extractor.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Archive preview is unavailable. Ask your administrator to configure the archive tool."})
		return
	}
	select {
	case s.archivePreviewSlots <- struct{}{}:
		defer func() { <-s.archivePreviewSlots }()
	default:
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Archive previews are busy. Please try again shortly."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	entries, err := s.extractor.Preview(ctx, req.Path, req.Password)
	if err != nil {
		code, message := "", "Could not open the archive. It may be damaged or unsupported by the installed archive tool. Contact your administrator for details."
		status := http.StatusUnprocessableEntity
		switch {
		case errors.Is(err, archive.ErrPasswordRequired):
			code, message = "archive_password_required", "Enter the archive password to view its contents."
		case errors.Is(err, archive.ErrIncorrectPassword):
			code, message = "archive_password_incorrect", "Incorrect password. Please try again."
		case errors.Is(err, archive.ErrPreviewLimit):
			message = "This archive is too large to preview. Extract it to access its contents."
		case errors.Is(err, archive.ErrPreviewToolUnavailable):
			status, message = http.StatusServiceUnavailable, "The archive tool is unavailable. Ask your administrator to check the configured 7-Zip installation."
		default:
			if friendly := clienterror.Text(err); friendly != string(clienterror.Unknown) {
				message = "Could not open the archive. " + friendly
			}
		}
		if code == "" {
			slog.Error("archive preview failed", "path", req.Path, "err", err)
		}
		writeJSON(w, status, map[string]string{"error": message, "code": code})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
