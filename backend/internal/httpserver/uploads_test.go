package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"webfile/internal/filesystem"
	"webfile/internal/operations"
)

func TestUploadPublicationErrorFinishesTaskAndRetainsResumeData(t *testing.T) {
	target, root := testDownloadServer(t)
	target.uploads = filesystem.NewUploadManager(target.files.Resolver(), t.TempDir())
	target.ops = operations.New(50)
	defer target.ops.Close(context.Background())
	response := httptest.NewRecorder()
	target.initUpload(response, httptest.NewRequest(http.MethodPost, "/api/fs/uploads/init", bytes.NewBufferString(`{"dir":"/Files","name":"same.bin","size":1,"last_modified":1}`)))
	var initial struct {
		Upload filesystem.UploadSession `json:"upload"`
	}
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "same.bin"), []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	target.uploadChunk(response, httptest.NewRequest(http.MethodPut, "/api/fs/uploads/chunk?id="+initial.Upload.ID+"&offset=0", bytes.NewBufferString("x")))
	op, _ := target.ops.Get(initial.Upload.OperationID)
	if response.Code != 412 || op.State != operations.StateFailed || op.Cancellable || op.Error == "" {
		t.Fatalf("HTTP %d task %#v", response.Code, op)
	}
	if err := os.Remove(filepath.Join(root, "same.bin")); err != nil {
		t.Fatal(err)
	}
	resumed, err := target.uploads.Init(context.Background(), "/Files", "same.bin", 1, 1)
	if err != nil || resumed.Offset != 1 {
		t.Fatalf("resume: %#v %v", resumed, err)
	}
}

type observedUploadBody struct {
	io.ReadCloser
	once    sync.Once
	entered chan struct{}
}

func (b *observedUploadBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.once.Do(func() { close(b.entered) })
	}
	return n, err
}

func TestCancellingStalledHTTPUploadReleasesHandler(t *testing.T) {
	resolver, err := filesystem.NewResolver([]filesystem.Root{{Name: "Files", Path: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	uploads := filesystem.NewUploadManager(resolver, t.TempDir())
	upload, err := uploads.Init(context.Background(), "/Files", "stalled.bin", 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	entered, finished := make(chan struct{}), make(chan struct{})
	target := &Server{uploads: uploads}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		r.Body = &observedUploadBody{ReadCloser: r.Body, entered: entered}
		target.uploadChunk(w, r)
	}))
	defer server.Close()
	connection, err := net.DialTimeout("tcp", strings.TrimPrefix(server.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	// Declare four bytes and send one, leaving the server's next Read blocked.
	if _, err := fmt.Fprintf(connection, "POST /api/fs/uploads/chunk?id=%s&offset=0 HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\n\r\nx", upload.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler never read the upload body")
	}
	if err := uploads.Cancel(upload.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled HTTP upload handler stayed blocked")
	}
}
