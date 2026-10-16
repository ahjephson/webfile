//go:build linux

package launcher

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestWorkerLivenessDuringExitAndShutdown(t *testing.T) {
	command := exec.Command("sh", "-c", "sleep 0.02")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	worker := workerProcess{cmd: command, done: done}
	go func() { _ = command.Wait(); close(done) }()
	server := &Server{workers: map[uint32]workerProcess{}, elevated: map[string]workerProcess{}}
	deadline := time.Now().Add(5 * time.Second)
	for worker.alive() {
		server.mu.Lock()
		server.workers[1000] = worker
		server.elevated["test"] = worker
		server.mu.Unlock()
		server.stopAll()
		if time.Now().After(deadline) {
			t.Fatal("worker exit was not observed")
		}
		runtime.Gosched()
	}
	if (workerProcess{}).alive() {
		t.Fatal("missing worker reported alive")
	}
}

func TestRandomElevationTokenIsValidAndUnique(t *testing.T) {
	first, err := RandomElevationToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := RandomElevationToken()
	if err != nil {
		t.Fatal(err)
	}
	if !validToken(first) || !validToken(second) {
		t.Fatalf("generated invalid tokens: %q %q", first, second)
	}
	if first == second {
		t.Fatal("generated duplicate elevation tokens")
	}
}

func TestValidTokenRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", "abc", string(make([]byte, 64))} {
		if validToken(value) {
			t.Fatalf("accepted malformed token %q", value)
		}
	}
}
