package filesystem

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"webfile/internal/operations"
)

// BenchmarkSmallFileMove includes real task persistence and forces a
// cross-filesystem move from tmpfs to the normal temporary directory. It is a
// repeatable local comparison, not a substitute for measuring two real disks.
func BenchmarkSmallFileMove(b *testing.B) {
	const files = 1000
	const size = 4096
	for _, method := range []string{"webfile", "cp-delete", "cp-sync-delete"} {
		b.Run(method, func(b *testing.B) {
			if _, err := os.Stat("/dev/shm"); err != nil {
				b.Skip("tmpfs unavailable")
			}
			b.SetBytes(files * size)
			for iteration := 0; iteration < b.N; iteration++ {
				b.StopTimer()
				source, err := os.MkdirTemp("/dev/shm", "webfile-small-move-")
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { _ = os.RemoveAll(source) })
				destination := b.TempDir()
				sourceInfo, err := os.Stat(source)
				if err != nil {
					b.Fatal(err)
				}
				targetInfo, err := os.Stat(destination)
				if err != nil {
					b.Fatal(err)
				}
				if sourceInfo.Sys().(*syscall.Stat_t).Dev == targetInfo.Sys().(*syscall.Stat_t).Dev {
					b.Skip("benchmark requires different filesystems")
				}
				for index := 0; index < files; index++ {
					if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("file-%04d.txt", index)), make([]byte, size), 0o600); err != nil {
						b.Fatal(err)
					}
				}
				resolver, err := NewResolver([]Root{{Name: "From", Path: filepath.Dir(source)}, {Name: "To", Path: destination}})
				if err != nil {
					b.Fatal(err)
				}
				service := NewService(resolver, 1024)
				manager, err := operations.Open(filepath.Join(b.TempDir(), "tasks.db"), 10, 1)
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				started := time.Now()
				if method == "webfile" {
					transfer := service.NewTransfer("move", []string{"/From/" + filepath.Base(source)}, "/To")
					op := manager.Start("move", []string{source}, destination, func(ctx context.Context, reporter operations.Reporter) error {
						return transfer.Run(ctx, func(p TransferProgress) {
							reporter.UpdatePhase(p.Phase, p.BytesDone, p.BytesTotal, p.ItemsDone, p.ItemsTotal, p.Message)
						})
					})
					for {
						current, ok := manager.Get(op.ID)
						if !ok || current.State == operations.StateFailed {
							b.Fatalf("transfer failed: %#v", current)
						}
						if current.State == operations.StateCompleted {
							break
						}
						time.Sleep(time.Millisecond)
					}
				} else {
					if output, err := exec.Command("cp", "-r", "--", source, destination).CombinedOutput(); err != nil {
						b.Fatalf("cp: %v %s", err, output)
					}
					if method == "cp-sync-delete" {
						if output, err := exec.Command("sync", "-f", destination).CombinedOutput(); err != nil {
							b.Fatalf("sync: %v %s", err, output)
						}
					}
					if err := os.RemoveAll(source); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(files/time.Since(started).Seconds(), "files/s")
				b.StopTimer()
				service.Close()
				if err := manager.Close(context.Background()); err != nil {
					b.Fatal(err)
				}
				entries, err := os.ReadDir(filepath.Join(destination, filepath.Base(source)))
				if err != nil || len(entries) != files {
					b.Fatalf("destination files=%d, error=%v", len(entries), err)
				}
			}
		})
	}
}
