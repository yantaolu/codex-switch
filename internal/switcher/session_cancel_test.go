package switcher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCancelAfterForkRequestCleansChildAndKeepsSource(t *testing.T) {
	e, _ := fixture(t)
	id := "01a0ccb3-1f5d-76d2-b36a-2e53db617c61"
	path := filepath.Join(e.Home, "sessions", "test.jsonl")
	os.MkdirAll(filepath.Dir(path), 0700)
	source := []byte(`{"type":"session_meta","payload":{"id":"` + id + `","model_provider":"codex_switch","source":"cli"}}` + "\n")
	os.WriteFile(path, source, 0600)
	pidPath := filepath.Join(t.TempDir(), "pid")
	quoted := "'" + strings.ReplaceAll(pidPath, "'", "'\\''") + "'"
	script := "#!/bin/sh\nwhile IFS= read -r line; do\ncase \"$line\" in\n*'\"method\":\"initialize\"'*) printf '%s\\n' '{\"id\":1,\"result\":{}}' ;;\n*'\"method\":\"thread/fork\"'*) sleep 30 & printf '%s' \"$!\" > " + quoted + "; wait ;;\nesac\ndone\n"
	bin := filepath.Join(t.TempDir(), "fake-codex")
	os.WriteFile(bin, []byte(script), 0700)
	e.Runtime = MacRuntime{Codex: bin, Home: e.Home}
	before, _ := snapshot(e.Home)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := e.ContinueSession(ctx, id); result <- err }()
	deadline := time.Now().Add(3 * time.Second)
	var pid []byte
	for time.Now().Before(deadline) {
		pid, _ = os.ReadFile(pidPath)
		if len(pid) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(pid) == 0 {
		t.Fatal("fork request not sent")
	}
	cancel()
	select {
	case err := <-result:
		var uncertain *SessionCopyUnconfirmedError
		if !errors.As(err, &uncertain) || !errors.Is(err, context.Canceled) {
			t.Fatalf("missing uncertain cancellation result: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server cleanup blocked")
	}
	after, _ := snapshot(e.Home)
	if !same(before, after) {
		t.Fatal("cancel changed account configuration")
	}
	saved, _ := os.ReadFile(path)
	if string(saved) != string(source) {
		t.Fatal("cancel changed source history")
	}
	stat, _ := exec.Command("/bin/ps", "-p", string(pid), "-o", "stat=").Output()
	if s := strings.TrimSpace(string(stat)); s != "" && !strings.HasPrefix(s, "Z") {
		t.Fatalf("child still running: %s", s)
	}
	unlock, err := e.Store.Lock()
	if err != nil {
		t.Fatal("lock leaked after cancellation")
	}
	unlock()
}

func TestCancelLoginCleansProcessGroupAndTemporaryCredentials(t *testing.T) {
	e, _ := fixture(t)
	before, err := snapshot(e.Home)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(filepath.Join(e.Store.Dir, "vault.enc"))
	if err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(t.TempDir(), "pid")
	bin := filepath.Join(t.TempDir(), "fake-codex")
	quoted := "'" + strings.ReplaceAll(pidPath, "'", "'\\''") + "'"
	script := "#!/bin/sh\nsleep 30 &\nprintf '%s' \"$!\" > " + quoted + "\nwait\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Runtime = MacRuntime{Codex: bin, Home: e.Home}
	e = e.WithContext(ctx)
	result := make(chan error, 1)
	go func() { result <- e.AddChatGPT("new-account", false, false) }()
	deadline := time.Now().Add(3 * time.Second)
	var pid []byte
	for time.Now().Before(deadline) {
		pid, _ = os.ReadFile(pidPath)
		if len(pid) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(pid) == 0 {
		cancel()
		t.Fatal("login child not started")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("login cleanup blocked")
	}
	stat, _ := exec.Command("/bin/ps", "-p", string(pid), "-o", "stat=").Output()
	if s := strings.TrimSpace(string(stat)); s != "" && !strings.HasPrefix(s, "Z") {
		t.Fatalf("login child still running: %s", s)
	}
	after, err := snapshot(e.Home)
	if err != nil || !same(before, after) {
		t.Fatal("cancel changed current credentials")
	}
	stateAfter, err := os.ReadFile(filepath.Join(e.Store.Dir, "vault.enc"))
	if err != nil || string(stateBefore) != string(stateAfter) {
		t.Fatal("cancel changed encrypted profiles")
	}
	temporary, err := filepath.Glob(filepath.Join(e.Store.Dir, "login-*"))
	if err != nil || len(temporary) != 0 {
		t.Fatal("temporary login credentials retained")
	}
	unlock, err := e.Store.Lock()
	if err != nil {
		t.Fatal("login cancellation leaked lock")
	}
	unlock()
}
