//go:build linux

package filesystem

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestEditorSaveWorkerProcess(t *testing.T) {
	if os.Getenv("WEBFILE_TEST_SAVE_WORKER") != "1" {
		t.Skip("subprocess helper")
	}
	resolver, err := NewResolver([]Root{{Name: "Files", Path: os.Getenv("WEBFILE_TEST_SAVE_ROOT")}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(resolver, 1024*1024)
	defer service.Close()
	var start [1]byte
	if _, err := os.Stdin.Read(start[:]); err != nil {
		t.Fatal(err)
	}
	_, err = service.WriteText(context.Background(), "/Files/document.txt", bytes.Repeat([]byte("x"), 512*1024), os.Getenv("WEBFILE_TEST_SAVE_ETAG"), false)
	if errors.Is(err, ErrConflict) {
		os.Exit(10)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentSaveProcessesAcceptAnETagOnlyOnce(t *testing.T) {
	service, root := testService(t)
	initial, err := service.WriteText(context.Background(), "/Files/document.txt", []byte("original"), "", true)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commands := make([]*exec.Cmd, 4)
	outputs := make([]bytes.Buffer, len(commands))
	var signals []func()
	for index := range commands {
		command := exec.Command(executable, "-test.run=^TestEditorSaveWorkerProcess$")
		command.Stdout = &outputs[index]
		command.Stderr = &outputs[index]
		command.Env = append(os.Environ(), "WEBFILE_TEST_SAVE_WORKER=1", "WEBFILE_TEST_SAVE_ROOT="+root, "WEBFILE_TEST_SAVE_ETAG="+initial.ETag)
		input, err := command.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands[index] = command
		t.Cleanup(func() {
			_ = input.Close()
			if command.ProcessState == nil {
				_ = command.Process.Kill()
				_ = command.Wait()
			}
		})
		signals = append(signals, func() { _, _ = input.Write([]byte{1}); _ = input.Close() })
	}
	for _, signal := range signals {
		signal()
	}
	saved := 0
	for index, command := range commands {
		if err := command.Wait(); err == nil {
			saved++
		} else {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 10 {
				t.Fatalf("save worker failed: %v\n%s", err, outputs[index].String())
			}
		}
	}
	if saved != 1 {
		t.Fatalf("%d processes accepted the same ETag; want exactly one", saved)
	}
}

func TestConcurrentSavesAcceptAnETagOnlyOnceAcrossServicesAndAliases(t *testing.T) {
	for _, linked := range []bool{false, true} {
		name := "regular"
		if linked {
			name = "hard-linked"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "other"), 0o700); err != nil {
				t.Fatal(err)
			}
			resolver, err := NewResolver([]Root{{Name: "Files", Path: root}})
			if err != nil {
				t.Fatal(err)
			}
			services := make([]*Service, 4)
			for index := range services {
				services[index] = NewService(resolver, 1024*1024)
				defer services[index].Close()
			}
			initial, err := services[0].WriteText(context.Background(), "/Files/document.txt", []byte("original"), "", true)
			if err != nil {
				t.Fatal(err)
			}
			if linked {
				if err := os.Link(filepath.Join(root, "document.txt"), filepath.Join(root, "other", "alias.txt")); err != nil {
					t.Fatal(err)
				}
			}
			start := make(chan struct{})
			results := make(chan error, 32)
			var writers sync.WaitGroup
			for index := 0; index < 32; index++ {
				writers.Add(1)
				go func(index int) {
					defer writers.Done()
					<-start
					path := "/Files/document.txt"
					if linked && index%2 != 0 {
						path = "/Files/other/alias.txt"
					}
					_, err := services[index%len(services)].WriteText(context.Background(), path,
						bytes.Repeat([]byte{byte('a' + index%26)}, 512*1024), initial.ETag, false)
					results <- err
				}(index)
			}
			close(start)
			writers.Wait()
			close(results)
			saved := 0
			for err := range results {
				if err == nil {
					saved++
				} else if !errors.Is(err, ErrConflict) {
					t.Errorf("unexpected save error: %v", err)
				}
			}
			if saved != 1 {
				t.Fatalf("%d writes accepted the same ETag; want exactly one", saved)
			}
		})
	}
}
