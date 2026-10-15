package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMovingDirectoryDoesNotExposeExcludedDescendants(t *testing.T) {
	for _, method := range []string{"transfer", "legacy", "rename"} {
		for _, exists := range []bool{true, false} {
			name := method + "/missing"
			if exists {
				name = method + "/existing"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				for _, name := range []string{"source", "public"} {
					if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				secret := filepath.Join(root, "source", "secret.txt")
				if exists {
					if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				resolver, err := NewResolver([]Root{{Name: "Files", Path: root, ExcludedPaths: []string{"/source/secret.txt"}}})
				if err != nil {
					t.Fatal(err)
				}
				service := NewService(resolver, 1024)
				defer service.Close()
				ctx := context.Background()
				switch method {
				case "transfer":
					err = service.NewTransfer("move", []string{"/Files/source"}, "/Files/public").Run(ctx, nil)
				case "legacy":
					err = service.CopyMove(ctx, "move", []string{"/Files/source"}, "/Files/public", nil)
				case "rename":
					err = service.Rename(ctx, "/Files/source", "/Files/public/source")
				}
				if !exists {
					if err != nil {
						t.Fatalf("nonexistent exclusion blocked a valid move: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatal("moved a directory containing an excluded descendant")
				}
				if content, err := os.ReadFile(secret); err != nil || string(content) != "private" {
					t.Fatalf("excluded source changed: %q %v", content, err)
				}
				if _, err := os.Stat(filepath.Join(root, "public", "source")); !os.IsNotExist(err) {
					t.Fatalf("rejected move created output: %v", err)
				}
			})
		}
	}
}
