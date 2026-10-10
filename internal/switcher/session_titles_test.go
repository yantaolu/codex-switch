package switcher

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionTitlesReadLocalIndexWithoutChangingHistory(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	id := "01a0ccb3-1f5d-76d2-b36a-2e53db617c61"
	path := filepath.Join(home, "sessions", "source.jsonl")
	history := []byte(`{"type":"session_meta","payload":{"id":"` + id + `","model_provider":"codex_switch","source":"cli"}}` + "\nnot history to decode\n")
	if err := os.WriteFile(path, history, 0600); err != nil {
		t.Fatal(err)
	}
	index := []byte(`{"id":"` + id + `","thread_name":"旧标题","updated_at":"2026-10-09T00:00:00Z"}` + "\n" +
		`{"id":"unrelated","thread_name":"其他会话"}` + "\ninvalid entry\n" +
		`{"id":"` + id + `","thread_name":" 最新\n 标题 \t","updated_at":"2026-10-10T00:00:00Z"}` + "\n" +
		`{"id":"` + id + `","thread_name":"过时的追加记录","updated_at":"2026-10-08T00:00:00Z"}` + "\n{partial")
	indexPath := filepath.Join(home, "session_index.jsonl")
	if err := os.WriteFile(indexPath, index, 0600); err != nil {
		t.Fatal(err)
	}
	e := Engine{Home: home}
	page, err := e.sessionPage(context.Background(), "openai", "", 0, 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].Title != "最新 标题" {
		t.Fatalf("title or session changed: %+v %v", page, err)
	}
	after, _ := os.ReadFile(path)
	indexAfter, _ := os.ReadFile(indexPath)
	if string(after) != string(history) || string(indexAfter) != string(index) {
		t.Fatal("title lookup changed local sessions")
	}
}

func TestSessionTitlesPreferBoundedTailAndSkipOversizedRecords(t *testing.T) {
	for _, mode := range []string{"large-index", "oversized-line", "damaged-line", "cross-buffer-record"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			f, err := os.Create(filepath.Join(home, "session_index.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			f.WriteString(`{"id":"wanted","thread_name":"old"}` + "\n")
			switch mode {
			case "large-index":
				if err = f.Truncate(titleScanBytes + 8192); err != nil {
					t.Fatal(err)
				}
				f.Seek(0, io.SeekEnd)
				f.WriteString("\n")
			case "oversized-line":
				f.WriteString(strings.Repeat("x", titleRecordBytes+64) + "\n")
			case "damaged-line":
				f.WriteString("{bad json}\n")
			case "cross-buffer-record":
				record, _ := json.Marshal(map[string]string{"id": "other", "thread_name": strings.Repeat("x", 128<<10)})
				f.Write(record)
				f.WriteString("\n")
			}
			f.WriteString(`{"id":"wanted","thread_name":"latest","updated_at":"2026-10-10T00:00:00Z"}` + "\n")
			items := []Session{{ID: "wanted"}}
			partial := readSessionTitles(context.Background(), home, items)
			if items[0].Title != "latest" || partial != (mode != "cross-buffer-record") {
				t.Fatalf("latest title hidden: %+v partial=%v", items, partial)
			}
		})
	}
}

func TestSessionTitlesMissingUnsafeOrCancelledAreOptional(t *testing.T) {
	for _, kind := range []string{"missing", "symlink", "directory", "malformed", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "session_index.jsonl")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "symlink":
				target := filepath.Join(t.TempDir(), "private")
				os.WriteFile(target, []byte(`{"id":"wanted","thread_name":"不应读取"}`), 0600)
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				os.Mkdir(path, 0700)
			case "malformed":
				os.WriteFile(path, []byte("not JSON\n"), 0600)
			case "cancelled":
				os.WriteFile(path, []byte(`{"id":"wanted","thread_name":"不应读取"}`), 0600)
				cancel()
			}
			items := []Session{{ID: "wanted"}}
			readSessionTitles(ctx, home, items)
			if items[0].Title != "" {
				t.Fatal("optional title read escaped its boundary")
			}
		})
	}
}

func TestSessionTitlesAreBoundedAndKeepPageSelection(t *testing.T) {
	home := t.TempDir()
	record, _ := json.Marshal(map[string]string{"id": "wanted", "thread_name": strings.Repeat("中", 300) + "\x1b\n", "updated_at": "2026-10-10T00:00:00Z"})
	os.WriteFile(filepath.Join(home, "session_index.jsonl"), record, 0600)
	items := []Session{{ID: "wanted"}, {ID: "missing"}}
	readSessionTitles(context.Background(), home, items)
	if len([]rune(items[0].Title)) != 241 || !strings.HasSuffix(items[0].Title, "…") || items[1].Title != "" || strings.ContainsAny(items[0].Title, "\x1b\n") {
		t.Fatal("title length or page mapping is invalid")
	}
}
