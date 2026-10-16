package httpserver

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestThumbnailResponsesCannotBeCached(t *testing.T) {
	target, root := testDownloadServer(t)
	t.Cleanup(target.files.Close)
	picture := image.NewRGBA(image.Rect(0, 0, 8, 8))
	picture.Set(0, 0, color.RGBA{R: 255, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, picture); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "picture.png"), data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	thumbnail, err := target.files.Thumbnail(context.Background(), "/Files/picture.png", 320, 320)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		etag string
	}{
		{name: "first request"},
		{name: "matching old validator", etag: thumbnail.ETag},
		{name: "wildcard validator", etag: "*"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/fs/thumbnail?path=%2FFiles%2Fpicture.png", nil)
			request.Header.Set("If-None-Match", test.etag)
			response := httptest.NewRecorder()
			target.thumbnail(response, request)
			if response.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 with the full image", response.Code)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
			}
			for _, header := range []string{"ETag", "Last-Modified"} {
				if value := response.Header().Get(header); value != "" {
					t.Errorf("unexpected %s: %q", header, value)
				}
			}
			if response.Header().Get("Content-Type") != "image/png" || !bytes.Equal(response.Body.Bytes(), thumbnail.Data) {
				t.Error("response did not contain the complete thumbnail")
			}
		})
	}
}

func TestThumbnailErrorsCannotBeCached(t *testing.T) {
	target, _ := testDownloadServer(t)
	t.Cleanup(target.files.Close)
	request := httptest.NewRequest(http.MethodGet, "/api/fs/thumbnail?path=%2FFiles%2Fmissing.png", nil)
	response := httptest.NewRecorder()
	target.thumbnail(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}
