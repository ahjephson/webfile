package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"webfile/internal/archive"
	"webfile/internal/filesystem"
	"webfile/internal/operations"
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
			if operation.Kind != "delete" || operation.ProgressDone != 1 || operation.ProgressTotal != 1 {
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

func TestAcceptedArchivePasswordMovesTaskOutOfAwaitingInputWithoutToolProgress(t *testing.T) {
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
		if operation.State == operations.StateRunning && operation.Phase == "checking_password" {
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
