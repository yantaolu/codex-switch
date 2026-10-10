package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"codex-switch/internal/switcher"
	tea "github.com/charmbracelet/bubbletea"
)

// The editor owns one worker at a time. Leaving the program cancels and waits
// even on renderer/input failure, so no save can outlive the editor.
type editorSaveTask struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}
type editorSaves struct{ active *editorSaveTask }

func (s *editorSaves) start(work func(context.Context) error) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	task := &editorSaveTask{cancel: cancel, done: make(chan struct{})}
	s.active = task
	go func() {
		defer close(task.done)
		defer cancel()
		if ctx.Err() != nil {
			task.err = ctx.Err()
		} else {
			task.err = work(ctx)
		}
	}()
	return func() tea.Msg {
		<-task.done
		return providerSavedMsg{Err: task.err}
	}
}
func (s *editorSaves) cancel() {
	if s != nil && s.active != nil {
		s.active.cancel()
	}
}
func (s *editorSaves) wait() {
	if s != nil && s.active != nil {
		<-s.active.done
	}
}
func runEditorProgram(model tea.Model, saves *editorSaves, options ...tea.ProgramOption) (tea.Model, error) {
	result, err := tea.NewProgram(model, append([]tea.ProgramOption{tea.WithAltScreen()}, options...)...).Run()
	saves.cancel()
	saves.wait()
	if err != nil {
		if saves.active != nil && (switcher.IsCommitted(saves.active.err) || switcher.IsUnconfirmed(saves.active.err) || switcher.NeedsRecovery(saves.active.err)) {
			return result, saves.active.err
		}
		if saves.active != nil && saves.active.err == nil {
			return result, &switcher.OperationError{Committed: true, Cause: errors.New("配置已保存，但编辑界面异常退出")}
		}
		return result, errors.New("配置编辑界面异常退出，保存任务已停止")
	}
	// A program-level quit can arrive without a key message. Settle the worker's
	// outcome instead of treating an in-flight committed save as a cancellation.
	if saves.active != nil {
		switch m := result.(type) {
		case providerEditor:
			if m.Saving {
				m.Canceling = true
				result, _ = m.Update(providerSavedMsg{Err: saves.active.err})
			}
		case usageEditor:
			if m.Saving {
				m.Canceling = true
				result, _ = m.Update(providerSavedMsg{Err: saves.active.err})
			}
		}
	}
	return result, nil
}

func editorResult(m providerEditor) error {
	if m.Exit {
		if m.Done {
			fmt.Fprintln(os.Stderr, "配置已保存")
		}
		if m.SaveWarning != nil {
			fmt.Fprintln(os.Stderr, m.SaveWarning)
		}
		if m.OperationErr != nil {
			fmt.Fprintln(os.Stderr, m.OperationErr)
		}
		return errExit
	}
	if !m.Done {
		if m.OperationErr != nil {
			return m.OperationErr
		}
		return errBack
	}
	return m.SaveWarning
}
