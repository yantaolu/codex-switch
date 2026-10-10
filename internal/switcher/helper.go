package switcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StableHelper preserves the invoked symlink and Homebrew's opt prefix rather
// than recording a versioned Cellar executable in Codex's credential command.
func StableHelper(invoked string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return stableHelperFor(executable, invoked)
}
func stableHelperFor(executable, invoked string) (string, error) {
	real, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	candidates := []string{}
	if i := strings.Index(real, "/Cellar/"); i >= 0 {
		parts := strings.Split(real[i+len("/Cellar/"):], "/")
		if len(parts) == 4 && parts[2] == "bin" && parts[3] == "codex-switch" {
			candidates = append(candidates, filepath.Join(real[:i], "opt", parts[0], "bin/codex-switch"))
		}
	}
	if invoked != "" {
		if strings.ContainsRune(invoked, os.PathSeparator) {
			if p, e := filepath.Abs(invoked); e == nil {
				candidates = append(candidates, p)
			}
		} else if p, e := exec.LookPath(invoked); e == nil {
			candidates = append(candidates, p)
		}
	}
	for _, candidate := range candidates {
		other, e := os.Stat(candidate)
		if e == nil && os.SameFile(info, other) {
			return candidate, nil
		}
	}
	return real, nil
}
