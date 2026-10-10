package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex-switch/internal/switcher"
	tea "github.com/charmbracelet/bubbletea"
)

const editorSource = `model_provider="relay"
model="m"
[model_providers.relay]
base_url="https://example.com/v1"
experimental_bearer_token="sk-test-visible"
`

func TestEditablePlaintextPasteAndSave(t *testing.T) {
	var saved switcher.Provider
	m := newProviderEditor(func(_ context.Context, name string, p switcher.Provider) error {
		if name != "relay" {
			t.Error("wrong name")
		}
		saved = p
		return nil
	})
	m.Name.SetValue("relay")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(editorSource), Paste: true})
	m = next.(providerEditor)
	if !strings.Contains(m.View(), "sk-test-visible") || m.Config.Value() != editorSource {
		t.Fatal("paste not displayed in editable plaintext")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(providerEditor)
	if m.Config.Value() == editorSource {
		t.Fatal("pasted text cannot be edited")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(providerEditor)
	if !m.Saving || cmd == nil {
		t.Fatal("save did not start")
	}
	next, _ = m.Update(cmd())
	m = next.(providerEditor)
	if !m.Done || saved.APIKey != "sk-test-visible" || m.View() != "" {
		t.Fatal("save did not persist parsed account")
	}
}
func TestEditorErrorPreservesDraftAndFocus(t *testing.T) {
	m := newProviderEditor(func(context.Context, string, switcher.Provider) error { return errors.New("名称已存在") })
	m.Config.SetValue(editorSource)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(providerEditor)
	if m.Focus != 0 || m.Error == "" || m.Config.Value() != editorSource {
		t.Fatal("invalid name lost draft")
	}
	m.Name.SetValue("relay")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(providerEditor)
	if m.Focus != 1 || !m.Config.Focused() {
		t.Fatal("focus did not switch to config")
	}
	m.Config.SetValue("invalid TOML")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(providerEditor)
	if m.Done || m.Error == "" || m.Config.Value() != "invalid TOML" {
		t.Fatal("parse failure lost draft")
	}
	m.Config.SetValue(editorSource)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(providerEditor)
	next, _ = m.Update(cmd())
	m = next.(providerEditor)
	if m.Done || m.Error != "名称已存在" || m.Config.Value() != editorSource {
		t.Fatal("storage error lost draft")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !next.(providerEditor).Canceled {
		t.Fatal("cancel failed")
	}
}
