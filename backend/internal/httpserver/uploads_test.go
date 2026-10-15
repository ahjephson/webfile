package httpserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"webfile/internal/filesystem"
)

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
