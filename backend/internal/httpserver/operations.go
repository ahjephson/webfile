package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"webfile/internal/archive"
	"webfile/internal/filesystem"
	"webfile/internal/operations"
)

type operationRequest struct {
	Kind          string   `json:"kind"`
	Sources       []string `json:"sources"`
	Destination   string   `json:"destination,omitempty"`
	CopyChunkMiB  int      `json:"copy_chunk_mib,omitempty"`
	ExtractLayout string   `json:"extract_layout,omitempty"`
	ExistingFiles string   `json:"existing_files,omitempty"`
}

func (s *Server) listOperations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"operations": s.ops.List()})
}

func (s *Server) startOperation(w http.ResponseWriter, r *http.Request) {
	var req operationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
	switch req.Kind {
	case "copy", "move":
		if len(req.Sources) == 0 || strings.TrimSpace(req.Destination) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sources and destination are required"})
			return
		}
		if req.CopyChunkMiB == 0 {
			req.CopyChunkMiB = 4
		}
		if req.CopyChunkMiB != 1 && req.CopyChunkMiB != 4 && req.CopyChunkMiB != 16 && req.CopyChunkMiB != 64 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "copy_chunk_mib must be 1, 4, 16 or 64"})
			return
		}
		transfer := s.files.NewTransfer(req.Kind, req.Sources, req.Destination)
		op := s.ops.StartTask(req.Kind, req.Sources, req.Destination, operations.TaskOptions{
			Cancellable:   true,
			CleanupPolicy: "The incomplete item is removed. Items completed before cancellation are retained.",
			InputHandler: func(data json.RawMessage) error {
				var decision filesystem.ConflictDecision
				if err := json.Unmarshal(data, &decision); err != nil {
					return err
				}
				return transfer.Decide(decision)
			},
		}, func(ctx context.Context, reporter operations.Reporter) error {
			ctx = filesystem.WithCopyChunkSize(ctx, req.CopyChunkMiB*1024*1024)
			err := transfer.Run(ctx, func(p filesystem.TransferProgress) {
				reporter.UpdatePhase(p.Phase, p.BytesDone, p.BytesTotal, p.ItemsDone, p.ItemsTotal, p.Message)
			})
			var conflict *filesystem.TransferConflict
			if errors.As(err, &conflict) {
				if err := reporter.SetInput(conflict); err != nil {
					return err
				}
				return operations.ErrAwaitingInput
			}
			return err
		})
		writeJSON(w, http.StatusAccepted, op)

	case "delete":
		if len(req.Sources) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sources are required"})
			return
		}
		op := s.ops.StartTask(req.Kind, req.Sources, "", operations.TaskOptions{
			Cancellable:   true,
			CleanupPolicy: "Deleted items cannot be recovered. Cancellation keeps items not yet deleted.",
		}, func(ctx context.Context, reporter operations.Reporter) error {
			var deleted int64
			for _, source := range req.Sources {
				if err := ctx.Err(); err != nil {
					return err
				}
				message := "Deleting " + path.Base(filesystem.CleanVirtual(source))
				before := deleted
				if err := s.files.DeleteWithProgress(ctx, source, func(count int64) {
					deleted = before + count
					reporter.Report(operations.ProgressUpdate{Phase: "deleting", Message: message,
						ProgressDone: deleted, ProgressUnit: operations.ProgressUnitItems, ItemsDone: deleted})
				}); err != nil {
					return err
				}
			}
			reporter.UpdatePhase("deleting", 0, 0, deleted, deleted, fmt.Sprintf("Deleted %d items", deleted))
			return nil
		})
		writeJSON(w, http.StatusAccepted, op)

	case "extract":
		if len(req.Sources) == 0 || strings.TrimSpace(req.Destination) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "archive sources and destination are required"})
			return
		}
		layout := strings.TrimSpace(req.ExtractLayout)
		if layout == "" && len(req.Sources) != 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "extract_layout is required for multiple archives"})
			return
		}
		if layout != "" && layout != "separate_directories" && layout != "here" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "extract_layout must be separate_directories or here"})
			return
		}
		policy := archive.ExistingFilePolicy(req.ExistingFiles)
		if policy == archive.ExistingFilesFail {
			policy = archive.ExistingFilesSkip
		}
		if policy != archive.ExistingFilesSkip && policy != archive.ExistingFilesOverwrite {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "existing_files must be skip or overwrite"})
			return
		}
		destinations := make([]string, len(req.Sources))
		for index, source := range req.Sources {
			if !supportedArchivePath(source) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "all sources must be supported archives"})
				return
			}
			destinations[index] = req.Destination
			if layout == "separate_directories" {
				destinations[index] = path.Join(req.Destination, archiveNameWithoutExtension(path.Base(filesystem.CleanVirtual(source))))
			}
		}
		started := make([]operations.Operation, 0, len(req.Sources))
		for index, source := range req.Sources {
			started = append(started, s.startExtractOperation(source, destinations[index], policy))
		}
		if layout == "" {
			writeJSON(w, http.StatusAccepted, started[0])
		} else {
			writeJSON(w, http.StatusAccepted, map[string]any{"operations": started})
		}

	case "archive":
		if len(req.Sources) != 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "one folder source is required"})
			return
		}
		source := filesystem.CleanVirtual(req.Sources[0])
		op := s.ops.StartTask("archive", []string{source}, "", operations.TaskOptions{
			Cancellable:   true,
			CleanupPolicy: "The incomplete archive is removed. A completed archive is removed after it is downloaded.",
			Cleanup: func(_ context.Context, operation operations.Operation) error {
				return removeArchiveArtifacts(s.archiveDir, operation.ID)
			},
		}, func(ctx context.Context, reporter operations.Reporter) error {
			if strings.TrimSpace(s.archiveDir) == "" {
				return errors.New("archive download storage is not configured")
			}
			if err := os.MkdirAll(s.archiveDir, 0o700); err != nil {
				return err
			}
			partialPath := filepath.Join(s.archiveDir, reporter.ID()+".partial.zip")
			completedPath := filepath.Join(s.archiveDir, reporter.ID()+".zip")
			defer func() { _ = os.Remove(partialPath) }()
			result, err := s.extractor.CreateFolderArchive(ctx, source, partialPath, func(progress archive.Progress) {
				reporter.Report(operations.ProgressUpdate{
					Phase: progress.Phase, Message: progress.Message,
					ProgressDone: progress.BytesDone, ProgressTotal: progress.BytesTotal,
					ProgressUnit: operations.ProgressUnitBytes,
					BytesDone:    progress.BytesDone, BytesTotal: progress.BytesTotal,
					ItemsDone: progress.ItemsDone, ItemsTotal: progress.ItemsTotal,
				})
			})
			if err != nil {
				return err
			}
			if err := os.Rename(partialPath, completedPath); err != nil {
				return fmt.Errorf("publish archive: %w", err)
			}
			reporter.Report(operations.ProgressUpdate{
				Phase: "ready", Message: fmt.Sprintf("Ready to download · %d files", result.Files),
				ProgressDone: result.Bytes, ProgressTotal: result.Bytes, ProgressUnit: operations.ProgressUnitBytes,
				BytesDone: result.Bytes, BytesTotal: result.Bytes, ItemsDone: result.Files, ItemsTotal: result.Files,
			})
			return nil
		})
		writeJSON(w, http.StatusAccepted, op)

	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kind must be copy, move, delete, extract or archive"})
	}
}

