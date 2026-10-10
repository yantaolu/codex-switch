package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"codex-switch/internal/switcher"
	tea "github.com/charmbracelet/bubbletea"
)

func terminalOptions(input string) []tea.ProgramOption {
	return []tea.ProgramOption{tea.WithInput(strings.NewReader(input)), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler()}
}

func TestEscapeThroughTerminalDecoderOnAllMenuPages(t *testing.T) {
	for _, menu := range []menuModel{
		{Title: "会话接续", Options: []string{"thread"}, Height: 24},
		{Title: "接续失败", Options: []string{"重试接续", "返回"}, Cursor: 1, Height: 24, ErrorTitle: true},
		{Title: "接续副本", Options: []string{"返回"}, Height: 24},
	} {
		_, err := chooseMenuWithOptions(menu, terminalOptions("\x1b")...)
		if !errors.Is(err, errBack) {
			t.Fatalf("escape did not return from %q: %v", menu.Title, err)
		}
	}
}

func TestSessionCancellationWaitsForWorkerCleanup(t *testing.T) {
	for _, test := range []struct {
		input string
		want  error
	}{{"\x1b", errBack}, {"\x03", errExit}} {
		cleaned := make(chan struct{})
		started := make(chan struct{})
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		result := make(chan error, 1)
		go func() {
			_, err := runSessionOperationWithOptions(func(ctx context.Context) (string, error) {
				close(started)
				defer close(cleaned)
				<-ctx.Done()
				return "", ctx.Err()
			}, tea.WithInput(reader), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
			result <- err
		}()
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("worker not started")
		}
		if _, err := writer.Write([]byte(test.input)); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if !errors.Is(err, test.want) {
				t.Fatalf("wrong cancellation result: %v", err)
			}
			select {
			case <-cleaned:
			default:
				t.Fatal("returned before worker cleanup")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("terminal cancellation blocked")
		}
	}
}

func TestCancelDoesNotDiscardCompletedCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := sessionOperation{Ctx: ctx, Cancel: cancel}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(sessionOperation)
	next, cmd := m.Update(sessionOperationResult{ID: "copy-id"})
	m = next.(sessionOperation)
	if !m.Finished || m.Result.ID != "copy-id" || cmd == nil {
		t.Fatal("completed copy hidden by cancellation")
	}
	uncertain := &switcher.SessionCopyUnconfirmedError{Cause: context.Canceled}
	if !isUnconfirmedCopy(uncertain) {
		t.Fatal("uncertain copy treated as safe cancellation")
	}
}
