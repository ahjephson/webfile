package httpserver

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"webfile/internal/filesystem"
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