func (s *Server) startExtractOperation(source, destination string, policy archive.ExistingFilePolicy) operations.Operation {
	var recoveryError error
	return s.ops.StartTask("extract", []string{source}, destination, operations.TaskOptions{
		Cancellable:   true,
		CleanupPolicy: "Publication is rolled back on cancellation. If restoration fails, recovery files are retained and their location is reported.",
		Cleanup:       func(context.Context, operations.Operation) error { return recoveryError },
	}, func(ctx context.Context, reporter operations.Reporter) error {
		defer func() {
			if !s.ops.HasQueuedKind("extract") {
				s.archivePassword.Clear()
			}
		}()
		reporter.SetState(operations.StatePreparing, "preparing", "Preparing archive")
		lastPassword := ""
		passwordProvider := func(waitCtx context.Context, incorrect bool) (string, error) {
			if incorrect {
				s.archivePassword.Clear()
			} else if cached, ok := s.archivePassword.Get(); ok {
				lastPassword = cached
				reporter.SetState(operations.StateRunning, "checking_password", "Checking saved password")
				return cached, nil
			}
			message := "Password required"
			if incorrect {
				message = "Incorrect password — try again"
			}
			value, err := s.passwords.Wait(waitCtx, reporter.ID(), func() {
				reporter.SetState(operations.StateAwaitingInput, "awaiting_password", message)
			})
			if err != nil {
				return "", err
			}
			lastPassword = value
			reporter.SetState(operations.StateRunning, "checking_password", "Checking password")
			return value, nil
		}
		result, err := s.extractor.ExtractInteractiveWithOptions(ctx, source, destination, passwordProvider, func(progress archive.Progress) {
			if progress.Phase == "extracting" && progress.PercentKnown {
				reporter.Report(operations.ProgressUpdate{
					Phase: progress.Phase, Message: progress.Message,
					ProgressDone: int64(progress.Percent), ProgressTotal: 100,
					ProgressUnit: operations.ProgressUnitPercent,
				})
			} else if progress.Phase == "extracting" {
				reporter.Report(operations.ProgressUpdate{
					Phase: progress.Phase, Message: progress.Message,
				})
			} else {
				reporter.Report(operations.ProgressUpdate{
					Phase: progress.Phase, Message: progress.Message,
					ProgressDone: progress.ItemsDone, ProgressTotal: progress.ItemsTotal,
					ProgressUnit: operations.ProgressUnitItems,
					ItemsDone:    progress.ItemsDone, ItemsTotal: progress.ItemsTotal,
				})
			}
		}, archive.ExtractOptions{ExistingFiles: policy})
		var recovery *archive.PublicationRecoveryError
		if errors.As(err, &recovery) {
			recoveryError = err
		}
		if err == nil {
			if lastPassword != "" {
				s.archivePassword.Set(lastPassword)
			}
			message := fmt.Sprintf("Extracted %d files", result.Extracted)
			if result.Skipped > 0 {
				message += fmt.Sprintf(" · skipped %d existing files", result.Skipped)
			}
			total := result.Extracted + result.Skipped
			reporter.Report(operations.ProgressUpdate{
				Phase: "publishing", Message: message, ProgressDone: int64(total),
				ProgressTotal: int64(total), ProgressUnit: operations.ProgressUnitItems,
				ItemsDone: int64(total), ItemsTotal: int64(total),
			})
			s.invalidateDestination(destination)
		}
		return err
	})
}

