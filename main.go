// Rewind is an optional PXB extension; it owns all file snapshots and recovery policy.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
	"github.com/pulseaiclub/phi/ext/go/pxb"
)

type rewind struct {
	api     *phi.ExtensionAPI
	root    string
	pending map[string]string
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m := phi.New("rewind", "0.1.0")
	r := &rewind{api: m, root: filepath.Join(home, ".phi", "rewind"), pending: make(map[string]string)}
	m.RegisterCommand(
		"rewind",
		ext.Command{Description: "Restore code, conversation, or both before a user turn", Handler: r.command},
	)
	m.OnToolPrepare(r.before)
	m.SubscribeEvent(ext.EventToolExecutionEnd, r.after)
	if err := m.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (r *rewind) before(ev ext.ToolCallEvent) *ext.ToolCallResult {
	if ev.ToolName != "write" && ev.ToolName != "edit" {
		return nil
	}
	if err := r.capture(ev); err != nil {
		return &ext.ToolCallResult{Block: true, Reason: "rewind snapshot failed: " + err.Error()}
	}
	return nil
}

func (r *rewind) capture(ev ext.ToolCallEvent) error {
	var local []string
	for _, path := range ev.Paths {
		if path != "" && !strings.HasPrefix(path, "mcp://") {
			local = append(local, path)
		}
	}
	if len(local) == 0 {
		return nil
	}
	if ev.ExecutionID == "" {
		return errors.New("rewind requires execution IDs; update the Phi host")
	}
	host := r.api.Host()
	turns, err := r.api.SessionTurns()
	if err != nil {
		return err
	}
	if len(turns) == 0 {
		return errors.New("no user turn is available for this tool call")
	}
	s, err := openStore(r.root, host.Cwd, host.SessionID)
	if err != nil {
		return err
	}
	for _, path := range local {
		before, err := readState(path)
		if err != nil {
			return err
		}
		s.Records = append(
			s.Records,
			snapshot{TurnID: turns[0].EntryID, ExecutionID: ev.ExecutionID, Path: path, Before: before},
		)
	}
	if err := s.save(); err != nil {
		return err
	}
	r.pending[ev.ExecutionID] = s.Path
	return nil
}

func (r *rewind) after(ev pxb.EventNotify) {
	path := r.pending[ev.ExecutionID]
	if path == "" {
		return
	}
	delete(r.pending, ev.ExecutionID)
	s := &store{Path: path}
	if err := s.finish(ev.ExecutionID, ev.IsError); err != nil {
		r.api.Notify("error", "Rewind could not finish its snapshot: "+err.Error())
	}
}

func (r *rewind) command(_ string, _ *ext.Context) error {
	host := r.api.Host()
	turns, err := r.api.SessionTurns()
	if err != nil {
		return err
	}
	if len(turns) == 0 {
		r.api.Notify("info", "No user turns to rewind")
		return nil
	}
	if err := r.api.SessionRewind(
		ext.RewindRequest{SessionID: host.SessionID, EntryID: turns[0].EntryID, ValidateOnly: true},
	); err != nil {
		return err
	}
	items := make([]ext.SelectItem, len(turns))
	for i, turn := range turns {
		items[i] = ext.SelectItem{
			Label:  promptLabel(turn.Text),
			Detail: turn.Timestamp.Local().Format("Jan 02 15:04:05"),
		}
	}
	for {
		choice, err := r.api.Select(ext.SelectRequest{Title: "Rewind · Select a message", Items: items})
		if err != nil || !choice.OK {
			return err
		}
		turn := turns[choice.Index]
		ids := make(map[string]bool)
		for _, turn := range turns[:choice.Index+1] {
			ids[turn.EntryID] = true
		}
		s, planErr := openStore(r.root, host.Cwd, host.SessionID)
		var files []restoreFile
		if planErr == nil {
			files, planErr = s.plan(ids)
		}
		if planErr != nil {
			r.api.Notify("warning", "Code restore unavailable: "+planErr.Error())
		}
		canRestoreCode := planErr == nil && len(files) > 0
		fileSummary := fmt.Sprintf("%d recorded files", len(files))
		if len(files) == 1 {
			fileSummary = "1 recorded file"
		}
		const (
			restoreBoth = iota
			restoreConversation
			restoreCode
			goBack
		)
		options := []ext.SelectItem{
			restoreBoth:         {Label: "Restore code and conversation", Detail: fileSummary + " + messages"},
			restoreConversation: {Label: "Restore conversation", Detail: "Keep current files"},
			restoreCode:         {Label: "Restore code", Detail: fileSummary + "; keep messages"},
			goBack:              {Label: "Never mind", Detail: "Back to messages"},
		}
		if !canRestoreCode {
			options = []ext.SelectItem{options[restoreConversation], options[goBack]}
		}
		restore, err := r.api.Select(ext.SelectRequest{
			Title: "Restore before: " + promptLabel(turn.Text), Items: options,
		})
		if err != nil {
			return err
		}
		if !restore.OK || restore.Index == len(options)-1 {
			continue
		}
		selected := restore.Index
		if !canRestoreCode {
			selected = restoreConversation
		}
		req := ext.RewindRequest{SessionID: host.SessionID, EntryID: turn.EntryID, ValidateOnly: true}
		if err := r.api.SessionRewind(req); err != nil {
			return err
		}
		result := "Conversation restored"
		if selected != restoreConversation {
			if _, err := s.restore(ids); err != nil {
				return err
			}
			result = "Restored " + fileSummary
		}
		if selected != restoreCode {
			req.ValidateOnly = false
			if err := r.api.SessionRewind(req); err != nil {
				return err
			}
			if selected != restoreConversation {
				result += " and conversation"
			}
		}
		r.api.Notify("info", result)
		return nil
	}
}

func promptLabel(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "(empty message)"
	}
	if len([]rune(text)) > 60 {
		text = string([]rune(text)[:60]) + "…"
	}
	return text
}
