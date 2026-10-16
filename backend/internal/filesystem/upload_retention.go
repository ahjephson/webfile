package filesystem

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const uploadRetention = 7 * 24 * time.Hour

func (m *UploadManager) pruneIfDue(now time.Time) error {
	m.mu.Lock()
	if now.Before(m.nextPrune) {
		m.mu.Unlock()
		return nil
	}
	m.nextPrune = now.Add(time.Hour)
	m.mu.Unlock()
	return m.Prune(now)
}

// RunCleanup expires idle staging at startup and hourly, including workers that
// stay running without receiving another upload. Active writers are preserved.
func (m *UploadManager) RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := m.pruneIfDue(time.Now()); err != nil {
			slog.Warn("clean abandoned uploads", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *UploadManager) Prune(now time.Time) error {
	entries, err := os.ReadDir(m.baseDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	referenced := make(map[string]bool)
	var result error
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		m.mu.Lock()
		record, readErr := m.readRecordLocked(id)
		if readErr == nil {
			referenced[record.TempName] = true
			info, statErr := os.Stat(filepath.Join(m.baseDir, record.TempName))
			idle := now.Sub(record.UpdatedAt) >= uploadRetention && (errors.Is(statErr, os.ErrNotExist) || statErr == nil && now.Sub(info.ModTime()) >= uploadRetention)
			if m.active[id] == nil && idle {
				result = errors.Join(result, m.discardLocked(id, record))
			}
		}
		m.mu.Unlock()
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".part") || referenced[entry.Name()] {
			continue
		}
		id, _, _ := strings.Cut(entry.Name(), "-")
		m.mu.Lock()
		if m.active[id] == nil {
			info, statErr := entry.Info()
			if statErr == nil && now.Sub(info.ModTime()) >= uploadRetention {
				result = errors.Join(result, removeUploadFile(filepath.Join(m.baseDir, entry.Name())))
			}
		}
		m.mu.Unlock()
	}
	return result
}

func removeUploadFile(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (m *UploadManager) discardLocked(id string, record uploadRecord) error {
	if cancel := m.active[id]; cancel != nil {
		cancel()
	}
	return errors.Join(removeUploadFile(filepath.Join(m.baseDir, record.TempName)), removeUploadFile(m.recordPath(id)))
}

// Discard is also used when failed/interrupted task history is explicitly removed.
func (m *UploadManager) Discard(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, err := m.readRecordLocked(id)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return m.discardLocked(id, record)
}
