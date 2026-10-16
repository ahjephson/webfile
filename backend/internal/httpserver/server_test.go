package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"webfile/internal/archive"
	"webfile/internal/filesystem"
	"webfile/internal/operations"
	"webfile/internal/watch"
)

func testDownloadServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	resolver, err := filesystem.NewResolver([]filesystem.Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	return &Server{files: filesystem.NewService(resolver, 1024*1024)}, root
}

type interruptedResponseWriter struct {
	header http.Header
	limit  int
}

func (w *interruptedResponseWriter) Header() http.Header {
	return w.header
}

func (w *interruptedResponseWriter) WriteHeader(_ int) {}

func (w *interruptedResponseWriter) Write(value []byte) (int, error) {
	if w.limit <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	written := min(len(value), w.limit)
	w.limit -= written
	if written != len(value) {
		return written, io.ErrUnexpectedEOF
	}
	return written, nil
}

func completedArchiveTask(t *testing.T, manager *operations.Manager) operations.Operation {
	t.Helper()
	operation := manager.Start("archive", []string{"/Files/photos"}, "", func(context.Context, operations.Reporter) error { return nil })
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, ok := manager.Get(operation.ID)
		if ok && current.State == operations.StateCompleted {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("archive task did not complete: %#v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCompletedFolderArchiveIsRemovedAfterSuccessfulDownload(t *testing.T) {
	manager := operations.New(10)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Close(ctx); err != nil {
			t.Errorf("close operations manager: %v", err)
		}
	})
	archiveDir := t.TempDir()
	target := &Server{ops: manager, archiveDir: archiveDir}
	operation := completedArchiveTask(t, manager)
	archivePath := filepath.Join(archiveDir, operation.ID+".zip")
	archiveData := []byte("prepared archive")
	if err := os.WriteFile(archivePath, archiveData, 0o600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/fs/operations/"+operation.ID+"/download", nil)
	request.SetPathValue("id", operation.ID)
	response := httptest.NewRecorder()

	target.downloadOperationArtifact(response, request)

	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), archiveData) {
		t.Fatalf("download response = %d %q", response.Code, response.Body.Bytes())
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("downloaded archive still exists: %v", err)
	}
	updated, ok := manager.Get(operation.ID)
	if !ok || updated.Phase != "downloaded" || updated.Message != "Downloaded · temporary archive removed" {
		t.Fatalf("updated operation = %#v", updated)
	}
	repeatRequest := httptest.NewRequest(http.MethodGet, "/api/fs/operations/"+operation.ID+"/download", nil)
	repeatRequest.SetPathValue("id", operation.ID)
	repeatResponse := httptest.NewRecorder()
	target.downloadOperationArtifact(repeatResponse, repeatRequest)
	if repeatResponse.Code != http.StatusGone {
		t.Fatalf("repeat download status = %d, want %d", repeatResponse.Code, http.StatusGone)
	}
	updated, _ = manager.Get(operation.ID)
	if updated.Phase != "downloaded" {
		t.Fatalf("repeat download changed operation phase to %q", updated.Phase)
	}
}

func TestInterruptedFolderArchiveDownloadIsRetainedForRetry(t *testing.T) {
	manager := operations.New(10)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Close(ctx); err != nil {
			t.Errorf("close operations manager: %v", err)
		}
	})
	archiveDir := t.TempDir()
	target := &Server{ops: manager, archiveDir: archiveDir}
	operation := completedArchiveTask(t, manager)
	archivePath := filepath.Join(archiveDir, operation.ID+".zip")
	if err := os.WriteFile(archivePath, []byte("prepared archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/fs/operations/"+operation.ID+"/download", nil)
	request.SetPathValue("id", operation.ID)
	response := &interruptedResponseWriter{header: make(http.Header), limit: 4}

	target.downloadOperationArtifact(response, request)

	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("interrupted download archive was removed: %v", err)
	}
	updated, _ := manager.Get(operation.ID)
	if updated.Phase != "completed" {
		t.Fatalf("operation phase = %q, want completed", updated.Phase)
	}
}

func TestListDirReportsSingleConfiguredRoot(t *testing.T) {
	target, _ := testDownloadServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/fs/dir?path=%2F", nil)
	response := httptest.NewRecorder()

	target.listDir(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("got status %d: %s", response.Code, response.Body.String())
	}
	var payload directoryResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.SingleRootPath != "/Files" {
		t.Fatalf("got single root path %q, want %q", payload.SingleRootPath, "/Files")
	}
}

