//go:build linux

package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFileActionsPreserveTrailingWhitespace(t *testing.T) {
	for _, suffix := range []string{" ", "\t", "\u00a0"} {
		t.Run(fmt.Sprintf("%q", suffix), func(t *testing.T) {
			service, root := testService(t)
			ctx := context.Background()
			for name, content := range map[string]string{"report": "keep", "report" + suffix: "target"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := service.List(ctx, "/Files")
			if err != nil || len(entries) != 2 {
				t.Fatalf("list: %v %v", entries, err)
			}
			file, err := service.ReadText(ctx, "/Files/report"+suffix)
			if err != nil || file.Content != "target" {
				t.Fatalf("read: %#v %v", file, err)
			}
			if err := service.Rename(ctx, "/Files/report"+suffix, "/Files/renamed"+suffix); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "renamed"+suffix)); err != nil {
				t.Fatal(err)
			}
			if err := service.Delete(ctx, "/Files/renamed"+suffix); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(root, "report"))
			if err != nil || string(data) != "keep" {
				t.Fatalf("wrong sibling changed: %q %v", data, err)
			}
		})
	}
}

func TestDownloadRemainsConfinedDuringDirectorySwaps(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	slot, swap := filepath.Join(root, "slot"), filepath.Join(root, "swap")
	if err := os.Mkdir(slot, 0700); err != nil {
		t.Fatal(err)
	}
	for filename, content := range map[string]string{filepath.Join(slot, "target"): "inside", filepath.Join(outside, "target"): "OUTSIDE"} {
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, swap); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(resolver, 1024)
	defer service.Close()
	stop, stopped := make(chan struct{}), make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				stopped <- nil
				return
			default:
			}
			if err := unix.Renameat2(unix.AT_FDCWD, slot, unix.AT_FDCWD, swap, unix.RENAME_EXCHANGE); err != nil {
				stopped <- err
				return
			}
		}
	}()
	defer func() {
		close(stop)
		if err := <-stopped; err != nil {
			t.Error(err)
		}
	}()
	for attempt := 0; attempt < 2000; attempt++ {
		file, _, err := service.OpenDownload(context.Background(), "/Files/slot/target")
		if err != nil {
			continue
		} // A changing or out-of-root path may be rejected.
		data := make([]byte, 7)
		n, err := file.Read(data)
		file.Close()
		if err != nil || string(data[:n]) != "inside" {
			t.Fatalf("download escaped root: %q %v", data[:n], err)
		}
	}
}

func TestDownloadFollowsAllowedLinksAndRejectsExcludedTargets(t *testing.T) {
	service, root := testService(t)
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	file, _, err := service.OpenDownload(context.Background(), "/Files/link.txt")
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	resolver, err := NewResolver([]Root{{Name: "Files", Path: root, ExcludedPaths: []string{"/target.txt"}}})
	if err != nil {
		t.Fatal(err)
	}
	if file, err := resolver.OpenExisting("/Files/link.txt"); !os.IsNotExist(err) {
		if file != nil {
			file.Close()
		}
		t.Fatalf("excluded symlink target accepted: %v", err)
	}
}
