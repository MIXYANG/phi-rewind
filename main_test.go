package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/go/pxb"
)

func TestCollidingProviderCallIDsKeepBothSnapshots(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	require.NoError(t, os.WriteFile(a, []byte("a0"), 0o600))
	require.NoError(t, os.WriteFile(b, []byte("b0"), 0o600))
	beforeA, err := readState(a)
	require.NoError(t, err)
	beforeB, err := readState(b)
	require.NoError(t, err)
	s := &store{Path: filepath.Join(dir, "journal.json"), Records: []snapshot{
		{TurnID: "turn", ExecutionID: "execution-a", Path: a, Before: beforeA},
		{TurnID: "turn", ExecutionID: "execution-b", Path: b, Before: beforeB},
	}}
	require.NoError(t, s.save())
	r := &rewind{pending: map[string]string{
		"execution-a": s.Path, "execution-b": s.Path,
	}}
	// The first end arrives before the second worker writes its file.
	require.NoError(t, os.WriteFile(a, []byte("a1"), 0o600))
	r.after(pxb.EventNotify{ToolCallID: "write", ExecutionID: "execution-a"})
	require.Contains(t, r.pending, "execution-b")
	require.NoError(t, os.WriteFile(b, []byte("b1"), 0o600))
	r.after(pxb.EventNotify{ToolCallID: "write", ExecutionID: "execution-b"})
	require.Empty(t, r.pending)
	data, err := os.ReadFile(s.Path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &s.Records))
	n, err := s.restore(map[string]bool{"turn": true})
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	data, err = os.ReadFile(a)
	require.NoError(t, err)
	assert.Equal(t, "a0", string(data))
	data, err = os.ReadFile(b)
	require.NoError(t, err)
	assert.Equal(t, "b0", string(data))
}