func TestExcludedAndMissingDirectoriesHaveIdenticalResponses(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolver, err := filesystem.NewResolver([]filesystem.Root{{
		Name:          "Files",
		Path:          root,
		ExcludedPaths: []string{"/hidden"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	target := &Server{files: filesystem.NewService(resolver, 1024*1024)}
	t.Cleanup(target.files.Close)

	requestPath := func(virtualPath string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/fs/dir?path="+virtualPath, nil)
		response := httptest.NewRecorder()
		target.listDir(response, request)
		return response
	}
	excluded := requestPath("%2FFiles%2Fhidden")
	missing := requestPath("%2FFiles%2Fmissing")
	if excluded.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("statuses: excluded=%d missing=%d", excluded.Code, missing.Code)
	}
	if excluded.Body.String() != missing.Body.String() {
		t.Fatalf("responses differ: excluded=%s missing=%s", excluded.Body.String(), missing.Body.String())
	}
	var payload map[string]string
	if err := json.NewDecoder(excluded.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["error"] != "path not found" {
		t.Fatalf("error = %q", payload["error"])
	}
}

func TestInlineDownloadAllowsDetectedRasterImageWithSandbox(t *testing.T) {
	target, root := testDownloadServer(t)
	var data bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 1, 1))
	picture.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	if err := png.Encode(&data, picture); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "picture"), data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/fs/download?path=%2FFiles%2Fpicture&inline=true", nil)
	response := httptest.NewRecorder()

	target.download(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("got status %d", response.Code)
	}
	if response.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("got content type %q", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("expected sandbox content security policy")
	}
}

func TestInlineDownloadRejectsActiveContent(t *testing.T) {
	target, root := testDownloadServer(t)
	if err := os.WriteFile(filepath.Join(root, "page.html"), []byte("<script>top.location='/'</script>"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/fs/download?path=%2FFiles%2Fpage.html&inline=true", nil)
	response := httptest.NewRecorder()

	target.download(response, request)

	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("got status %d", response.Code)
	}
}

func TestDeleteOperationPermanentlyRemovesEntry(t *testing.T) {
	target, root := testDownloadServer(t)
	target.ops = operations.New(10)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := target.ops.Close(ctx); err != nil {
			t.Errorf("close operations manager: %v", err)
		}
	})

	deleted := filepath.Join(root, "delete-me")
	if err := os.Mkdir(deleted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deleted, "file.txt"), []byte("gone"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(deleted, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deleted, "nested", "child.txt"), []byte("gone too"), 0o600); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/fs/operations", bytes.NewBufferString(`{"kind":"delete","sources":["/Files/delete-me"]}`))
	response := httptest.NewRecorder()
	target.startOperation(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("got status %d: %s", response.Code, response.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		operation := target.ops.List()[0]
		if operation.State == operations.StateCompleted {
			if operation.Kind != "delete" || operation.ProgressDone != 4 || operation.ProgressTotal != 4 || operation.ItemsDone != 4 || operation.Message != "Deleted 4 items" {
				t.Fatalf("unexpected operation: %#v", operation)
			}
			break
		}
		if operation.State == operations.StateFailed {
			t.Fatalf("delete failed: %s", operation.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("delete did not complete: %#v", operation)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(deleted); !os.IsNotExist(err) {
		t.Fatalf("deleted entry still exists: %v", err)
	}
}

func TestAcceptedArchivePasswordMovesTaskIntoIndeterminateExtraction(t *testing.T) {
	target, root := testDownloadServer(t)
	target.ops = operations.New(10)
	target.passwords = newPasswordBroker()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := target.ops.Close(ctx); err != nil {
			t.Errorf("close operations manager: %v", err)
		}
	})

	archivePath := filepath.Join(root, "secure.7z")
	if err := os.WriteFile(archivePath, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(root, "archive-tool.sh")
	if err := os.WriteFile(tool, []byte(`#!/bin/sh
set -eu
if [ "${1:-}" = inspect ]; then
  printf 'Encrypted = +\n'
  exit 0
fi
printf 'Enter password (will not be echoed):' >&2
IFS= read -r password
sleep 30
`), 0o755); err != nil {
		t.Fatal(err)
	}
	target.extractor = archive.New(archive.Config{
		Executable:         tool,
		ExtractArgs:        []string{"extract", "{archive}", "-o{destination}"},
		InspectArgs:        []string{"inspect", "{archive}"},
		InspectPasswordArg: "-p",
	}, target.files.Resolver())

	request := httptest.NewRequest(http.MethodPost, "/api/fs/operations", bytes.NewBufferString(`{"kind":"extract","sources":["/Files/secure.7z"],"destination":"/Files"}`))
	response := httptest.NewRecorder()
	target.startOperation(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("got status %d: %s", response.Code, response.Body.String())
	}
	var started operations.Operation
	if err := json.NewDecoder(response.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		operation := target.ops.List()[0]
		if operation.State == operations.StateAwaitingInput && operation.Phase == "awaiting_password" {
			break
		}
		if operation.State == operations.StateFailed {
			t.Fatalf("extract failed before password submission: %s", operation.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("extract did not request a password: %#v", operation)
		}
		time.Sleep(10 * time.Millisecond)
	}

	passwordRequest := httptest.NewRequest(http.MethodPost, "/api/fs/operations/"+started.ID+"/password", bytes.NewBufferString(`{"password":"secret"}`))
	passwordRequest.SetPathValue("id", started.ID)
	passwordResponse := httptest.NewRecorder()
	target.submitOperationPassword(passwordResponse, passwordRequest)
	if passwordResponse.Code != http.StatusAccepted {
		t.Fatalf("got password status %d: %s", passwordResponse.Code, passwordResponse.Body.String())
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		operation := target.ops.List()[0]
		if operation.State == operations.StateRunning && operation.Phase == "extracting" {
			if operation.ProgressTotal != 0 || operation.Message != "Extracting archive — files will appear when complete" {
				t.Fatalf("unexpected extraction state: %#v", operation)
			}
			break
		}
		if operation.State == operations.StateFailed {
			t.Fatalf("extract failed after password submission: %s", operation.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("extract remained waiting after password submission: %#v", operation)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !target.ops.Cancel(started.ID) {
		t.Fatal("could not cancel password test operation")
	}
}

func TestBatchExtractionCreatesIndependentTasksAndReusesPassword(t *testing.T) {
	target, root := testDownloadServer(t)
	target.ops = operations.New(10)
	target.passwords = newPasswordBroker()
	watchManager, err := watch.New(watch.Options{Polling: watch.PollingDisabled})
	if err != nil {
		t.Fatal(err)
	}
	target.watch = watchManager
	t.Cleanup(func() {
		_ = watchManager.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := target.ops.Close(ctx); err != nil {
			t.Errorf("close operations manager: %v", err)
		}
	})

	for _, name := range []string{"first.7z", "second.7z"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("archive"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tool := filepath.Join(root, "archive-tool.sh")
	if err := os.WriteFile(tool, []byte(`#!/bin/sh
set -eu
if [ "${1:-}" = inspect ]; then printf 'Encrypted = +\n'; exit 0; fi
archive="$2"
destination="${3#-o}"
printf 'Enter password (will not be echoed):' >&2
IFS= read -r password
if [ "$password" != secret ]; then printf '\nWrong password\n' >&2; exit 2; fi
printf '100%%\n'
printf extracted > "$destination/$(basename "$archive").txt"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	target.extractor = archive.New(archive.Config{
		Executable:         tool,
		ExtractArgs:        []string{"extract", "{archive}", "-o{destination}"},
		InspectArgs:        []string{"inspect", "{archive}"},
		InspectPasswordArg: "-p",
	}, target.files.Resolver())

	updates, stopUpdates := target.ops.Subscribe()
	defer stopUpdates()
	request := httptest.NewRequest(http.MethodPost, "/api/fs/operations", bytes.NewBufferString(`{"kind":"extract","sources":["/Files/first.7z","/Files/second.7z"],"destination":"/Files","extract_layout":"separate_directories","existing_files":"skip"}`))
	response := httptest.NewRecorder()
	target.startOperation(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("got status %d: %s", response.Code, response.Body.String())
	}
	var started struct {
		Operations []operations.Operation `json:"operations"`
	}
	if err := json.NewDecoder(response.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	if len(started.Operations) != 2 {
		t.Fatalf("got %d tasks, want 2", len(started.Operations))
	}

	awaitingPrompts := 0
	passwordSubmitted := false
	completed := map[string]bool{}
	deadline := time.After(5 * time.Second)
	for len(completed) < 2 {
		select {
		case update := <-updates:
			if update.State == operations.StateAwaitingInput {
				awaitingPrompts++
				if !passwordSubmitted {
					if err := target.passwords.Submit(update.ID, "secret"); err != nil {
						t.Fatal(err)
					}
					passwordSubmitted = true
				}
			}
			if update.State == operations.StateCompleted {
				completed[update.ID] = true
			}
			if update.State == operations.StateFailed {
				t.Fatalf("extraction failed: %s", update.Error)
			}
		case <-deadline:
			t.Fatalf("batch did not complete: %#v", target.ops.List())
		}
	}
	if awaitingPrompts != 1 {
		t.Fatalf("got %d password prompts, want 1", awaitingPrompts)
	}
	if _, ok := target.archivePassword.Get(); ok {
		t.Fatal("password remained cached after extraction queue drained")
	}
}
