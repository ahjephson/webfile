// Package taskcontrol supplies cancellation-aware pause checkpoints for tasks.
package taskcontrol

import (
	"context"
	"sync"
)

type contextKey struct{}

type Control struct {
	mu        sync.Mutex
	paused    bool
	changed   chan struct{}
	processes map[int]struct{}
}

func New() *Control { return &Control{changed: make(chan struct{}), processes: make(map[int]struct{})} }

func With(ctx context.Context, control *Control) context.Context {
	return context.WithValue(ctx, contextKey{}, control)
}

func from(ctx context.Context) *Control {
	control, _ := ctx.Value(contextKey{}).(*Control)
	return control
}

// Wait is a checkpoint, not a replacement for context cancellation semantics.
func Wait(ctx context.Context) error {
	control := from(ctx)
	if control == nil {
		return ctx.Err()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		control.mu.Lock()
		paused, changed := control.paused, control.changed
		control.mu.Unlock()
		if !paused {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
