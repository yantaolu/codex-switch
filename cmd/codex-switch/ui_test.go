package main

import (
	"codex-switch/internal/switcher"
	"errors"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
)

func TestMainMenuAndRecoveryVisibility(t *testing.T) {
	for _, pending := range []bool{false, true} {
		entries := mainMenu(pending)
		found := false
		for _, entry := range entries {
			if entry.Action == "recover" {
				found = true
			}
			if entry.Action == "restore" {
				t.Fatal("restore still visible")
			}
		}
		if found != pending || entries[0].Action != "list" || entries[len(entries)-1].Action != "exit" || entries[len(entries)-1].Label != "退出" {
			t.Fatal("incorrect flat menu")
		}
	}
}

func TestMenuKeyboard(t *testing.T) {
	m := menuModel{Options: []string{"first", "second"}}
	for _, key := range []tea.KeyType{tea.KeyDown, tea.KeyDown, tea.KeyEnter} {
		updated, _ := m.Update(tea.KeyMsg{Type: key})
		m = updated.(menuModel)
	}
	if m.Cursor != 0 || !m.Selected {
		t.Fatal("keyboard selection failed")
	}
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		updated, _ := (menuModel{}).Update(tea.KeyMsg{Type: key})
		n := updated.(menuModel)
		if !n.Canceled && !n.Exit {
			t.Fatal("cancel failed")
		}
	}
}

func TestSecretInputAndValidation(t *testing.T) {
	input := textinput.New()
	input.EchoMode = textinput.EchoPassword
	input.SetValue("private-test-key")
	m := inputModel{Input: input, Secret: true, Validate: required}
	if strings.Contains(m.View(), "private-test-key") {
		t.Fatal("secret exposed")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(inputModel).View() != "" {
		t.Fatal("completed secret left on screen")
	}
	input.SetValue("")
	m.Input = input
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(inputModel).Done || updated.(inputModel).Error == "" {
		t.Fatal("blank input accepted")
	}
}

func TestParseModelIDs(t *testing.T) {
	if _, err := parseModels("one,one"); err == nil {
		t.Fatal("duplicate accepted")
	}
	models, err := parseModels("one, two")
	if err != nil || len(models) != 2 || models[1].ID != "two" {
		t.Fatal("model IDs not parsed")
	}
}

func TestRepeatedEnterSelectsSafeDefaults(t *testing.T) {
	st := switcher.State{Current: "5X", Profiles: map[string]switcher.Profile{"5X": {Kind: "chatgpt"}, "20X": {Kind: "chatgpt"}, "relay": {Kind: "provider"}}}
	for _, removing := range []bool{false, true} {
		m, names := accountMenu(st, false, removing)
		for j := 0; j < 3; j++ {
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(menuModel)
		}
		if removing {
			if m.Cursor != len(names) || m.Options[m.Cursor] != "返回" {
				t.Fatal("delete default is destructive")
			}
		} else if m.Cursor >= len(names) || names[m.Cursor] != st.Current {
			t.Fatal("switch default is not current account")
		}
	}
	models := []switcher.Model{{ID: "one"}, {ID: "two"}, {ID: "three"}, {ID: "four"}, {ID: "five"}, {ID: "six"}, {ID: "seven"}, {ID: "eight"}}
	m := defaultModelMenu(models, "seven")
	if m.Cursor != 6 || len(m.Options) != 9 || m.Options[8] != "返回" {
		t.Fatal("model default or long list truncated")
	}
}
func TestCommittedAndPendingSwitchNeverOfferRetry(t *testing.T) {
	for _, outcome := range []*switcher.OperationError{{Committed: true, Cause: errors.New("audit warning")}, {Pending: true, Cause: errors.New("recovery required")}, {Unconfirmed: true, Cause: errors.New("result unknown")}} {
		calls := 0
		err := runAccountSwitch("target", func() error { calls++; return outcome }, func(m menuModel) (int, error) {
			if len(m.Options) != 1 || m.Options[0] != "返回" {
				t.Fatal("unsafe retry offered")
			}
			return 0, nil
		})
		if calls != 1 || err != nil && !errors.Is(err, errBack) {
			t.Fatal("unexpected outcome")
		}
	}
}
