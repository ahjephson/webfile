package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"webfile/internal/archive"
	"webfile/internal/filesystem"
	"webfile/internal/operations"
	"webfile/internal/ui"
	"webfile/internal/watch"
)

type Server struct {
	files     *filesystem.Service
	uploads   *filesystem.UploadManager
	watch     *watch.Manager
	extractor *archive.Extractor
	ops       *operations.Manager
	passwords *passwordBroker
	mux       *http.ServeMux
}

func NewWorker(files *filesystem.Service, uploads *filesystem.UploadManager, watchManager *watch.Manager, extractor *archive.Extractor, opManager *operations.Manager) *Server {
	s := &Server{files: files, uploads: uploads, watch: watchManager, extractor: extractor, ops: opManager, passwords: newPasswordBroker(), mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return requestLog(recoverer(s.mux))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /internal/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	s.mux.HandleFunc("GET /api/fs/dir", s.listDir)
	s.mux.HandleFunc("GET /api/fs/file", s.readFile)
	s.mux.HandleFunc("GET /api/fs/content-type", s.contentType)
	s.mux.HandleFunc("PUT /api/fs/file", s.writeFile)
	s.mux.HandleFunc("POST /api/fs/mkdir", s.mkdir)
	s.mux.HandleFunc("POST /api/fs/rename", s.rename)
	s.mux.HandleFunc("POST /api/fs/upload", s.upload)
	s.mux.HandleFunc("GET /api/fs/download", s.download)
	s.mux.HandleFunc("GET /api/fs/thumbnail", s.thumbnail)
	s.mux.HandleFunc("POST /api/fs/thumbnails/queue", s.queueVideoThumbnails)
	s.mux.HandleFunc("GET /api/fs/thumbnails/jobs", s.listThumbnailJobs)
	s.mux.HandleFunc("DELETE /api/fs/thumbnails/jobs/{id}", s.cancelThumbnailJob)
	s.mux.HandleFunc("GET /api/fs/details", s.details)
	s.mux.HandleFunc("POST /api/fs/chmod", s.chmod)
	s.mux.HandleFunc("POST /api/fs/chown", s.chown)
	s.mux.HandleFunc("POST /api/fs/uploads/init", s.initUpload)
	s.mux.HandleFunc("PUT /api/fs/uploads/chunk", s.uploadChunk)
	s.mux.HandleFunc("DELETE /api/fs/uploads", s.cancelUpload)
	s.mux.HandleFunc("GET /api/fs/operations", s.listOperations)
	s.mux.HandleFunc("POST /api/fs/operations", s.startOperation)
	s.mux.HandleFunc("POST /api/fs/operations/cancel", s.cancelOperation)
	s.mux.HandleFunc("POST /api/fs/operations/{id}/password", s.submitOperationPassword)
	s.mux.HandleFunc("DELETE /api/fs/operations/history", s.clearOperationHistory)
	s.mux.HandleFunc("DELETE /api/fs/operations/{id}", s.removeOperationHistory)
	s.mux.HandleFunc("GET /api/fs/ws", s.websocket)
}

type directoryResponse struct {
	Path           string             `json:"path"`
	Entries        []filesystem.Entry `json:"entries"`
	SingleRootPath string             `json:"single_root_path,omitempty"`
}

func (s *Server) listDir(w http.ResponseWriter, r *http.Request) {
	p := queryPath(r)
	entries, err := s.files.List(r.Context(), p)
	if err != nil {
		writeError(w, err)
		return
	}
	response := directoryResponse{Path: filesystem.CleanVirtual(p), Entries: entries}
	roots := s.files.Resolver().Roots()
	if len(roots) == 1 {
		response.SingleRootPath = filesystem.VirtualJoin("/", roots[0].Name)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) readFile(w http.ResponseWriter, r *http.Request) {
	content, err := s.files.ReadText(r.Context(), queryPath(r))
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("ETag", content.ETag)
	writeJSON(w, http.StatusOK, content)
}

func (s *Server) contentType(w http.ResponseWriter, r *http.Request) {
	result, err := s.files.DetectContentType(r.Context(), queryPath(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) writeFile(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	data, err := ioReadAllLimit(r, 16<<20)
	if err != nil {
		writeError(w, err)
		return
	}
	createOnly := r.Header.Get("If-None-Match") == "*"
	content, err := s.files.WriteText(r.Context(), queryPath(r), data, r.Header.Get("If-Match"), createOnly)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("ETag", content.ETag)
	writeJSON(w, http.StatusOK, content)
}

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.files.Mkdir(r.Context(), req.Path); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (s *Server) rename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.files.Rename(r.Context(), req.From, req.To); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "multipart upload required"})
		return
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, err)
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_ = part.Close()
			continue
		}
		entry, err := s.files.Upload(r.Context(), r.URL.Query().Get("dir"), part.FileName(), part)
		_ = part.Close()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, entry)
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file form field is required"})
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	virtualPath := queryPath(r)
	details, err := s.files.Details(r.Context(), virtualPath)
	if err != nil {
		writeError(w, err)
		return
	}
	if details.IsDir {
		name := details.Name + ".zip"
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(name)))
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Cache-Control", "no-store")
		if err := s.files.WriteFolderZip(r.Context(), virtualPath, w); err != nil {
			slog.Warn("folder download failed", slog.String("path", virtualPath), slog.String("err", err.Error()))
		}
		return
	}
	file, st, err := s.files.OpenDownload(r.Context(), virtualPath)
	if err != nil {
		writeError(w, err)
		return
	}
	defer file.Close()
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "true" {
		detected, detectErr := s.files.DetectContentType(r.Context(), virtualPath)
		if detectErr != nil {
			writeError(w, detectErr)
			return
		}
		if !inlineContentTypeAllowed(detected.ContentType) {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "this file type cannot be displayed inline"})
			return
		}
		disposition = "inline"
		w.Header().Set("Content-Type", detected.ContentType)
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; base-uri 'none'; form-action 'none'")
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disposition, url.PathEscape(st.Name())))
	if w.Header().Get("Content-Type") == "" {
		if typ := mime.TypeByExtension(path.Ext(st.Name())); typ != "" {
			w.Header().Set("Content-Type", typ)
		}
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), file)
}

