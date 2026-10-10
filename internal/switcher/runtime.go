package switcher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Runtime interface {
	Stop() (bool, error)
	Login(home string, device bool) error
	Verify(home string) error
}
type MacRuntime struct {
	App     string
	Codex   string
	Home    string
	Force   bool
	Context context.Context
}

func canonicalApp(app string) string {
	if app == "" {
		return ""
	}
	if absolute, err := filepath.Abs(app); err == nil {
		app = absolute
	}
	if actual, err := filepath.EvalSymlinks(app); err == nil {
		return actual
	}
	return filepath.Clean(app)
}

func FindCodex() (string, error) {
	if p, e := exec.LookPath("codex"); e == nil {
		return p, nil
	}
	for _, p := range []string{"/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex", "/Applications/Codex.app/Contents/Resources/codex"} {
		if _, e := os.Stat(p); e == nil {
			return p, nil
		}
	}
	return "", errors.New("未找到官方 Codex CLI")
}
func hasCodexResources(app string) bool {
	for _, relative := range []string{"Contents/Resources/codex", "Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex"} {
		if st, err := os.Stat(filepath.Join(app, relative)); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
			return true
		}
	}
	return false
}
func FindApps() []string {
	apps := []string{}
	for _, app := range []string{"/Applications/ChatGPT.app", "/Applications/Codex.app"} {
		if hasCodexResources(app) {
			apps = append(apps, canonicalApp(app))
		}
	}
	return apps
}
func FindApp() string {
	apps := FindApps()
	if len(apps) > 0 {
		return apps[0]
	}
	return ""
}
func (r MacRuntime) runningApps(all map[int]codexProcess) []string {
	seen := map[string]bool{}
	apps := []string{}
	for _, p := range all {
		if p.UID != os.Getuid() || processKind(p, r.App) != "桌面客户端" {
			continue
		}
		app := p.App
		if app == "" {
			app = r.App
		}
		if app != "" && !seen[app] {
			seen[app] = true
			apps = append(apps, app)
		}
	}
	sort.Strings(apps)
	return apps
}
func command(timeout time.Duration, bin string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if exec.CommandContext(ctx, bin, args...).Run() != nil {
		return errors.New("系统命令失败或超时")
	}
	return nil
}
func (r MacRuntime) processes() (bool, bool, error) {
	all, err := r.scopedProcesses()
	if err != nil {
		return false, false, err
	}
	app, details := processSummary(all, r.App)
	return app, len(details) > 0, nil
}

// This check never terminates processes, even in force mode. A client launched
// during browser authentication must be explicitly stopped by a new operation.
func (r MacRuntime) EnsureStopped() error {
	all, err := r.scopedProcesses()
	if err != nil {
		return err
	}
	app, details := processSummary(all, r.App)
	if app {
		details = append(details, "桌面客户端")
	}
	if len(details) > 0 {
		return &ProcessConflictError{Details: strings.Join(details, "、")}
	}
	return nil
}

func (r MacRuntime) codexPath() (string, error) {
	if r.Codex != "" {
		if st, err := os.Stat(r.Codex); err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return "", errors.New("指定 Codex CLI 不可执行")
		}
		return r.Codex, nil
	}
	return FindCodex()
}

func (r MacRuntime) Stop() (bool, error) {
	if r.App != "" && !hasCodexResources(r.App) {
		return false, errors.New("指定 APP 缺少 Codex 组件；请检查 --app 路径")
	}
	if err := r.context().Err(); err != nil {
		return false, err
	}
	if r.Force {
		return r.ForceStop()
	}
	app, cli, e := r.processes()
	if e != nil {
		return false, e
	}
	if cli {
		all, err := r.scopedProcesses()
		if err != nil {
			return false, err
		}
		_, details := processSummary(all, r.App)
		return false, &ProcessConflictError{Details: strings.Join(details, "、")}
	}
	if !app {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(r.context(), 35*time.Second)
	defer cancel()
	all, err := r.scopedProcesses()
	if err != nil {
		return false, err
	}
	for _, app := range r.runningApps(all) {
		script := "tell application " + strconv.Quote(app) + " to quit"
		if exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script).Run() != nil {
			return false, errors.New("APP 未正常退出，认证未切换")
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := r.context().Err(); err != nil {
			return true, err
		}
		a, c, e := r.processes()
		if e != nil {
			return true, e
		}
		if c {
			all, er := r.scopedProcesses()
			if er != nil {
				return true, er
			}
			_, details := processSummary(all, r.App)
			return true, &ProcessConflictError{Details: strings.Join(details, "、")}
		}
		if !a {
			return true, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false, errors.New("等待 APP 退出超时，认证未切换")
}
func scopedEnv(home string) []string {
	e := []string{}
	for _, v := range os.Environ() {
		if strings.HasPrefix(v, "CODEX_HOME=") || strings.HasPrefix(v, "OPENAI_API_KEY=") || strings.HasPrefix(v, "CODEX_ACCESS_TOKEN=") || strings.HasPrefix(v, "CODEX_SQLITE_HOME=") {
			continue
		}
		e = append(e, v)
	}
	return append(e, "CODEX_HOME="+home)
}
func (r MacRuntime) Login(home string, device bool) error {
	args := []string{"-c", `cli_auth_credentials_store="file"`, "login"}
	if device {
		args = append(args, "--device-auth")
	}
	bin, err := r.codexPath()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(r.context(), bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	cmd.Env = scopedEnv(home)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if cmd.Run() != nil {
		if err := r.context().Err(); err != nil {
			return err
		}
		return errors.New("官方登录未完成，当前账户未修改")
	}
	return nil
}
func (r MacRuntime) Verify(home string) error {
	ctx, cancel := context.WithTimeout(r.context(), 10*time.Second)
	defer cancel()
	bin, err := r.codexPath()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "login", "status")
	cmd.Env = scopedEnv(home)
	if cmd.Run() != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.New("Codex 无法读取目标认证")
	}
	return nil
}

func (r MacRuntime) context() context.Context {
	if r.Context != nil {
		return r.Context
	}
	return context.Background()
}

func (r MacRuntime) Preflight() error { _, err := r.codexPath(); return err }
