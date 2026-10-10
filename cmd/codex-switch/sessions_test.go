package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-switch/internal/switcher"
)

func TestSwitchLogShowsCompletedAndInterruptedOperations(t *testing.T) {
	dir := t.TempDir()
	events := []switcher.AuditEvent{
		{ID: "a", Time: "2026-10-09T00:00:00Z", Status: "started", From: "5X", To: "PlainAI"},
		{ID: "a", Time: "2026-10-09T00:00:01Z", Status: "failed", Stage: "verify", From: "5X", To: "PlainAI", RolledBack: true},
		{ID: "b", Time: "2026-10-09T00:00:02Z", Status: "started", From: "5X", To: "20X"},
	}
	f, err := os.Create(filepath.Join(dir, "switch.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		json.NewEncoder(f).Encode(event)
	}
	f.Close()
	lines, err := switchLogLines(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !strings.Contains(lines[0], "已回滚") || !strings.Contains(lines[0], "认证核验") || !strings.Contains(lines[1], "未完成") {
		t.Fatalf("incorrect lines: %v", lines)
	}
}

func TestSessionAndLogHelpWithoutCredentials(t *testing.T) {
	for _, command := range []string{"sessions", "logs"} {
		if !knownCommand(command) {
			t.Fatal("command missing")
		}
		if err := run([]string{"--home", t.TempDir(), "--store", t.TempDir(), command, "--help"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := run([]string{"--store", t.TempDir(), "logs"}); err != nil {
		t.Fatal(err)
	}
}
