package main

import (
	"codex-switch/internal/switcher"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSwitchFailureEnterReturns(t *testing.T) {
	m := switchFailure("20X", errors.New("检测到独立 CLI"))
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(menuModel)
	if !got.Selected || got.Options[got.Cursor] != "返回" || !m.ErrorTitle || !strings.Contains(m.View(), "检测到独立 CLI") {
		t.Fatal("failure page must default to return and show red error")
	}
}

func TestForceChoiceIsExplicitAndDefaultsToReturn(t *testing.T) {
	forced := 0
	conflict := func() error { return &switcher.ProcessConflictError{Details: "IDE 集成"} }
	err := runAccountSwitch("20X", conflict, func(m menuModel) (int, error) {
		if len(m.Options) != 3 || m.Cursor != 2 || !strings.Contains(m.Title, "结束") {
			t.Fatal("unsafe force default")
		}
		return 1, nil
	}, func() error { forced++; return nil })
	if err != nil || forced != 1 {
		t.Fatal("explicit force choice failed")
	}
	err = runAccountSwitch("20X", conflict, func(m menuModel) (int, error) { return m.Cursor, nil }, func() error { t.Fatal("default killed a process"); return nil })
	if !errors.Is(err, errBack) {
		t.Fatal("default must return")
	}
}
func TestUseForceArguments(t *testing.T) {
	for _, args := range [][]string{{"20X", "--force"}, {"--force", "20X"}} {
		name, force, err := parseUse(args)
		if err != nil || name != "20X" || !force {
			t.Fatal("force args rejected")
		}
	}
	for _, args := range [][]string{{"--force"}, {"20X", "--unknown"}, {"20X", "other"}, {"20X", "--force", "--force"}} {
		if _, _, err := parseUse(args); err == nil {
			t.Fatal("invalid use args accepted")
		}
	}
}
func TestSwitchRetryRunsOnlyAfterExplicitSelection(t *testing.T) {
	attempts := 0
	err := runAccountSwitch("20X", func() error {
		attempts++
		if attempts == 1 {
			return errors.New("busy")
		}
		return nil
	}, func(menuModel) (int, error) { return 0, nil })
	if err != nil || attempts != 2 {
		t.Fatal("explicit retry failed")
	}
}
func TestSwitchFailureReturnDoesNotBubbleError(t *testing.T) {
	err := runAccountSwitch("20X", func() error { return errors.New("busy") }, func(menuModel) (int, error) { return 1, nil })
	if !errors.Is(err, errBack) {
		t.Fatal("failure would appear on main menu")
	}
}
