package persistence

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const schemaVersion = 1

type SessionRecord struct {
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
	CSRFToken string    `json:"csrf_token"`
}

type Preferences struct {
	ViewMode              string `json:"view_mode,omitempty"`
	GridSize              string `json:"grid_size,omitempty"`
	ShowHidden            bool   `json:"show_hidden,omitempty"`
	Theme                 string `json:"theme,omitempty"`
	EditorWordWrap        bool   `json:"editor_word_wrap,omitempty"`
	EditorFontSize        int    `json:"editor_font_size,omitempty"`
	EditorLineNumbers     *bool  `json:"editor_line_numbers,omitempty"`
	EditorShowWhitespace  bool   `json:"editor_show_whitespace,omitempty"`
	EditorMinimap         bool   `json:"editor_minimap,omitempty"`
	EditorToolbar         string `json:"editor_toolbar,omitempty"`
	EditorMarkdownPreview bool   `json:"editor_markdown_preview,omitempty"`
	CopyChunkMiB          int    `json:"copy_chunk_mib,omitempty"`
}

type userState struct {
	Preferences Preferences `json:"preferences"`
}

type diskState struct {
	Version  int                      `json:"version"`
	Sessions map[string]SessionRecord `json:"sessions,omitempty"`
	Users    map[string]userState     `json:"users,omitempty"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data diskState
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("state path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	s := &Store{path: path, data: diskState{Version: schemaVersion, Sessions: map[string]SessionRecord{}, Users: map[string]userState{}}}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &s.data); err != nil {
			return nil, err
		}
		if s.data.Version != schemaVersion {
			return nil, errors.New("unsupported state file version")
		}
		if s.data.Sessions == nil {
			s.data.Sessions = map[string]SessionRecord{}
		}
		if s.data.Users == nil {
			s.data.Users = map[string]userState{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

func (s *Store) PutSession(hash string, rec SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Sessions[hash] = rec
	return s.saveLocked()
}

func (s *Store) GetSession(hash string) (SessionRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.data.Sessions[hash]
	return rec, ok
}

func (s *Store) DeleteSession(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Sessions, hash)
	return s.saveLocked()
}

func (s *Store) SweepSessions(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for key, rec := range s.data.Sessions {
		if !now.Before(rec.ExpiresAt) {
			delete(s.data.Sessions, key)
			removed++
		}
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, s.saveLocked()
}

func (s *Store) Preferences(username string) Preferences {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Users[username].Preferences
}

func (s *Store) SetPreferences(username string, prefs Preferences) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.data.Users[username]
	u.Preferences = prefs
	s.data.Users[username] = u
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".webfile-state-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
