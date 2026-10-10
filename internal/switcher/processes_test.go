package switcher

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"syscall"
	"testing"
)

func TestProcessesWithLocalizedSingleAndDoubleDigitDays(t *testing.T) {
	for _, stamp := range []string{"Fri Oct 9 09:28:04 2026", "Sat Oct 10 00:07:39 2026", "五 10月/ 9 09:28:04 2026", "六 10月/10 00:07:39 2026"} {
		main := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT"
		helper := "/Applications/ChatGPT.app/Contents/Frameworks/Codex (Service).app/Contents/MacOS/Codex (Service)"
		raw := fmt.Sprintf("100 1 %d %s %s\n101 100 %d %s %s\n", os.Getuid(), stamp, main, os.Getuid(), stamp, helper)
		all := parseProcesses(raw)
		if len(all) != 2 || all[100].Path != main || all[101].Path != helper || all[100].Start != stamp {
			t.Fatalf("incorrect parsing for %q: %#v", stamp, all)
		}
		running, conflicts := processSummary(all, "/Applications/ChatGPT.app")
		if !running || len(conflicts) != 0 {
			t.Fatal("localized date misclassified desktop")
		}
	}
}

func processFixture() map[int]codexProcess {
	paths := map[int]string{1: "launchd", 10: "/Applications/Otty.app/Contents/MacOS/Otty", 11: "-zsh", 12: "/Users/test/.local/bin/codex-switch", 20: "/Applications/WebStorm.app/Contents/MacOS/webstorm", 21: "npm exec @agentclientprotocol/codex-acp@1.1", 22: "node", 23: "/Users/test/acp-agents/codex-acp/bin/codex", 24: "/usr/bin/helper", 30: "/Users/test/.codex/packages/app-server-daemon/release/bin/codex", 31: "/Users/test/.codex/packages/app-server-daemon/release/bin/codex-code-mode-host", 40: "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT", 41: "/Applications/ChatGPT.app/Contents/Resources/codex", 50: "/Applications/CC Switch.app/Contents/MacOS/cc-switch", 60: "node", 61: "/vendor/bin/codex", 62: "/usr/bin/unrelated", 70: "/vendor/bin/codex"}
	parents := map[int]int{10: 1, 11: 10, 12: 11, 20: 1, 21: 20, 22: 21, 23: 22, 24: 23, 30: 1, 31: 30, 40: 1, 41: 40, 50: 1, 60: 1, 61: 60, 62: 60, 70: 1}
	all := map[int]codexProcess{}
	for pid, path := range paths {
		all[pid] = codexProcess{PID: pid, Parent: parents[pid], UID: 501, Path: path, Start: "Thu Oct 9 09:00:00 2026"}
	}
	p := all[70]
	p.UID = 502
	all[70] = p
	return all
}
func TestForceTargetsKeepsHostApplicationsAndSharedWrappers(t *testing.T) {
	all := processFixture()
	targets, err := forceTargets(all, "/Applications/ChatGPT.app", 12, 501)
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{21, 22, 23, 24, 30, 31, 40, 41, 61} {
		if _, ok := targets[pid]; !ok {
			t.Fatalf("missed Codex process %d", pid)
		}
	}
	for _, pid := range []int{1, 10, 11, 12, 20, 50, 60, 62, 70} {
		if _, ok := targets[pid]; ok {
			t.Fatalf("unrelated process selected %d", pid)
		}
	}
}
func TestForceRejectsCodexAncestor(t *testing.T) {
	all := processFixture()
	p := all[11]
	p.Path = "/vendor/bin/codex"
	all[11] = p
	if _, err := forceTargets(all, "/Applications/ChatGPT.app", 12, 501); err == nil {
		t.Fatal("would terminate invoking Codex session")
	}
}
func TestSignalRevalidatesIdentityAndStopsChildrenFirst(t *testing.T) {
	all := processFixture()
	targets := map[int]codexProcess{30: all[30], 31: all[31]}
	called := []int{}
	if err := signalTargets(targets, all, syscall.SIGTERM, func(pid int, _ syscall.Signal) error { called = append(called, pid); return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(called, []int{31, 30}) {
		t.Fatal("parent signalled before child")
	}
	current := map[int]codexProcess{}
	for pid, p := range targets {
		p.Start = "new process"
		current[pid] = p
	}
	called = nil
	signalTargets(targets, current, syscall.SIGKILL, func(pid int, _ syscall.Signal) error { called = append(called, pid); return nil })
	if len(called) != 0 {
		t.Fatal("reused PID terminated")
	}
	if err := signalTargets(targets, all, syscall.SIGTERM, func(int, syscall.Signal) error { return syscall.EPERM }); err == nil {
		t.Fatal("permission failure ignored")
	}
	if err := signalTargets(targets, all, syscall.SIGTERM, func(int, syscall.Signal) error { return syscall.ESRCH }); err != nil {
		t.Fatal(err)
	}
}
func TestKnownDescendantStillTrackedAfterParentExits(t *testing.T) {
	all := processFixture()
	known := map[int]codexProcess{31: all[31]}
	p := all[31]
	p.Parent = 1
	survivors := survivingTargets(known, map[int]codexProcess{31: p}, map[int]codexProcess{})
	if len(survivors) != 1 {
		t.Fatal("orphaned Codex worker forgotten")
	}
}
func TestProcessParserAndClassification(t *testing.T) {
	all := parseProcesses("123 1 501 Thu Oct 9 09:00:00 2026 /Applications/ChatGPT.app/Contents/MacOS/ChatGPT\ninvalid line")
	if len(all) != 1 || processKind(all[123], "/Applications/ChatGPT.app") != "桌面客户端" {
		t.Fatal("invalid process parsing")
	}
	var conflict *ProcessConflictError
	if !errors.As(&ProcessConflictError{Details: "IDE 集成（PID 123）"}, &conflict) {
		t.Fatal("conflict not recognizable")
	}
}
