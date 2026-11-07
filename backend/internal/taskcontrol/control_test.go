package taskcontrol

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCheckpointResumeAndCancel(t *testing.T) {
	c := New()
	ctx, cancel := context.WithCancel(With(context.Background(), c))
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.SetPaused(true); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- Wait(ctx) }()
		select {
		case err := <-done:
			t.Fatalf("paused checkpoint returned: %v", err)
		case <-time.After(30 * time.Millisecond):
		}
		if err := c.SetPaused(false); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("resume stayed blocked")
		}
	}
	_ = c.SetPaused(true)
	done := make(chan error, 1)
	go func() { done <- Wait(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel stayed blocked")
	}
}

func TestProcessGroupPauseResumeAndCancel(t *testing.T) {
	c := New()
	ctx, cancel := context.WithCancel(With(context.Background(), c))
	defer cancel()
	output := filepath.Join(t.TempDir(), "progress")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `while :; do printf x >> "$1"; sleep 0.01; done`, "tool", output)
	if err := Start(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- WaitCommand(ctx, cmd) }()
	waitFor(t, func() bool { data, _ := os.ReadFile(output); return len(data) > 1 })
	if err := c.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		data, _ := os.ReadFile("/proc/" + strconv.Itoa(cmd.Process.Pid) + "/stat")
		fields := strings.Fields(string(data))
		return len(fields) > 2 && fields[2] == "T"
	})
	before, _ := os.ReadFile(output)
	time.Sleep(60 * time.Millisecond)
	after, _ := os.ReadFile(output)
	if len(after) != len(before) {
		t.Fatal("paused process kept writing")
	}
	if err := c.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { data, _ := os.ReadFile(output); return len(data) > len(after) })
	if err := c.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled tool succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not kill stopped process group")
	}
}

func waitFor(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStoppedProcessDiesWithItsWorker(t *testing.T) {
	if os.Getenv("WEBFILE_PAUSE_PARENT_HELPER") == "1" {
		control := New()
		ctx := With(context.Background(), control)
		cmd := exec.CommandContext(ctx, "/bin/sleep", "1000")
		if err := Start(ctx, cmd); err != nil {
			t.Fatal(err)
		}
		if err := control.SetPaused(true); err != nil {
			t.Fatal(err)
		}
		fmt.Println(cmd.Process.Pid)
		_ = WaitCommand(ctx, cmd)
		return
	}
	parent := exec.Command(os.Args[0], "-test.run=^TestStoppedProcessDiesWithItsWorker$")
	parent.Env = append(os.Environ(), "WEBFILE_PAUSE_PARENT_HELPER=1")
	stdout, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Process.Kill() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatal("worker did not report child process")
	}
	pid, err := strconv.Atoi(scanner.Text())
	if err != nil {
		t.Fatal(err)
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Kill(); _ = child.Release() })
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	waitFor(t, func() bool {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if os.IsNotExist(err) {
			return true
		}
		fields := strings.Fields(string(data))
		return len(fields) > 2 && fields[2] == "Z"
	})
}