var archiveExtensions = []string{".tar.bz2", ".tar.gz", ".tar.xz", ".zip", ".7z", ".rar", ".tar", ".tgz"}

func supportedArchivePath(value string) bool {
	lower := strings.ToLower(path.Base(filesystem.CleanVirtual(value)))
	for _, extension := range archiveExtensions {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}

func archiveNameWithoutExtension(name string) string {
	lower := strings.ToLower(name)
	for _, extension := range archiveExtensions {
		if strings.HasSuffix(lower, extension) {
			return name[:len(name)-len(extension)]
		}
	}
	return name
}

func (s *Server) submitOperationPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.passwords.Submit(r.PathValue("id"), req.Password); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) submitOperationInput(w http.ResponseWriter, r *http.Request) {
	var decision filesystem.ConflictDecision
	if err := decodeJSON(r, &decision); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	data, _ := json.Marshal(decision)
	operation, err := s.ops.SubmitInput(r.PathValue("id"), data)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, operation)
}

func (s *Server) invalidateDestination(destination string) {
	resolver := s.files.Resolver()
	if _, absolute, err := resolver.ResolveExisting(destination); err == nil {
		s.watch.Invalidate(absolute, "operation")
	}
	parent := path.Dir(filesystem.CleanVirtual(destination))
	if _, absolute, err := resolver.ResolveExisting(parent); err == nil {
		s.watch.Invalidate(absolute, "operation")
	}
}

