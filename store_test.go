package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func directTempDir(t *testing.T) string {
	t.Helper()
	// System temp paths may contain symlinks or Windows short-name aliases.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

func recordChange(t *testing.T, s *store, turn, path string, next *string) {
	t.Helper()
	before, err := readState(path)
	require.NoError(t, err)
	if next == nil {
		require.NoError(t, os.Remove(path))
	} else {
		require.NoError(t, os.WriteFile(path, []byte(*next), 0o600))
	}
	after, err := readState(path)
	require.NoError(t, err)
	s.Records = append(s.Records, snapshot{TurnID: turn, Path: path, Before: before, After: &after})
}

func TestRestoreRepeatedChangesAndNewFilesAfterReload(t *testing.T) {
	dir := directTempDir(t)
	s, err := openStore(directTempDir(t), dir, "s1")
	require.NoError(t, err)
	path, created := filepath.Join(dir, "existing"), filepath.Join(dir, "created")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0o600))
	for i, text := range []string{"one", "two", "three"} {
		turn := "turn1"
		if i == 2 {
			turn = "turn2"
		}
		recordChange(t, s, turn, path, &text)
	}
	newText := "new"
	recordChange(t, s, "turn2", created, &newText)
	require.NoError(t, s.save())
	s, err = openStore(filepath.Dir(s.Path), dir, "s1")
	require.NoError(t, err)
	n, err := s.restore(map[string]bool{"turn1": true, "turn2": true})
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "original", string(content))
	assert.NoFileExists(t, created)
	assert.Empty(t, s.Records)
}

func TestRestoreRejectsExternalChangesAndPendingRecords(t *testing.T) {
	for _, mode := range []string{"after", "between", "pending"} {
		t.Run(mode, func(t *testing.T) {
			dir := directTempDir(t)
			s := &store{Path: filepath.Join(dir, "snapshots.json")}
			path := filepath.Join(dir, "file")
			require.NoError(t, os.WriteFile(path, []byte("original"), 0o600))
			text := "tool"
			recordChange(t, s, "turn", path, &text)
			if mode == "pending" {
				s.Records[0].After = nil
			} else {
				require.NoError(t, os.WriteFile(path, []byte("external"), 0o600))
				if mode == "between" {
					recordChange(t, s, "turn", path, &text)
				}
			}
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			_, err = s.restore(map[string]bool{"turn": true})
			require.Error(t, err)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestRestoreIgnoresAbandonedBranchAndIsRepeatable(t *testing.T) {
	dir := directTempDir(t)
	s := &store{Path: filepath.Join(dir, "snapshots.json")}
	old, current := filepath.Join(dir, "abandoned"), filepath.Join(dir, "current")
	text := "old branch"
	recordChange(t, s, "old", old, &text)
	text = "new branch"
	recordChange(t, s, "new", current, &text)
	for _, want := range []int{1, 0} {
		n, err := s.restore(map[string]bool{"new": true})
		require.NoError(t, err)
		assert.Equal(t, want, n)
	}
	assert.FileExists(t, old)
	assert.NoFileExists(t, current)
	require.Len(t, s.Records, 1)
	assert.Equal(t, "old", s.Records[0].TurnID)
}

func TestRestoreRollsBackWhenJournalCannotBeSaved(t *testing.T) {
	dir := directTempDir(t)
	s := &store{Path: dir}
	path := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0o600))
	text := "tool"
	recordChange(t, s, "turn", path, &text)
	_, err := s.restore(map[string]bool{"turn": true})
	require.Error(t, err)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "tool", string(content))
	require.Len(t, s.Records, 1)
}

func TestRestorePathCaseUsesFileIdentity(t *testing.T) {
	dir := directTempDir(t)
	lower, upper := filepath.Join(dir, "file.txt"), filepath.Join(dir, "FILE.txt")
	require.NoError(t, os.WriteFile(lower, []byte("original"), 0o600))
	// On case-sensitive filesystems these must remain independent files.
	if _, err := os.Stat(upper); os.IsNotExist(err) {
		require.NoError(t, os.WriteFile(upper, []byte("original"), 0o600))
	}
	a, err := os.Stat(lower)
	require.NoError(t, err)
	b, err := os.Stat(upper)
	require.NoError(t, err)
	want := 2
	if os.SameFile(a, b) {
		want = 1
	}
	s, err := openStore(directTempDir(t), dir, "case")
	require.NoError(t, err)
	one, two := "one", "two"
	recordChange(t, s, "turn", lower, &one)
	recordChange(t, s, "turn", upper, &two)
	require.NoError(t, s.save())
	s, err = openStore(filepath.Dir(s.Path), dir, "case")
	require.NoError(t, err)
	n, err := s.restore(map[string]bool{"turn": true})
	require.NoError(t, err)
	assert.Equal(t, want, n)
	for _, path := range []string{lower, upper} {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "original", string(data))
	}
}

func TestHardLinksRejectedForCaptureAndRestore(t *testing.T) {
	dir := directTempDir(t)
	path, alias := filepath.Join(dir, "file"), filepath.Join(dir, "linked")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0o600))
	s := &store{Path: filepath.Join(dir, "journal.json")}
	next := "changed"
	recordChange(t, s, "turn", path, &next)
	require.NoError(t, os.Link(path, alias))
	_, err := readState(path)
	require.ErrorContains(t, err, "hard links")
	_, err = s.restore(map[string]bool{"turn": true})
	require.ErrorContains(t, err, "hard links")
	for _, name := range []string{path, alias} {
		data, err := os.ReadFile(name)
		require.NoError(t, err)
		assert.Equal(t, "changed", string(data))
	}
	a, err := os.Stat(path)
	require.NoError(t, err)
	b, err := os.Stat(alias)
	require.NoError(t, err)
	assert.True(t, os.SameFile(a, b))
	require.Len(t, s.Records, 1)
}
