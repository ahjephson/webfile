package filesystem

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"webfile/internal/taskcontrol"
)

func TestCopyPausesInsideCurrentFile(t *testing.T) {
	for _, fast := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "kernel copy"}[fast], func(t *testing.T) {
			root := t.TempDir()
			data := bytes.Repeat([]byte("copy chunk"), 1<<20)
			path := filepath.Join(root, "source")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			source, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			target, err := os.Create(filepath.Join(root, "target"))
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			control := taskcontrol.New()
			ctx, cancel := context.WithCancel(taskcontrol.With(context.Background(), control))
			defer cancel()
			paused := make(chan struct{})
			done := make(chan error, 1)
			var once sync.Once
			copyRange := rangeCopyFunc(func(context.Context, *os.File, *os.File, func(int64)) (bool, error) { return false, nil })
			if fast {
				copyRange = copyFileRangePlatform
			}
			go func() {
				done <- copyFileData(ctx, source, target, func(_ string, copied int64) {
					if copied > 0 {
						once.Do(func() { _ = control.SetPaused(true); close(paused) })
					}
				}, copyRange)
			}()
			select {
			case <-paused:
			case <-time.After(2 * time.Second):
				t.Fatal("copy did not reach first chunk")
			}
			info, err := target.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() <= 0 || info.Size() >= int64(len(data)) {
				t.Fatalf("pause was not mid-file: %d", info.Size())
			}
			select {
			case err := <-done:
				t.Fatalf("copy finished while paused: %v", err)
			case <-time.After(40 * time.Millisecond):
			}
			after, _ := target.Stat()
			if after.Size() != info.Size() {
				t.Fatal("paused copy kept writing")
			}
			if err := control.SetPaused(false); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("copy did not resume")
			}
			got, err := os.ReadFile(target.Name())
			if err != nil || !bytes.Equal(data, got) {
				t.Fatalf("resumed data differs: %v", err)
			}
		})
	}
}
