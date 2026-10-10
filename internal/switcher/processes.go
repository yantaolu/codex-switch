package switcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type codexProcess struct {
	PID, Parent, UID int
	Start, Path      string
	Excluded         bool
	App              string
	KnownCLI         bool
}
type ProcessConflictError struct{ Details string }

func (e *ProcessConflictError) Error() string {
	return "仍有 Codex 进程运行：" + e.Details + "；请退出对应会话，或选择强制切换"
}

func parseProcesses(raw string) map[int]codexProcess {
	result := map[int]codexProcess{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 8 {
			continue
		}
		pid, e1 := strconv.Atoi(parts[0])
		parent, e2 := strconv.Atoi(parts[1])
		uid, e3 := strconv.Atoi(parts[2])
		if e1 != nil || e2 != nil || e3 != nil || pid <= 0 {
			continue
		}
		// lstart is localized: Chinese dates may occupy four or five fields.
		// The year ends the timestamp; do not consume part of the executable path.
		yearEnd := -1
		for i := 3; i < len(parts)-1 && i < 9; i++ {
			if year, err := strconv.Atoi(parts[i]); err == nil && len(parts[i]) == 4 && year >= 1900 && year <= 9999 {
				yearEnd = i + 1
				break
			}
		}
		if yearEnd < 0 {
			continue
		}
		result[pid] = codexProcess{PID: pid, Parent: parent, UID: uid, Start: strings.Join(parts[3:yearEnd], " "), Path: strings.Join(parts[yearEnd:], " ")}
	}
	return result
}
func readProcesses() (map[int]codexProcess, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,uid=,lstart=,comm=")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "LC_ALL=") && !strings.HasPrefix(value, "LANG=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C", "LANG=C")
	b, err := cmd.Output()
	if err != nil {
		return nil, errors.New("无法检查运行进程，已停止切换")
	}
	all := parseProcesses(string(b))
	if len(all) == 0 && strings.TrimSpace(string(b)) != "" {
		return nil, errors.New("运行进程格式无法解析，已停止切换")
	}
	return all, nil
}
func processKind(p codexProcess, app string) string {
	if p.Excluded {
		return ""
	}
	if p.App != "" {
		return "桌面客户端"
	}
	if p.KnownCLI {
		return "Codex CLI"
	}
	if app != "" && strings.HasPrefix(p.Path, app+"/") {
		if filepath.Base(p.Path) == "browser_crashpad_handler" {
			return ""
		}
		return "桌面客户端"
	}
	base := filepath.Base(p.Path)
	if base != "codex" && !strings.HasPrefix(base, "codex-aarch64") && !strings.HasPrefix(base, "codex-x86_64") {
		return ""
	}
	if strings.Contains(p.Path, "/app-server-daemon/") {
		return "后台服务"
	}
	if strings.Contains(p.Path, "/acp-agents/") || strings.Contains(p.Path, "/codex-acp/") {
		return "IDE 集成"
	}
	return "Codex CLI"
}
func processSummary(all map[int]codexProcess, app string) (bool, []string) {
	app = canonicalApp(app)
	appRunning := false
	details := []string{}
	for _, p := range all {
		if p.UID != os.Getuid() || p.PID == os.Getpid() {
			continue
		}
		kind := processKind(p, app)
		if kind == "桌面客户端" {
			appRunning = true
		} else if kind != "" {
			details = append(details, fmt.Sprintf("%s（PID %d）", kind, p.PID))
		}
	}
	sort.Strings(details)
	return appRunning, details
}