func (s *Server) cancelOperation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !s.ops.Cancel(req.ID) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "operation is not running"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) reorderOperation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Direction string `json:"direction"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	direction := 0
	if req.Direction == "up" {
		direction = -1
	} else if req.Direction == "down" {
		direction = 1
	} else {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "direction must be up or down"})
		return
	}
	operation, moved := s.ops.MoveQueued(r.PathValue("id"), direction)
	if !moved {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "operation cannot be moved"})
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (s *Server) downloadOperationArtifact(w http.ResponseWriter, r *http.Request) {
	operation, ok := s.ops.Get(r.PathValue("id"))
	if !ok || operation.Kind != "archive" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "archive task not found"})
		return
	}
	if operation.State != operations.StateCompleted {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "archive is not ready to download"})
		return
	}
	if operation.Phase == "downloaded" || operation.Phase == "unavailable" {
		writeJSON(w, http.StatusGone, map[string]string{"error": "archive download is no longer available"})
		return
	}
	if strings.TrimSpace(s.archiveDir) == "" {
		writeJSON(w, http.StatusGone, map[string]string{"error": "archive download is no longer available"})
		return
	}
	filePath := filepath.Join(s.archiveDir, operation.ID+".zip")
	file, err := os.Open(filePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.ops.MarkArtifactUnavailable(operation.ID)
			writeJSON(w, http.StatusGone, map[string]string{"error": "archive download is no longer available"})
			return
		}
		writeError(w, err)
		return
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil {
		writeError(w, err)
		return
	}
	name := "folder.zip"
	if len(operation.Sources) == 1 {
		name = path.Base(filesystem.CleanVirtual(operation.Sources[0])) + ".zip"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(name)))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	written, copyErr := io.Copy(w, file)
	closeErr := file.Close()
	closed = true
	if copyErr != nil || closeErr != nil || written != info.Size() {
		slog.Warn("folder archive download interrupted", "operation", operation.ID, "written", written, "expected", info.Size(), "copy_error", copyErr, "close_error", closeErr)
		return
	}
	if err := os.Remove(filePath); err != nil {
		slog.Error("remove downloaded folder archive", "operation", operation.ID, "path", filePath, "error", err)
		return
	}
	s.ops.MarkArtifactDownloaded(operation.ID)
}

func removeArchiveArtifacts(directory, id string) error {
	if strings.TrimSpace(directory) == "" {
		return nil
	}
	var result error
	for _, suffix := range []string{".partial.zip", ".zip"} {
		if err := os.Remove(filepath.Join(directory, id+suffix)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (s *Server) cleanOrphanArchives() error {
	if strings.TrimSpace(s.archiveDir) == "" {
		return nil
	}
	entries, err := os.ReadDir(s.archiveDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var result error
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".zip"), ".partial")
		if operation, exists := s.ops.Get(id); exists && operation.Kind == "archive" {
			switch operation.State {
			case operations.StateQueued, operations.StatePreparing, operations.StateAwaitingInput, operations.StateRunning, operations.StateCancelling, operations.StateCleaningUp:
				continue
			case operations.StateCompleted:
				if operation.Phase == "completed" && !strings.HasSuffix(entry.Name(), ".partial.zip") {
					continue
				}
			}
		}
		if err := os.Remove(filepath.Join(s.archiveDir, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (s *Server) clearOperationHistory(w http.ResponseWriter, r *http.Request) {
	completed := s.ops.List()
	removed, err := s.ops.ClearHistory()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not clear task history"})
		return
	}
	for _, operation := range completed {
		if operation.Kind == "archive" && operation.FinishedAt != nil {
			_ = removeArchiveArtifacts(s.archiveDir, operation.ID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

func (s *Server) removeOperationHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	operation, exists := s.ops.Get(id)
	removed, err := s.ops.RemoveHistory(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not remove task history"})
		return
	}
	if !removed {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "task is active or no longer exists"})
		return
	}
	if exists && operation.Kind == "archive" {
		_ = removeArchiveArtifacts(s.archiveDir, operation.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}
