package main

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

var accent = lipgloss.Color("#189BFF")
var green = lipgloss.Color("#8DAA00")
var subtle = lipgloss.AdaptiveColor{Light: "#607987", Dark: "#8B9BA8"}
var titleStyle = lipgloss.NewStyle().Foreground(accent)
var currentStyle = lipgloss.NewStyle().Foreground(green)
var selectedStyle = lipgloss.NewStyle().Foreground(green)
var mutedStyle = lipgloss.NewStyle().Foreground(subtle)
var errorStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#CC5966", Dark: "#E78896"})

const projectURL = "https://github.com/yantaolu/codex-switch"

func init() {
	if term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" {
		lipgloss.SetColorProfile(termenv.ANSI256)
	}
}
func renderTitle(line string) string {
	line = strings.TrimSpace(safeUI(line))
	if i := strings.Index(line, "当前账户："); i >= 0 {
		return titleStyle.Render(line[:i]) + currentStyle.Render(line[i:])
	}
	if strings.Contains(line, "操作未完成") {
		return errorStyle.Render(line)
	}
	if strings.Contains(line, "操作已完成") || strings.Contains(line, "已添加：") {
		return currentStyle.Render(line)
	}
	return titleStyle.Render(line)
}
func renderOption(label string, selected bool) string {
	label = safeUI(label)
	if selected {
		return selectedStyle.Render("> " + label)
	}
	parts := strings.SplitN(label, " · ", 2)
	if len(parts) == 2 {
		rest := " · " + parts[1]
		rest = strings.ReplaceAll(rest, " · 当前", currentStyle.Render(" · 当前"))
		return "  " + parts[0] + mutedStyle.Render(rest)
	}
	return "  " + label
}

func mainBanner() []string {
	logo := []string{`   ____ ____`, `  / ___/ ___|`, ` | |   \___ \`, ` | |___ ___) |`, `  \____|____/`}
	right := []string{"", projectLink("codex-switch"), projectLink(projectURL), currentStyle.Render("账户切换，简单直接"), ""}
	lines := make([]string, len(logo))
	for i, line := range logo {
		lines[i] = currentStyle.Render(line+strings.Repeat(" ", 16-len(line))) + right[i]
	}
	return lines
}

func projectLink(label string) string {
	return ansi.SetHyperlink(projectURL) + titleStyle.Render(label) + ansi.ResetHyperlink()
}