// Add dedicated wrappers only when every child is already a Codex target.
// Stop at IDEs, shells, terminal applications and shared Node processes.
func forceTargets(all map[int]codexProcess, app string, self, uid int) (map[int]codexProcess, error) {
	app = canonicalApp(app)
	protected := map[int]bool{}
	pid := self
	for pid > 0 && !protected[pid] {
		protected[pid] = true
		p, ok := all[pid]
		if !ok {
			break
		}
		pid = p.Parent
	}
	targets := map[int]codexProcess{}
	for pid, p := range all {
		if p.UID == uid && processKind(p, app) != "" {
			if protected[pid] {
				return nil, errors.New("当前命令运行在 Codex 关联会话中，请从独立终端执行强制切换")
			}
			targets[pid] = p
		}
	}
	for changed := true; changed; {
		changed = false
		for pid, p := range all {
			if p.UID != uid || protected[pid] || p.Excluded {
				continue
			}
			if _, ok := targets[pid]; ok {
				continue
			}
			if _, ok := targets[p.Parent]; ok {
				targets[pid] = p
				changed = true
				continue
			}
			base := filepath.Base(p.Path)
			wrapper := base == "node" || base == "npm" || strings.HasPrefix(p.Path, "npm exec @agentclientprotocol/codex-acp")
			if !wrapper {
				continue
			}
			hasChild, allSelected := false, true
			for _, child := range all {
				if child.Parent == pid {
					hasChild = true
					if _, ok := targets[child.PID]; !ok {
						allSelected = false
					}
				}
			}
			if hasChild && allSelected {
				targets[pid] = p
				changed = true
			}
		}
	}
	return targets, nil
}
func signalTargets(targets, current map[int]codexProcess, sig syscall.Signal, kill func(int, syscall.Signal) error) error {
	ids := []int{}
	for id := range targets {
		ids = append(ids, id)
	}
	depth := func(pid int) int {
		n := 0
		seen := map[int]bool{}
		for !seen[pid] {
			seen[pid] = true
			p, ok := targets[pid]
			if !ok {
				break
			}
			n++
			pid = p.Parent
		}
		return n
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := depth(ids[i]), depth(ids[j])
		if a != b {
			return a > b
		}
		return ids[i] > ids[j]
	})
	for _, id := range ids {
		p := targets[id]
		now, ok := current[id]
		// Revalidate process identity immediately before sending a signal.
		if !ok || p.Start != now.Start || p.Path != now.Path || p.UID != now.UID {
			continue
		}
		if err := kill(id, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("无法结束 Codex 进程 PID %d，账户未切换", id)
		}
	}
	return nil
}
func survivingTargets(known, current, selected map[int]codexProcess) map[int]codexProcess {
	for pid, p := range selected {
		known[pid] = p
	}
	result := map[int]codexProcess{}
	for pid, p := range known {
		if now, ok := current[pid]; ok && p.Start == now.Start && p.Path == now.Path && p.UID == now.UID {
			result[pid] = now
		}
	}
	return result
}
func (r MacRuntime) ForceStop() (bool, error) {
	if r.App != "" && !hasCodexResources(r.App) {
		return false, errors.New("指定 APP 缺少 Codex 组件")
	}
	all, err := r.scopedProcesses()
	if err != nil {
		return false, err
	}
	wasRunning, _ := processSummary(all, r.App)
	if _, err = forceTargets(all, r.App, os.Getpid(), os.Getuid()); err != nil {
		return false, err
	}
	if wasRunning {
		// Give the desktop a chance to save state; explicit force handles refusal below.
		for _, app := range r.runningApps(all) {
			_ = command(8*time.Second, "/usr/bin/osascript", "-e", "tell application "+strconv.Quote(app)+" to quit")
		}
	}
	for _, p := range all {
		if processKind(p, r.App) == "后台服务" {
			ctx, cancel := context.WithTimeout(r.context(), 8*time.Second)
			bin, pathErr := r.codexPath()
			if pathErr != nil {
				cancel()
				return wasRunning, pathErr
			}
			cmd := exec.CommandContext(ctx, bin, "app-server", "daemon", "stop")
			cmd.Env = scopedEnv(r.Home)
			_ = cmd.Run()
			cancel()
			break
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	known := map[int]codexProcess{}
	terminated := map[int]string{}
	for {
		if err := r.context().Err(); err != nil {
			return wasRunning, err
		}
		all, err = r.scopedProcesses()
		if err != nil {
			return wasRunning, err
		}
		targets, er := forceTargets(all, r.App, os.Getpid(), os.Getuid())
		if er != nil {
			return wasRunning, er
		}
		targets = survivingTargets(known, all, targets)
		if len(targets) == 0 {
			return wasRunning, nil
		}
		current, er := r.scopedProcesses()
		if er != nil {
			return wasRunning, er
		}
		sig := syscall.SIGTERM
		if time.Now().After(deadline) {
			sig = syscall.SIGKILL
		}
		toSignal := map[int]codexProcess{}
		for pid, p := range targets {
			if sig == syscall.SIGKILL || terminated[pid] != p.Start+p.Path {
				toSignal[pid] = p
			}
		}
		if er = signalTargets(toSignal, current, sig, syscall.Kill); er != nil {
			return wasRunning, er
		}
		for pid, p := range toSignal {
			terminated[pid] = p.Start + p.Path
		}
		if sig == syscall.SIGKILL {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	for i := 0; i < 15; i++ {
		all, err = r.scopedProcesses()
		if err != nil {
			return wasRunning, err
		}
		targets, er := forceTargets(all, r.App, os.Getpid(), os.Getuid())
		if er != nil {
			return wasRunning, er
		}
		targets = survivingTargets(known, all, targets)
		if len(targets) == 0 {
			return wasRunning, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return wasRunning, errors.New("Codex 进程仍在运行或被自动重启，账户未切换；请关闭对应 IDE 会话后重试")
}
