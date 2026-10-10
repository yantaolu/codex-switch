package main

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"codex-switch/internal/switcher"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSavingEscapeAndCtrlCWaitForCleanupThroughTerminalDecoder(t *testing.T) {
	for _, test := range []struct {
		input string
		want  error
	}{{"\x1b", errBack}, {"\x03", errExit}} {
		t.Run(test.input, func(t *testing.T) {
			started, cancelled, cleaned, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			m := newProviderEditor(func(ctx context.Context, _ string, _ switcher.Provider) error {
				close(started)
				<-ctx.Done()
				close(cancelled)
				<-release
				close(cleaned)
				return ctx.Err()
			})
			m.Name.SetValue("relay")
			m.Config.SetValue(editorSource)
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			result := make(chan error, 1)
			go func() {
				model, err := runEditorProgram(m, m.Saves, tea.WithInput(reader), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
				if err == nil {
					err = editorResult(model.(providerEditor))
				}
				result <- err
			}()
			if _, err := writer.Write([]byte("\x13")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("save not started")
			}
			if _, err := writer.Write([]byte(test.input)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("save ignored cancellation")
			}
			select {
			case err := <-result:
				t.Fatalf("returned before cleanup: %v", err)
			default:
			}
			once.Do(func() { close(release) })
			select {
			case err := <-result:
				if !errors.Is(err, test.want) {
					t.Fatalf("wrong navigation: %v", err)
				}
				select {
				case <-cleaned:
				default:
					t.Fatal("cleanup missing")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("save cleanup blocked")
			}
		})
	}
}

func TestEditorCancellationRetainsCommittedAndPendingResults(t *testing.T) {
	for _, outcome := range []error{nil, &switcher.OperationError{Committed: true, Cause: errors.New("sync warning")}, &switcher.OperationError{Pending: true, Cause: context.Canceled}, &switcher.OperationError{Unconfirmed: true, Cause: context.Canceled}} {
		m := newProviderEditor(nil)
		m.Saving = true
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = next.(providerEditor)
		if !m.Canceling || cmd != nil || m.View() == "" {
			t.Fatal("cancel closed editor before save settled")
		}
		next, cmd = m.Update(providerSavedMsg{Err: outcome})
		m = next.(providerEditor)
		if cmd == nil {
			t.Fatal("settled cancellation did not quit")
		}
		if outcome == nil || switcher.IsCommitted(outcome) {
			if !m.Done || m.Canceled || editorResult(m) != outcome {
				t.Fatal("committed result hidden by cancel")
			}
		} else if m.Done || m.OperationErr != outcome || editorResult(m) != outcome {
			t.Fatal("unsafe outcome hidden by cancel")
		}
	}
}

func TestUsageEditorSavingSharesCancellationAndOutcome(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		base := newProviderEditor(nil)
		base.Saving = true
		m := usageEditor{providerEditor: base}
		next, cmd := m.Update(tea.KeyMsg{Type: key})
		m = next.(usageEditor)
		if !m.Canceling || cmd != nil || m.View() == "" {
			t.Fatal("usage save ignores cancellation")
		}
		warning := &switcher.OperationError{Committed: true, Cause: errors.New("applied")}
		next, cmd = m.Update(providerSavedMsg{Err: warning})
		m = next.(usageEditor)
		if !m.Done || m.SaveWarning != warning || cmd == nil {
			t.Fatal("usage save lost committed warning")
		}
	}
}

func TestEditorPendingOrUnconfirmedSaveCannotRetry(t *testing.T) {
	for _, outcome := range []error{&switcher.OperationError{Pending: true, Cause: errors.New("pending")}, &switcher.OperationError{Unconfirmed: true, Cause: errors.New("uncertain")}} {
		m := newProviderEditor(nil)
		m.Name.SetValue("relay")
		m.Config.SetValue(editorSource)
		m.Saving = true
		next, _ := m.Update(providerSavedMsg{Err: outcome})
		m = next.(providerEditor)
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
		m = next.(providerEditor)
		if !m.Pending || m.Saving || cmd != nil {
			t.Fatal("unsafe retry accepted")
		}
	}
}

func TestProgramTerminationWaitsAndPreservesWorkerOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, cleaned := make(chan struct{}), make(chan struct{})
	m := newProviderEditor(func(saveCtx context.Context, _ string, _ switcher.Provider) error {
		close(started)
		<-saveCtx.Done()
		defer close(cleaned)
		return &switcher.OperationError{Pending: true, Cause: saveCtx.Err()}
	})
	m.Name.SetValue("relay")
	m.Config.SetValue(editorSource)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	result := make(chan error, 1)
	go func() {
		_, err := runEditorProgram(m, m.Saves, tea.WithContext(ctx), tea.WithInput(reader), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
		result <- err
	}()
	writer.Write([]byte("\x13"))
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("save not started")
	}
	cancel()
	select {
	case err := <-result:
		if !switcher.NeedsRecovery(err) {
			t.Fatalf("lost pending result: %v", err)
		}
		select {
		case <-cleaned:
		default:
			t.Fatal("worker outlived program")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("program shutdown blocked")
	}
}
