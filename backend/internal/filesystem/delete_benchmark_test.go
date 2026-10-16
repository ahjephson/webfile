//go:build linux

package filesystem

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func BenchmarkDeleteSmallFiles(b *testing.B) {
	for _, method := range []string{"webfile", "rm"} {
		b.Run(method, func(b *testing.B) {
			for iteration := 0; iteration < b.N; iteration++ {
				b.StopTimer()
				root := b.TempDir()
				tree := filepath.Join(root, "tree")
				if err := os.Mkdir(tree, 0o700); err != nil {
					b.Fatal(err)
				}
				for index := 0; index < 5000; index++ {
					if err := os.WriteFile(filepath.Join(tree, fmt.Sprint(index)), []byte("small file"), 0o600); err != nil {
						b.Fatal(err)
					}
				}
				resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
				if err != nil {
					b.Fatal(err)
				}
				service := NewService(resolver, 1024)
				b.StartTimer()
				if method == "webfile" {
					if err := service.Delete(context.Background(), "/Files/tree"); err != nil {
						b.Fatal(err)
					}
				} else if output, err := exec.Command("rm", "-rf", "--", tree).CombinedOutput(); err != nil {
					b.Fatalf("rm: %v %s", err, output)
				}
				b.StopTimer()
				service.Close()
				if _, err := os.Stat(tree); !os.IsNotExist(err) {
					b.Fatalf("delete did not remove tree: %v", err)
				}
			}
		})
	}
}
