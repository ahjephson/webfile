//go:build linux

package worker

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"webfile/internal/identity"
	"webfile/internal/thumbnailcache"
)

func TestWorkerExitHelper(t *testing.T) {
	if os.Getenv("WEBFILE_WORKER_EXIT_HELPER") != "1" {
		return
	}
	var socket string
	for index, argument := range os.Args {
		if argument == "-socket" && index+1 < len(os.Args) {
			socket = os.Args[index+1]
		}
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connection, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	time.Sleep(20 * time.Millisecond)
}

func TestLocalWorkerExitCanBeCheckedConcurrently(t *testing.T) {
	t.Setenv("WEBFILE_WORKER_EXIT_HELPER", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "worker-helper")
	if err := os.WriteFile(wrapper, []byte(fmt.Sprintf("#!/bin/sh\nexec %q -test.run '^TestWorkerExitHelper$' -- \"$@\"\n", executable)), 0700); err != nil {
		t.Fatal(err)
	}
	id, err := identity.Current()
	if err != nil {
		t.Fatal(err)
	}
	id.Home = t.TempDir()
	configuration := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configuration, []byte(`{"roots":[{"name":"Files","path":"{home}"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(wrapper, configuration, t.TempDir(), "", "", thumbnailcache.NewAuthorizer())
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	process, err := manager.Ensure(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for process.alive() {
		if ctx.Err() != nil {
			t.Fatal("worker exit was not observed")
		}
		runtime.Gosched()
	}
}
