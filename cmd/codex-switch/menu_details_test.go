package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex-switch/internal/switcher"
	"github.com/charmbracelet/x/ansi"
)

func TestFinalReturnUsesSameEscapePathAndPreservesActionIndexes(t *testing.T) {
	m := menuModel{Title: "添加类型", Options: addAccountTypes(), Height: 24}
	for _, input := range []string{"\x1b", "\x1b[B\x1b[B\r"} {
		_, err := chooseMenuWithOptions(m, terminalOptions(input)...)
		if !errors.Is(err, errBack) {
			t.Fatalf("return selected an action: %v", err)
		}
	}
	i, err := chooseMenuWithOptions(m, terminalOptions("\x1b[B\r")...)
	if err != nil || i != 1 {
		t.Fatalf("provider selection shifted: %d %v", i, err)
	}
	prepared := withReturn(m)
	if len(prepared.Options) != 3 || prepared.Options[2] != "返回" || len(m.Options) != 2 {
		t.Fatal("missing final return or mutated caller options")
	}
}

func TestDetailReturnIsLastAndSafeBeforeAndAfterRefresh(t *testing.T) {
	m := newUsageMenu(context.Background(), switcher.Engine{}, "账户详情", []string{"备注编辑", "重新登录"}, nil, nil, true)
	if m.Options[m.Cursor] != "返回" || m.Cursor != len(m.Options)-1 {
		t.Fatal("detail default can trigger authentication")
	}
	m.decorate()
	if m.Options[m.Cursor] != "返回" {
		t.Fatal("refresh moved the return selection")
	}
	m.Width = 40
	view := m.View()
	if !strings.Contains(view, "R 刷新") || !strings.Contains(view, "Esc 返回") {
		t.Fatal("narrow details lost navigation or refresh hint")
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > m.Width {
			t.Fatal("detail footer overflows narrow terminal")
		}
	}
}

func TestBannerAndSessionTitlesFitTerminal(t *testing.T) {
	for _, dimensions := range [][2]int{{80, 24}, {60, 24}, {40, 24}, {80, 18}} {
		width, height := dimensions[0], dimensions[1]
		labels := []string{}
		for _, entry := range mainMenu(false) {
			labels = append(labels, entry.Label)
		}
		m := menuModel{Title: "codex-switch  当前账户：5X", Options: labels, Brand: true, Width: width, Height: height}
		text := ansi.Strip(m.View())
		if width == 80 && height == 24 {
			t.Log("\n" + text)
		}
		if strings.Contains(text, "github.com/") != (width >= 60 && height >= 24) {
			t.Fatal("banner did not collapse for narrow or short terminal")
		}
		if !strings.Contains(text, "当前账户：5X") || !strings.Contains(text, "> 账户列表") || strings.Contains(text, "返回\n") {
			t.Fatal("main menu identity or selection lost")
		}
		for _, line := range strings.Split(text, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("terminal overflow at width %d: %q", width, line)
			}
		}
		if strings.Count(text, "\n") > height {
			t.Fatal("main menu exceeds terminal height")
		}
	}
	s := switcher.Session{ID: "01a0ccb3-1f5d-76d2-b36a-2e53db617c61", Title: "会话配置体检", Provider: "openai", Updated: time.Now()}
	label := sessionLabel(s)
	if !strings.Contains(label, s.Title) || !strings.Contains(label, "01a0ccb3") || strings.Contains(label, s.ID) {
		t.Fatal("title or short ID missing")
	}
	s.Title = "\n\t"
	if sessionTitle(s) != "未命名会话" {
		t.Fatal("missing title fallback lost")
	}
}

func TestBannerLinksOnlyPointToProject(t *testing.T) {
	m := menuModel{Title: "codex-switch  当前账户：5X", Options: []string{"退出"}, Brand: true, Width: 60, Height: 24}
	view := m.View()
	if !strings.Contains(ansi.Strip(view), projectURL) || strings.Count(view, ansi.SetHyperlink(projectURL)) != 2 || strings.Count(view, ansi.ResetHyperlink()) != 2 {
		t.Fatal("name or full HTTPS address is not explicitly linked to project")
	}
	if strings.Contains(view, "file://") {
		t.Fatal("banner links to local files")
	}
	m.Width = 40
	view = m.View()
	if strings.Count(view, ansi.SetHyperlink(projectURL)) != 1 || strings.Contains(ansi.Strip(view), "https://") {
		t.Fatal("compact name lost link or included overflowing full address")
	}
}

func TestMainWarningsLeaveSpaceForMenuAndFooter(t *testing.T) {
	labels := []string{}
	for _, entry := range mainMenu(false) {
		labels = append(labels, entry.Label)
	}
	m := menuModel{Title: "codex-switch  当前账户：5X\n" + strings.Repeat("配置字段提示\n", 20), Options: labels, Height: 24, Width: 80, Brand: true}
	view := m.View()
	if strings.Count(view, "\n") > m.Height || !strings.Contains(view, "Enter 确认") || !strings.Contains(view, "> 账户列表") || !strings.Contains(view, "部分信息省略") {
		t.Fatal("warnings crowded out menu or keyboard hints")
	}
}
