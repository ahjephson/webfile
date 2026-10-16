package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"webfile/internal/thumbnailcache"
)

// This benchmark uses only APIs present before the directory index, so the
// same fixture can also measure the committed baseline in a native WSL tree.
func BenchmarkThumbnailFolderLifecycle(b *testing.B) {
	for _, scenario := range []struct {
		name         string
		text, images int
		move         bool
		remove       bool
	}{
		{"rename-5000-text-1-cached", 5000, 1, false, false},
		{"rename-5000-text-cold-cache", 5000, 0, false, false},
		{"move-reopen-32-cached", 0, 32, true, false},
		{"move-reopen-256-cached", 0, 256, true, false},
		{"delete-5000-text-1-cached", 5000, 1, false, true},
	} {
		b.Run(scenario.name, func(b *testing.B) {
			for iteration := 0; iteration < b.N; iteration++ {
				b.StopTimer()
				root := b.TempDir()
				folder := filepath.Join(root, "before")
				if err := os.Mkdir(folder, 0700); err != nil {
					b.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "destination"), 0700); err != nil {
					b.Fatal(err)
				}
				for index := 0; index < scenario.text; index++ {
					if err := os.WriteFile(filepath.Join(folder, fmt.Sprintf("text-%05d", index)), []byte("text"), 0600); err != nil {
						b.Fatal(err)
					}
				}
				resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
				if err != nil {
					b.Fatal(err)
				}
				store, err := thumbnailcache.Open(b.TempDir(), 64<<20)
				if err != nil {
					b.Fatal(err)
				}
				socketDir, err := os.MkdirTemp("/tmp", "wf-thumb-bench-")
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { _ = os.RemoveAll(socketDir) })
				authorizer := thumbnailcache.NewAuthorizer()
				var token string
				if scoped, ok := any(authorizer).(interface {
					IssueScoped(uint32, string, []string) (string, error)
				}); ok {
					// RootKey is intentionally reproduced through JSON-independent
					// configuration encoding so this file builds on the old API too.
					key := benchmarkRootKey("Files", root)
					token, err = scoped.IssueScoped(uint32(os.Geteuid()), "benchmark", []string{key})
				} else {
					token, err = authorizer.Issue(uint32(os.Geteuid()))
				}
				if err != nil {
					b.Fatal(err)
				}
				listener, err := thumbnailcache.Listen(filepath.Join(socketDir, "cache.sock"))
				if err != nil {
					b.Fatal(err)
				}
				server := &http.Server{Handler: authorizer.Handler(store.Handler()), ConnContext: thumbnailcache.ConnContext}
				go server.Serve(listener)
				service := NewServiceWithThumbnailOptions(resolver, 1024, ThumbnailOptions{Cache: thumbnailcache.NewClient(filepath.Join(socketDir, "cache.sock"), token)})
				for index := 0; index < scenario.images; index++ {
					path := filepath.Join(folder, fmt.Sprintf("image-%03d.jpg", index))
					// The existing image helper accepts testing.T only; use the
					// common encoder below for identical baseline/candidate fixtures.
					if err := writeBenchmarkImage(path); err != nil {
						b.Fatal(err)
					}
					if _, err := service.Thumbnail(context.Background(), fmt.Sprintf("/Files/before/image-%03d.jpg", index), 320, 320); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
				if scenario.remove {
					if err := service.Delete(context.Background(), "/Files/before"); err != nil {
						b.Fatal(err)
					}
				} else if scenario.move {
					if err := service.NewTransfer("move", []string{"/Files/before"}, "/Files/destination").Run(context.Background(), nil); err != nil {
						b.Fatal(err)
					}
					for index := 0; index < scenario.images; index++ {
						if _, err := service.Thumbnail(context.Background(), fmt.Sprintf("/Files/destination/before/image-%03d.jpg", index), 320, 320); err != nil {
							b.Fatal(err)
						}
					}
				} else if err := service.Rename(context.Background(), "/Files/before", "/Files/after"); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				service.Close()
				_ = server.Close()
				// Close is optional on the old store API used for baseline runs.
				if closer, ok := any(store).(interface{ Close() error }); ok {
					if err := closer.Close(); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

var benchmarkImageColor = color.RGBA{R: 120, G: 60, B: 200, A: 255}

func writeBenchmarkImage(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	canvas := image.NewRGBA(image.Rect(0, 0, 800, 400))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(benchmarkImageColor), image.Point{}, draw.Src)
	return jpeg.Encode(file, canvas, &jpeg.Options{Quality: 80})
}

func benchmarkRootKey(name, root string) string {
	data, _ := json.Marshal([]any{name, filepath.Clean(root), []string(nil), []string(nil)})
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest)
}
