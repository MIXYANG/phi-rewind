package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maxFileBytes = 8 << 20

type fileState struct {
	identity string      // Current filesystem identity; never persisted across replacements.
	Exists   bool        `json:"exists"`
	Content  []byte      `json:"content,omitempty"`
	Mode     os.FileMode `json:"mode,omitempty"`
}

type snapshot struct {
	TurnID      string     `json:"turn_id"`
	ExecutionID string     `json:"execution_id,omitempty"`
	Path        string     `json:"path"`
	Before      fileState  `json:"before"`
	After       *fileState `json:"after,omitempty"`
}

type store struct {
	Path    string
	Records []snapshot
}

func openStore(root, cwd, sessionID string) (*store, error) {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(cwd+"\x00"+sessionID)))
	s := &store{Path: filepath.Join(root, key+".json")}
	err := s.load()
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return s, err
}

func (s *store) load() error {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.Records); err != nil {
		return fmt.Errorf("read rewind snapshots: %w", err)
	}
	return nil
}

func (s *store) finish(executionID string, isError bool) error {
	// Other sub-agents may have appended snapshots since capture.
	if err := s.load(); err != nil {
		return err
	}
	var keep []snapshot
	for _, record := range s.Records {
		if record.ExecutionID == executionID && record.After == nil {
			state, err := readState(record.Path)
			if err != nil {
				return err
			}
			if sameState(state, record.Before) {
				continue
			}
			if !isError {
				record.After = &state
			}
		}
		keep = append(keep, record)
	}
	s.Records = keep
	return s.save()
}

func (s *store) save() error {
	data, err := json.Marshal(s.Records)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	return replaceFile(s.Path, data, 0o600)
}

// replaceFile keeps a failed write from truncating either a snapshot journal
// or a restored file. Each rename stays on the destination filesystem.
func replaceFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".phi-rewind-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err := f.Chmod(mode.Perm()); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func readState(path string) (fileState, error) {
	if err := checkParents(filepath.Dir(path)); err != nil {
		return fileState{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileState{}, nil
	}
	if err != nil {
		return fileState{}, err
	}
	if !info.Mode().IsRegular() {
		return fileState{}, fmt.Errorf(
			"%s is not a regular file; rewind does not support symlinks or directories",
			path,
		)
	}
	if info.Size() > maxFileBytes {
		return fileState{}, fmt.Errorf("%s exceeds rewind's 8 MiB file limit", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return fileState{}, err
	}
	defer f.Close()
	identity, links, err := fileIdentity(f)
	if err != nil {
		return fileState{}, err
	}
	if links > 1 {
		return fileState{}, fmt.Errorf("%s has hard links; rewind requires an unlinked copy of the file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if len(data) > maxFileBytes {
		return fileState{}, fmt.Errorf("%s exceeds rewind's 8 MiB file limit", path)
	}
	return fileState{identity: identity, Exists: true, Content: data, Mode: info.Mode().Perm()}, err
}

// Parent symlinks would let the same snapshot path refer to a different file
// after capture. Refuse them before both capture and recovery.
func checkParents(path string) error {
	for {
		real, err := filepath.EvalSymlinks(path)
		if err == nil {
			same := filepath.Clean(real) == filepath.Clean(path)
			if runtime.GOOS == "windows" {
				same = strings.EqualFold(filepath.Clean(real), filepath.Clean(path))
			}
			if !same {
				return fmt.Errorf("%s contains a symlink; rewind requires a direct file path", path)
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return err
		}
		path = parent
	}
}

func sameState(a, b fileState) bool {
	return a.Exists == b.Exists && a.Mode == b.Mode && bytes.Equal(a.Content, b.Content)
}

func writeState(path string, state fileState) error {
	if !state.Exists {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return replaceFile(path, state.Content, state.Mode)
}

type restoreFile struct {
	Path          string
	Before, After fileState
}

// plan only considers entries on the active branch. Abandoned branches remain
// in the journal but cannot leak into a later rewind through wall-clock time.
func (s *store) plan(turnIDs map[string]bool) ([]restoreFile, error) {
	var files []restoreFile
	index := make(map[string]int)
	current := make(map[string]fileState)
	for _, record := range s.Records {
		if !turnIDs[record.TurnID] {
			continue
		}
		if record.After == nil {
			return nil, fmt.Errorf(
				"unfinished snapshot for %s; code recovery is unavailable for this turn",
				record.Path,
			)
		}
		state, ok := current[record.Path]
		if !ok {
			var err error
			state, err = readState(record.Path)
			if err != nil {
				return nil, err
			}
			current[record.Path] = state
		}
		key := state.identity
		if key == "" {
			key = record.Path
		}
		if i, ok := index[key]; ok {
			if !sameState(files[i].After, record.Before) {
				return nil, fmt.Errorf(
					"%s changed outside write/edit between snapshots; restore it manually",
					record.Path,
				)
			}
			files[i].After = *record.After
		} else {
			index[key] = len(files)
			files = append(files, restoreFile{Path: record.Path, Before: record.Before, After: *record.After})
		}
	}
	for _, file := range files {
		if !sameState(current[file.Path], file.After) {
			return nil, fmt.Errorf(
				"%s changed after the recorded tool call; restore it manually or choose conversation only",
				file.Path,
			)
		}
	}
	return files, nil
}

func (s *store) restore(turnIDs map[string]bool) (int, error) {
	files, err := s.plan(turnIDs)
	if err != nil {
		return 0, err
	}
	rollback := func(applied []restoreFile, cause error) error {
		for i := len(applied) - 1; i >= 0; i-- {
			cause = errors.Join(cause, writeState(applied[i].Path, applied[i].After))
		}
		return cause
	}
	for i, file := range files {
		if err := writeState(file.Path, file.Before); err != nil {
			return 0, rollback(files[:i], fmt.Errorf("restore %s: %w", file.Path, err))
		}
	}
	old := s.Records
	s.Records = nil
	for _, record := range old {
		if !turnIDs[record.TurnID] {
			s.Records = append(s.Records, record)
		}
	}
	if err := s.save(); err != nil {
		s.Records = old
		return 0, rollback(files, err)
	}
	return len(files), nil
}
