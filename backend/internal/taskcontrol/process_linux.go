package taskcontrol

import (
	"context"
	"errors"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func (c *Control) SetPaused(paused bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused == paused {
		return nil
	}
	signal := unix.SIGCONT
	if paused {
		signal = unix.SIGSTOP
	}
	for pid := range c.processes {
		if err := unix.Kill(-pid, signal); err != nil && !errors.Is(err, unix.ESRCH) {
			// Undo any signals already sent; the operation keeps its old state.
			undo := unix.SIGSTOP
			if paused {
				undo = unix.SIGCONT
			}
			for other := range c.processes {
				_ = unix.Kill(-other, undo)
			}
			return err
		}
	}
	c.paused = paused
	close(c.changed)
	c.changed = make(chan struct{})
	return nil
}

// Start serializes process registration with Pause so a new child cannot escape
// a concurrent pause. Only the child's own process group is ever signalled.
func Start(ctx context.Context, cmd *exec.Cmd) error {
	c := from(ctx)
	for {
		if err := Wait(ctx); err != nil {
			return err
		}
		if c != nil {
			c.mu.Lock()
			if c.paused {
				c.mu.Unlock()
				continue
			}
		}
		if cmd.SysProcAttr == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
		}
		cmd.SysProcAttr.Setpgid = true
		// A stopped 7-Zip process must not outlive a crashed worker.
		cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
		cmd.Cancel = func() error { return unix.Kill(-cmd.Process.Pid, unix.SIGKILL) }
		err := cmd.Start()
		if c != nil {
			if err == nil {
				c.processes[cmd.Process.Pid] = struct{}{}
			}
			c.mu.Unlock()
		}
		return err
	}
}

func WaitCommand(ctx context.Context, cmd *exec.Cmd) error {
	err := cmd.Wait()
	if c := from(ctx); c != nil {
		c.mu.Lock()
		delete(c.processes, cmd.Process.Pid)
		c.mu.Unlock()
	}
	return err
}

func Run(ctx context.Context, cmd *exec.Cmd) error {
	if err := Start(ctx, cmd); err != nil {
		return err
	}
	return WaitCommand(ctx, cmd)
}
