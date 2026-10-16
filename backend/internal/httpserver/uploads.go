package httpserver

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"webfile/internal/filesystem"
	"webfile/internal/operations"
)

type uploadInitRequest struct {
	Dir          string `json:"dir"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	LastModified int64  `json:"last_modified,omitempty"`
}

func (s *Server) initUpload(w http.ResponseWriter, r *http.Request) {
	var req uploadInitRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	upload, err := s.uploads.Init(r.Context(), req.Dir, req.Name, req.Size, req.LastModified)
	if err != nil {
		writeError(w, err)
		return
	}
	op := s.ops.UpsertExternal(upload.OperationID, "upload", []string{upload.Path}, upload.Path, operations.StateRunning,
		upload.Offset, upload.Size, 0, 1, "Uploading "+upload.Name, "")
	if cancellable, ok := s.ops.SetExternalCancel(upload.OperationID, func() {
		_ = s.uploads.Cancel(upload.ID)
	}); ok {
		op = cancellable
	}
	writeJSON(w, http.StatusOK, map[string]any{"upload": upload, "operation": op})
}

func (s *Server) uploadChunk(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if id == "" || err != nil || offset < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid id and offset are required"})
		return
	}
	length := r.ContentLength
	var observed operations.Operation
	if s.ops != nil {
		observed, _ = s.ops.Get("upload-" + id)
	}
	body := cancellableUploadBody{ReadCloser: r.Body, response: http.NewResponseController(w)}
	upload, complete, err := s.uploads.Append(r.Context(), id, offset, body, length)
	if err != nil {
		if s.ops != nil && !s.uploads.Active(id) {
			s.ops.FailExternal("upload-"+id, observed.UpdatedAt, err)
		}
		var offsetErr *filesystem.UploadOffsetError
		if errors.As(err, &offsetErr) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":           offsetErr.Error(),
				"code":            "offset_mismatch",
				"expected_offset": offsetErr.Expected,
			})
			return
		}
		writeError(w, err)
		return
	}
	state := operations.StateRunning
	message := "Uploading " + upload.Name
	itemsDone := int64(0)
	if complete {
		if s.files != nil { s.files.InvalidateFolderSizes(upload.Path) }
		state = operations.StateCompleted
		message = "Uploaded " + upload.Name
		itemsDone = 1
	}
	op := s.ops.UpsertExternal(upload.OperationID, "upload", []string{upload.Path}, upload.Path, state,
		upload.Offset, upload.Size, itemsDone, 1, message, "")
	writeJSON(w, http.StatusOK, map[string]any{"upload": upload, "complete": complete, "operation": op})
}

type cancellableUploadBody struct {
	io.ReadCloser
	response *http.ResponseController
}

func (b cancellableUploadBody) Close() error {
	// Server request bodies can hold their read lock while waiting for the
	// client. A deadline releases that read before Close takes the same lock.
	_ = b.response.SetReadDeadline(time.Now())
	return b.ReadCloser.Close()
}

func (s *Server) cancelUpload(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
		return
	}
	if err := s.uploads.Cancel(id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
