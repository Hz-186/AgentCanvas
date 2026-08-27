package memory_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileProgressStore persists migration and cleanup progress to a single JSON
// file outside the durable memory root. The durable root must contain only
// owner-<id> directories (enforced by MemoryFileMigration.RunAll), so progress
// lives next to it, not inside it. A failed run resumes from the recorded
// per-owner file checksums and completed cleanup actions.
type FileProgressStore struct {
	Path string

	mu     sync.Mutex
	loaded bool
	state  fileProgressState
}

type fileProgressState struct {
	Owners        map[int64]*MemoryMigrationProgress `json:"owners"`
	CleanupDone   map[string]bool                    `json:"cleanup_done"`
	CleanupFailed map[string]string                  `json:"cleanup_failed,omitempty"`
}

func NewFileProgressStore(path string) *FileProgressStore {
	return &FileProgressStore{Path: path}
}

func (s *FileProgressStore) load() (*fileProgressState, error) {
	if s == nil {
		return nil, fmt.Errorf("progress store is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		state := s.state
		if state.Owners == nil {
			state.Owners = map[int64]*MemoryMigrationProgress{}
		}
		if state.CleanupDone == nil {
			state.CleanupDone = map[string]bool{}
		}
		return &state, nil
	}
	state := fileProgressState{Owners: map[int64]*MemoryMigrationProgress{}, CleanupDone: map[string]bool{}}
	if path := s.Path; path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if decodeErr := json.Unmarshal(data, &state); decodeErr != nil {
				return nil, fmt.Errorf("decode legacy migration progress %s: %w", path, decodeErr)
			}
		case os.IsNotExist(err):
			// First run: start empty.
		default:
			return nil, fmt.Errorf("read legacy migration progress %s: %w", path, err)
		}
	}
	if state.Owners == nil {
		state.Owners = map[int64]*MemoryMigrationProgress{}
	}
	if state.CleanupDone == nil {
		state.CleanupDone = map[string]bool{}
	}
	s.state = state
	s.loaded = true
	return &state, nil
}

func (s *FileProgressStore) persist(state *fileProgressState) error {
	if s == nil || s.Path == "" {
		return fmt.Errorf("progress store path is not configured")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("create progress directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode legacy migration progress: %w", err)
	}
	temp := s.Path + ".tmp"
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return fmt.Errorf("write legacy migration progress: %w", err)
	}
	if err := os.Rename(temp, s.Path); err != nil {
		return fmt.Errorf("commit legacy migration progress: %w", err)
	}
	return nil
}

func (s *FileProgressStore) LoadProgress(ctx context.Context, ownerID int64) (*MemoryMigrationProgress, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, err := s.load()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if progress, ok := state.Owners[ownerID]; ok {
		copy := *progress
		copy.Files = map[string]string{}
		for path, checksum := range progress.Files {
			copy.Files[path] = checksum
		}
		return &copy, nil
	}
	return &MemoryMigrationProgress{OwnerID: ownerID, Files: map[string]string{}}, nil
}

func (s *FileProgressStore) SaveProgress(ctx context.Context, ownerID int64, progress *MemoryMigrationProgress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := s.load()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if progress == nil {
		delete(state.Owners, ownerID)
	} else {
		state.Owners[ownerID] = progress
	}
	return s.persist(state)
}

func (s *FileProgressStore) IsDone(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	state, err := s.load()
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return state.CleanupDone[id], nil
}

func (s *FileProgressStore) MarkDone(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := s.load()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state.CleanupDone[id] = true
	return s.persist(state)
}
