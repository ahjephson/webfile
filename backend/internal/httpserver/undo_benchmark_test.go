package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"webfile/internal/filesystem"
	"webfile/internal/operations"
)

// Measures the full task lifecycle for a folder with 9,999 file children.
// File contents are not journaled.
func BenchmarkUndoLifecycleMemory(b *testing.B) {
	for _, kind := range []string{"move", "copy"} {
		b.Run(kind+"_folder_9999_files", func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				root := b.TempDir()
				if err := os.MkdirAll(filepath.Join(root, "source", "photos"), 0755); err != nil {
					b.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "destination"), 0755); err != nil {
					b.Fatal(err)
				}
				for index := range 9999 {
					name := fmt.Sprintf("holiday-2026-original-image-%05d.jpg", index)
					if err := os.WriteFile(filepath.Join(root, "source", "photos", name), nil, 0644); err != nil {
						b.Fatal(err)
					}
				}
				resolver, err := filesystem.NewResolver([]filesystem.Root{{Name: "Files", Path: root}})
				if err != nil {
					b.Fatal(err)
				}
				service := filesystem.NewService(resolver, 1024*1024)
				manager, err := operations.Open(filepath.Join(b.TempDir(), "tasks.db"), 50, 2)
				if err != nil {
					b.Fatal(err)
				}
				server := &Server{files: service, ops: manager}
				baseline := retainedUndoBenchmarkHeap()
				b.StartTimer()
				w := httptest.NewRecorder()
				body := fmt.Sprintf(`{"kind":%q,"sources":["/Files/source/photos"],"destination":"/Files/destination"}`, kind)
				server.startOperation(w, httptest.NewRequest(http.MethodPost, "/api/fs/operations", bytes.NewBufferString(body)))
				if w.Code != http.StatusAccepted {
					b.Fatal(w.Code, w.Body.String())
				}
				var started operations.Operation
				if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
					b.Fatal(err)
				}
				journalBytes := waitUndoBenchmarkTask(b, manager, started.ID)
				b.StopTimer()
				completed := retainedUndoBenchmarkHeap()
				b.ReportMetric(float64(int64(completed)-int64(baseline)), "completed-live-B")
				b.ReportMetric(float64(journalBytes), "journal-B")

				// List is the real history API path and clones the private receipt
				// even though JSON serialization hides it from the browser.
				history := manager.List()
				withHistory := retainedUndoBenchmarkHeap()
				b.ReportMetric(float64(int64(withHistory)-int64(completed)), "history-clone-B")
				runtime.KeepAlive(history)
				history = nil

				b.StartTimer()
				request := httptest.NewRequest(http.MethodPost, "/api/fs/operations/"+started.ID+"/undo", nil)
				request.SetPathValue("id", started.ID)
				response := httptest.NewRecorder()
				server.undoOperation(response, request)
				if response.Code != http.StatusAccepted {
					b.Fatal(response.Code, response.Body.String())
				}
				var undo operations.Operation
				if err := json.Unmarshal(response.Body.Bytes(), &undo); err != nil {
					b.Fatal(err)
				}
				waitUndoBenchmarkTask(b, manager, undo.ID)
				b.StopTimer()
				undone := retainedUndoBenchmarkHeap()
				b.ReportMetric(float64(int64(undone)-int64(baseline)), "after-undo-live-B")
				manager.ClearHistory()
				cleared := retainedUndoBenchmarkHeap()
				b.ReportMetric(float64(int64(cleared)-int64(baseline)), "after-clear-live-B")
				runtime.KeepAlive(server)
				if err := manager.Close(context.Background()); err != nil {
					b.Fatal(err)
				}
				service.Close()
				b.StartTimer()
			}
			b.ReportAllocs()
		})
	}
}

func retainedUndoBenchmarkHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func waitUndoBenchmarkTask(b *testing.B, manager *operations.Manager, id string) int {
	b.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		op, ok := manager.Get(id)
		if !ok {
			b.Fatal("task disappeared")
		}
		if op.State == operations.StateCompleted {
			var plan struct {
				Journal string `json:"journal"`
			}
			if len(op.UndoData) == 0 {
				return 0
			}
			if err := json.Unmarshal(op.UndoData, &plan); err != nil {
				b.Fatal(err)
			}
			info, err := os.Stat(plan.Journal)
			if err != nil {
				b.Fatal(err)
			}
			return len(op.UndoData) + int(info.Size())
		}
		if op.State == operations.StateFailed || op.State == operations.StateCancelled {
			b.Fatal(op.State, op.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.Fatal("task timed out")
	return 0
}
