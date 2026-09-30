package httpserver

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPasswordBrokerDeliversPasswordOnlyToWaitingOperation(t *testing.T) {
	target := newPasswordBroker()
	ready := make(chan struct{})
	result := make(chan string, 1)
	go func() {
		password, _ := target.Wait(context.Background(), "operation", func() { close(ready) })
		result <- password
	}()
	<-ready
	if err := target.Submit("operation", "Password"); err != nil {
		t.Fatal(err)
	}
	if password := <-result; password != "Password" {
		t.Fatalf("password=%q", password)
	}
	if err := target.Submit("operation", "Another"); !errors.Is(err, errPasswordNotExpected) {
		t.Fatalf("error=%v", err)
	}
}

func TestPasswordBrokerStopsWaitingWhenContextIsCancelled(t *testing.T) {
	target := newPasswordBroker()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := target.Wait(ctx, "operation", func() { close(ready) })
		result <- err
	}()
	<-ready
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not stop")
	}
}
