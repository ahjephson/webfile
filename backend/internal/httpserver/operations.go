package httpserver

import (
	"context"
	"net/http"
	"path"
	"strings"

	"webfile/internal/archive"
	"webfile/internal/filesystem"
	"webfile/internal/operations"
)

type operationRequest struct {
	Kind         string   `json:"kind"`
	Sources      []string `json:"sources"`
	Destination  string   `json:"destination,omitempty"`
	CopyChunkMiB int      `json:"copy_chunk_mib,omitempty"`
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
		op := s.ops.StartTask(req.Kind, req.Sources, req.Destination, operations.TaskOptions{
			Cancellable:   true,
			CleanupPolicy: "The incomplete item is removed. Items completed before cancellation are retained.",
		}, func(ctx context.Context, reporter operations.Reporter) error {
			ctx = filesystem.WithCopyChunkSize(ctx, req.CopyChunkMiB*1024*1024)
			return s.files.CopyMove(ctx, req.Kind, req.Sources, req.Destination, func(p filesystem.TransferProgress) {
				reporter.UpdatePhase(p.Phase, p.BytesDone, p.BytesTotal, p.ItemsDone, p.ItemsTotal, p.Message)
			})
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
			total := int64(len(req.Sources))
			for index, source := range req.Sources {
				if err := ctx.Err(); err != nil {
					return err
				}
				message := "Deleting " + path.Base(filesystem.CleanVirtual(source))
				reporter.UpdatePhase("deleting", 0, 0, int64(index), total, message)
				if err := s.files.Delete(ctx, source); err != nil {
					return err
				}
				reporter.UpdatePhase("deleting", 0, 0, int64(index+1), total, message)
			}
			return nil
		})
		writeJSON(w, http.StatusAccepted, op)

	case "extract":
		if len(req.Sources) != 1 || strings.TrimSpace(req.Destination) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "one archive source and destination are required"})
			return
		}
		source := req.Sources[0]
		op := s.ops.StartTask(req.Kind, req.Sources, req.Destination, operations.TaskOptions{
			Cancellable:   true,
			CleanupPolicy: "Temporary and newly-created extraction output is removed on cancellation.",
		}, func(ctx context.Context, reporter operations.Reporter) error {
			reporter.SetState(operations.StatePreparing, "preparing", "Preparing archive")
			passwordProvider := func(waitCtx context.Context, incorrect bool) (string, error) {
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
				reporter.SetState(operations.StateRunning, "checking_password", "Checking password")
				return value, nil
			}
			count, err := s.extractor.ExtractInteractive(ctx, source, req.Destination, passwordProvider, func(progress archive.Progress) {
				if progress.Phase == "extracting" {
					reporter.Report(operations.ProgressUpdate{
						Phase: progress.Phase, Message: progress.Message,
						ProgressDone: int64(progress.Percent), ProgressTotal: 100,
						ProgressUnit: operations.ProgressUnitPercent,
					})
				} else {
					reporter.Report(operations.ProgressUpdate{
						Phase: progress.Phase, Message: progress.Message,
						ProgressDone: progress.ItemsDone, ProgressTotal: progress.ItemsTotal,
						ProgressUnit: operations.ProgressUnitItems,
						ItemsDone:    progress.ItemsDone, ItemsTotal: progress.ItemsTotal,
					})
				}
			})
			if err == nil {
				reporter.Report(operations.ProgressUpdate{
					Phase: "publishing", Message: "Extracted", ProgressDone: int64(count),
					ProgressTotal: int64(count), ProgressUnit: operations.ProgressUnitItems,
					ItemsDone: int64(count), ItemsTotal: int64(count),
				})
				s.invalidateDestination(req.Destination)
			}
			return err
		})
		writeJSON(w, http.StatusAccepted, op)

	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kind must be copy, move, delete or extract"})
	}
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

func (s *Server) clearOperationHistory(w http.ResponseWriter, r *http.Request) {
	removed, err := s.ops.ClearHistory()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not clear task history"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

func (s *Server) removeOperationHistory(w http.ResponseWriter, r *http.Request) {
	removed, err := s.ops.RemoveHistory(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not remove task history"})
		return
	}
	if !removed {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "task is active or no longer exists"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}
