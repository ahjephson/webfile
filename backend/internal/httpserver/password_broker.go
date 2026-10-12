package httpserver

import (
	"context"
	"errors"
	"sync"
)

var errPasswordNotExpected = errors.New("this operation is not waiting for a password")

type passwordBroker struct {
	mu      sync.Mutex
	waiters map[string]chan string
}

type passwordCache struct {
	mu       sync.Mutex
	password string
}

func (c *passwordCache) Get() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.password, c.password != ""
}

func (c *passwordCache) Set(password string) {
	c.mu.Lock()
	c.password = password
	c.mu.Unlock()
}

func (c *passwordCache) Clear() {
	c.mu.Lock()
	c.password = ""
	c.mu.Unlock()
}

func newPasswordBroker() *passwordBroker {
	return &passwordBroker{waiters: make(map[string]chan string)}
}

func (b *passwordBroker) Wait(ctx context.Context, operationID string, ready func()) (string, error) {
	input := make(chan string, 1)
	b.mu.Lock()
	b.waiters[operationID] = input
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if b.waiters[operationID] == input {
			delete(b.waiters, operationID)
		}
		b.mu.Unlock()
	}()
	ready()
	select {
	case value := <-input:
		return value, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (b *passwordBroker) Submit(operationID, password string) error {
	if password == "" {
		return errors.New("password is required")
	}
	b.mu.Lock()
	waiter := b.waiters[operationID]
	b.mu.Unlock()
	if waiter == nil {
		return errPasswordNotExpected
	}
	select {
	case waiter <- password:
		return nil
	default:
		return errPasswordNotExpected
	}
}
