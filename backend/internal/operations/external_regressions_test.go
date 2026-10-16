package operations

import (
	"context"
	"errors"
	"testing"
)

func TestExternalFailureDoesNotOverwriteNewerAttemptOrCancellation(t *testing.T) {
	m := New(50)
	defer m.Close(context.Background())
	old := m.UpsertExternal("upload-one", "upload", nil, "", StateRunning, 0, 4, 0, 1, "uploading", "")
	newer := m.UpsertExternal("upload-one", "upload", nil, "", StateRunning, 2, 4, 0, 1, "resumed", "")
	m.FailExternal(old.ID, old.UpdatedAt, errors.New("old connection failed"))
	current, _ := m.Get(old.ID)
	if current.State != StateRunning || !current.UpdatedAt.Equal(newer.UpdatedAt) {
		t.Fatalf("old failure overwrote newer progress: %#v", current)
	}
	m.UpsertExternal(old.ID, "upload", nil, "", StateCancelled, 2, 4, 0, 1, "cancelled", "")
	m.FailExternal(old.ID, newer.UpdatedAt, errors.New("late error"))
	current, _ = m.Get(old.ID)
	if current.State != StateCancelled {
		t.Fatalf("late failure overwrote cancellation: %#v", current)
	}
}

func TestExplicitHistoryRemovalCleansArtifactsAndRetainsFailures(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(map[bool]string{false: "remove", true: "clear"}[clear], func(t *testing.T) {
			m := New(50)
			defer m.Close(context.Background())
			m.UpsertExternal("upload-one", "upload", nil, "", StateInterrupted, 1, 4, 0, 1, "interrupted", "")
			m.UpsertExternal("active", "upload", nil, "", StateRunning, 1, 4, 0, 1, "running", "")
			fail := true
			called := 0
			m.SetHistoryCleanup(func(op Operation) error {
				if op.ID == "active" {
					t.Fatal("active artifact cleaned")
				}
				called++
				if fail {
					return errors.New("disk unavailable")
				}
				return nil
			})
			remove := func() error {
				if clear {
					_, err := m.ClearHistory()
					return err
				}
				_, err := m.RemoveHistory("upload-one")
				return err
			}
			if err := remove(); err == nil {
				t.Fatal("cleanup failure ignored")
			}
			if _, ok := m.Get("upload-one"); !ok {
				t.Fatal("failed cleanup discarded its task")
			}
			fail = false
			if err := remove(); err != nil {
				t.Fatal(err)
			}
			if _, ok := m.Get("upload-one"); ok || called != 2 {
				t.Fatalf("cleanup retry failed: exists=%v calls=%d", ok, called)
			}
		})
	}
}