func inlineContentTypeAllowed(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch mediaType {
	case "image/avif", "image/bmp", "image/gif", "image/jpeg", "image/png", "image/webp",
		"video/mp4", "video/ogg", "video/quicktime", "video/webm":
		return true
	default:
		return false
	}
}

func (s *Server) thumbnail(w http.ResponseWriter, r *http.Request) {
	width := thumbnailDimension(r.URL.Query().Get("width"), 320)
	height := thumbnailDimension(r.URL.Query().Get("height"), 320)
	result, err := s.files.RequestThumbnail(r.Context(), queryPath(r), width, height)
	if err != nil {
		if errors.Is(err, filesystem.ErrThumbnailQueueFull) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeError(w, err)
		return
	}
	if result.Job != nil {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusAccepted, map[string]any{"job": result.Job})
		return
	}
	thumbnail := *result.Thumbnail
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", thumbnail.ETag)
	if r.Header.Get("If-None-Match") == thumbnail.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", thumbnail.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(thumbnail.Data)))
	_, _ = w.Write(thumbnail.Data)
}

type queueVideoThumbnailsRequest struct {
	Paths  []string `json:"paths"`
	Width  int      `json:"width"`
	Height int      `json:"height"`
}

func (s *Server) queueVideoThumbnails(w http.ResponseWriter, r *http.Request) {
	var req queueVideoThumbnailsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(req.Paths) > 2000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many thumbnail paths"})
		return
	}
	width, height := req.Width, req.Height
	if width < 32 || width > 1024 {
		width = 320
	}
	if height < 32 || height > 1024 {
		height = 320
	}
	jobs, err := s.files.QueueVideoThumbnails(r.Context(), req.Paths, width, height)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) listThumbnailJobs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.files.VideoThumbnailJobs()})
}

func (s *Server) cancelThumbnailJob(w http.ResponseWriter, r *http.Request) {
	if !s.files.CancelVideoThumbnailJob(r.PathValue("id")) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "video thumbnail job is not active"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func thumbnailDimension(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 32 {
		return fallback
	}
	if parsed > 1024 {
		return 1024
	}
	return parsed
}

func queryPath(r *http.Request) string {
	if p := r.URL.Query().Get("path"); p != "" {
		return p
	}
	return "/"
}

func decodeJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

func ioReadAllLimit(r *http.Request, max int64) ([]byte, error) {
	reader := io.LimitReader(r.Body, max+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("request body exceeds %d bytes", max)
	}
	return data, nil
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, filesystem.ErrConflict):
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": err.Error(), "code": "conflict"})
	case errors.Is(err, filesystem.ErrTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
	case errors.Is(err, fs.ErrNotExist):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "path not found"})
	case errors.Is(err, fs.ErrPermission):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, filesystem.ErrInvalidPath), errors.Is(err, filesystem.ErrOutsideRoot), errors.Is(err, filesystem.ErrNotDirectory):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		slog.Error("request failed", slog.String("err", err.Error()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("panic", slog.Any("panic", recovered))
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("http", slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Duration("duration", time.Since(start)))
	})
}

func spaHandler() http.Handler {
	dist, err := fs.Sub(ui.Dist, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "." || p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		index, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			http.Error(w, "frontend missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
