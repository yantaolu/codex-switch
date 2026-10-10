package switcher

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"syscall"
	"time"
)

var ClientVersion = "dev"

// RPCRejectedError means the server explicitly rejected the request. Transport
// errors carry no such guarantee: a write may already have been applied.
type RPCRejectedError struct{ Message string }

func (e *RPCRejectedError) Error() string { return e.Message }

type nativeClient struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	rpc usageRPC
}

func startNative(ctx context.Context, bin, home string, experimental bool) (*nativeClient, error) {
	if bin == "" {
		var err error
		bin, err = FindCodex()
		if err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, bin, "app-server", "--listen", "stdio://")
	cmd.Env = scopedEnv(home)
	cmd.Dir = home
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.New("无法打开 Codex 原生接口")
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		out.Close()
		return nil, errors.New("无法打开 Codex 原生接口")
	}
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return nil, errors.New("无法启动官方 Codex CLI")
	}
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 4096), 20<<20)
	c := &nativeClient{cmd: cmd, in: in, rpc: usageRPC{scan: scan, out: in}}
	if _, err = c.rpc.call("initialize", map[string]any{"clientInfo": map[string]any{"name": "codex_switch", "version": ClientVersion}, "capabilities": map[string]any{"experimentalApi": experimental}}); err != nil {
		c.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if err = json.NewEncoder(in).Encode(map[string]any{"method": "initialized"}); err != nil {
		c.Close()
		return nil, errors.New("Codex 原生接口初始化中断")
	}
	return c, nil
}
func (c *nativeClient) Close() { c.in.Close(); _ = c.cmd.Cancel(); _ = c.cmd.Wait() }
