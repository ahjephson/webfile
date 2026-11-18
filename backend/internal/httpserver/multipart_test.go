package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"webfile/internal/archive"
	"webfile/internal/operations"
)

func TestMultipartArchiveNames(t *testing.T) {
	for _, name := range []string{"sample.7z.001", "sample.7z.002", "sample.ZIP.002", "sample.7z.1000"} {
		if !supportedArchivePath(name) || archiveNameWithoutExtension(name) != "sample" {
			t.Fatalf("unsupported or incorrect folder for %q", name)
		}
	}
	for _, name := range []string{"notes.001", "sample.7z.00x", "sample.7z.01", "sample.7z.0001"} {
		if supportedArchivePath(name) {
			t.Fatalf("unexpected archive: %q", name)
		}
	}
}

func TestMultipartSelectionStartsOneTask(t *testing.T) {
	target, _ := testDownloadServer(t)
	target.ops = operations.New(10)
	target.extractor = archive.New(archive.Config{}, target.files.Resolver())
	t.Cleanup(func() { _ = target.ops.Close(context.Background()) })
	request := httptest.NewRequest(http.MethodPost, "/api/fs/operations", bytes.NewBufferString(`{"kind":"extract","sources":["/Files/sample.7z.002","/Files/sample.7z.001"],"destination":"/Files/out","extract_layout":"separate_directories"}`))
	response := httptest.NewRecorder()
	target.startOperation(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d: %s", response.Code, response.Body.String())
	}
	var started struct {
		Operations []operations.Operation `json:"operations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if len(started.Operations) != 1 {
		t.Fatalf("operations=%+v", started.Operations)
	}
	op := started.Operations[0]
	if !reflect.DeepEqual(op.Sources, []string{"/Files/sample.7z.001"}) || op.Destination != "/Files/out/sample" {
		t.Fatalf("operation=%+v", op)
	}
}
