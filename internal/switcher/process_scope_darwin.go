//go:build darwin

package switcher

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func processHome(pid int) (string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", err
	}
	return homeFromProcArgs(raw)
}

// Read just CODEX_HOME, never retain or log the complete process environment.
func homeFromProcArgs(raw []byte) (string, error) {
	if len(raw) < 5 {
		return "", errors.New("进程环境不可读")
	}
	nargs := int(binary.LittleEndian.Uint32(raw[:4]))
	if nargs < 1 || nargs > 65536 {
		return "", errors.New("进程参数格式无效")
	}
	data := raw[4:]
	i := bytes.IndexByte(data, 0)
	if i < 0 {
		return "", errors.New("进程环境不完整")
	}
	data = data[i+1:]
	for len(data) > 0 && data[0] == 0 {
		data = data[1:]
	}
	for n := 0; n < nargs; n++ {
		i = bytes.IndexByte(data, 0)
		if i < 0 {
			return "", errors.New("进程环境不完整")
		}
		data = data[i+1:]
	}
	for _, value := range bytes.Split(data, []byte{0}) {
		if bytes.HasPrefix(value, []byte("CODEX_HOME=")) {
			home := string(value[len("CODEX_HOME="):])
			if home != "" && filepath.IsAbs(home) {
				return canonicalHome(home), nil
			}
			if home != "" {
				return "", errors.New("进程使用相对 Codex 目录")
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return canonicalHome(filepath.Join(home, ".codex")), nil
}
func canonicalHome(home string) string {
	if actual, err := filepath.EvalSymlinks(home); err == nil {
		return actual
	}
	return filepath.Clean(home)
}
func (r MacRuntime) scopedProcesses() (map[int]codexProcess, error) {
	all, err := readProcesses()
	if err != nil {
		return nil, err
	}
	if r.Home == "" {
		return all, nil
	}
	knownCLI := r.Codex
	if knownCLI == "" {
		knownCLI, _ = FindCodex()
	}
	if knownCLI != "" {
		if real, err := filepath.EvalSymlinks(knownCLI); err == nil {
			knownCLI = real
		}
	}
	apps := FindApps()
	if r.App != "" {
		apps = append(apps, canonicalApp(r.App))
	}
	for pid, p := range all {
		for _, app := range apps {
			if processKind(p, app) == "桌面客户端" {
				p.App = app
				break
			}
		}
		kind := processKind(p, r.App)
		if kind == "" && knownCLI != "" && p.Path == knownCLI {
			p.KnownCLI = true
			kind = "Codex CLI"
		}
		if p.UID != os.Getuid() || kind == "" {
			continue
		}
		home, inspectErr := processHome(pid)
		if inspectErr != nil {
			// An exited process is harmless; every live unknown process blocks force.
			if err := unix.Kill(pid, 0); errors.Is(err, unix.ESRCH) {
				delete(all, pid)
				continue
			}
			return nil, errors.New("无法确认 Codex 进程的数据目录，已停止切换；请手动退出该进程")
		}
		p.Excluded = home != canonicalHome(r.Home)
		all[pid] = p
	}
	return all, nil
}
