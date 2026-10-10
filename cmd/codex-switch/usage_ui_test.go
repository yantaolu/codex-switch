package main

import (
	"context"
	"strings"
	"testing"

	"codex-switch/internal/switcher"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestMenuWrapAndEmpty(t *testing.T) {
	m := menuModel{Options: []string{"a", "b", "c"}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(menuModel)
	if m.Cursor != 2 {
		t.Fatal("up did not wrap")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if next.(menuModel).Cursor != 0 {
		t.Fatal("down did not wrap")
	}
	for _, key := range []tea.KeyType{tea.KeyUp, tea.KeyDown, tea.KeyEnd, tea.KeyEnter} {
		next, _ = (menuModel{}).Update(tea.KeyMsg{Type: key})
		if next.(menuModel).Cursor != 0 || next.(menuModel).Selected {
			t.Fatal("empty menu invalid")
		}
	}
}
func TestHelpDisplayWidthAndNarrowLayout(t *testing.T) {
	text := helpText(100)
	column := -1
	for _, row := range helpRows {
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, row.Command) && strings.HasSuffix(line, row.Description) {
				i := strings.LastIndex(line, row.Description)
				w := lipgloss.Width(line[:i])
				if column < 0 {
					column = w
				}
				if w != column {
					t.Fatalf("help column %q %d vs %d", line, w, column)
				}
			}
		}
	}
	for _, line := range strings.Split(helpText(40), "\n") {
		if lipgloss.Width(line) > 40 {
			t.Fatalf("narrow help overflow: %q", line)
		}
	}
}
func TestUsageMenuRefreshPreservesSelectionAndShowsFailures(t *testing.T) {
	m := usageMenu{menuModel: menuModel{Height: 24, Cursor: 1}, Ctx: context.Background(), BaseTitle: "账户列表", BaseOptions: []string{"one", "返回"}, Names: []string{"one"}, Profiles: map[string]switcher.Profile{"one": {Kind: "chatgpt"}}, Results: map[string]switcher.UsageSnapshot{}, Queue: []string{"one"}}
	next, _ := m.Update(usageResultMsg{Name: "one", Snapshot: switcher.UsageSnapshot{Error: "登录过期"}})
	m = next.(usageMenu)
	if m.Cursor != 1 || !strings.Contains(m.View(), "查询失败") || !strings.Contains(m.View(), "R 刷新") {
		t.Fatal("async view lost selection or error")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = next.(usageMenu)
	if !m.Force || len(m.Queue) != 1 || cmd == nil {
		t.Fatal("refresh did not queue")
	}
}
func TestQueryConfigJSONValidation(t *testing.T) {
	for _, s := range []string{`{"type":"generic","url":"https://example.com","fields":{"remaining":"data.balance"}}`, `{"type":"sub2api"}`, `{"type":"off"}`} {
		if _, err := decodeUsage([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{`{"type":"generic"}`, `{"type":"sub2api","typo":true}`, `{"type":"off"} {}`} {
		if _, err := decodeUsage([]byte(s)); err == nil {
			t.Fatal("invalid settings accepted")
		}
	}
}

func TestUsageLoadingDoesNotMoveMenu(t *testing.T) {
	m := usageMenu{menuModel: menuModel{Height: 24}, BaseTitle: "账户列表", BaseOptions: []string{"one", "返回"}, Names: []string{"one"}, Profiles: map[string]switcher.Profile{"one": {Kind: "chatgpt"}}, Results: map[string]switcher.UsageSnapshot{}, Queue: []string{"one"}}
	m.decorate()
	before := m.View()
	next, cmd := m.Update(usageResultMsg{Name: "one", Snapshot: switcher.UsageSnapshot{Plan: "pro"}})
	after := next.(usageMenu).View()
	if cmd != nil || strings.Contains(before, "正在查询") || strings.Count(before, "\n") != strings.Count(after, "\n") {
		t.Fatal("loading shifted list or scheduled another refresh")
	}
}

func TestAddMenuOnlyCreatesNewAccounts(t *testing.T) {
	types := addAccountTypes()
	if len(types) != 2 || types[0] != "官方登录" || types[1] != "第三方配置" {
		t.Fatal("ambiguous save-current entry remains")
	}
}
