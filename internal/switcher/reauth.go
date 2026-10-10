package switcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func authExpired(auth []byte) bool {
	var d struct {
		Tokens struct {
			Access string `json:"access_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(auth, &d) != nil {
		return true
	}
	exp := number(jwtClaims(d.Tokens.Access)["exp"])
	return exp == nil || int64(*exp) <= time.Now().Add(30*time.Second).Unix()
}

// Expiry describes a locally cached timestamp; it does not prove that an online
// service has not revoked a token.
func CredentialStatus(p Profile) string {
	if p.Kind != "chatgpt" {
		return "API Key · 未联网核验"
	}
	var data struct {
		Tokens struct {
			Access string `json:"access_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(p.Auth, &data) != nil {
		return "凭据结构无效"
	}
	exp := number(jwtClaims(data.Tokens.Access)["exp"])
	if exp == nil {
		return "有效期未知 · 切换时重新认证"
	}
	if authExpired(p.Auth) {
		return "已到期 · 切换时重新认证"
	}
	return "本地有效期至 " + time.Unix(int64(*exp), 0).Local().Format("01-02 15:04")
}

func (r MacRuntime) Refresh(home string) error {
	ctx, cancel := context.WithTimeout(r.context(), 20*time.Second)
	defer cancel()
	client, err := startNative(ctx, r.Codex, home, false)
	if err != nil {
		return err
	}
	defer client.Close()
	rpc := &client.rpc
	_, err = rpc.call("account/read", map[string]any{"refreshToken": true})
	return err
}

// Renew in isolation, then persist the new credentials before starting a switch.
func (e Engine) reauthenticate(st *State, name string, p Profile) (Profile, error) {
	if p.Kind != "chatgpt" || !authExpired(p.Auth) {
		return p, nil
	}
	if e.OnMessage != nil {
		e.OnMessage("官方账户 " + name + " 凭据过期或有效期未知，正在重新认证；请选择原账户及工作空间。")
	}
	home, err := os.MkdirTemp(e.Store.Dir, "reauth-*")
	if err != nil {
		return p, err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(home)
		}
	}()
	if err = atomicWrite(filepath.Join(home, "auth.json"), p.Auth); err != nil {
		return p, err
	}
	if err = atomicWrite(filepath.Join(home, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n")); err != nil {
		return p, err
	}
	refreshed := false
	var refreshErr error
	if runtime, ok := e.Runtime.(interface{ Refresh(string) error }); ok {
		refreshErr = runtime.Refresh(home)
		// The native process can write rotated credentials before its RPC is
		// interrupted. Always inspect the file before any deletion or fallback.
		raw, readErr := readSafe(filepath.Join(home, "auth.json"))
		if readErr == nil && !authExpired(raw) {
			refreshed = true
		}
	}
	if !refreshed {
		if err = e.contextErr(); err != nil {
			return p, err
		}
		if errors.Is(refreshErr, context.Canceled) || errors.Is(refreshErr, context.DeadlineExceeded) {
			return p, refreshErr
		}
		// Remove stale credentials so login cannot short-circuit on an expired cache.
		if err = os.Remove(filepath.Join(home, "auth.json")); err != nil && !os.IsNotExist(err) {
			return p, err
		}
		refreshErr = e.Runtime.Login(home, false)
		if refreshErr != nil {
			// Login may also finish writing credentials before returning an
			// interrupted result; validate and save them in the common path.
			raw, readErr := readSafe(filepath.Join(home, "auth.json"))
			if readErr != nil || authExpired(raw) {
				if errors.Is(refreshErr, context.Canceled) || errors.Is(refreshErr, context.DeadlineExceeded) {
					return p, refreshErr
				}
				return p, errors.New("目标账户重新认证未完成，当前账户未切换")
			}
		}
	}
	raw, err := readSafe(filepath.Join(home, "auth.json"))
	if err != nil {
		return p, errors.New("重新认证未生成完整凭据")
	}
	expected, err := AuthIdentity(p.Auth)
	if err != nil {
		return p, err
	}
	actual, err := AuthIdentity(raw)
	if err != nil || authExpired(raw) {
		return p, errors.New("重新认证凭据不完整或已过期，未切换账户")
	}
	if actual != expected {
		return p, errors.New("登录账户或工作空间与目标账户不一致，请登录原账户后重试")
	}
	p.Auth = raw
	p.AuthPendingSync = true
	st.Profiles[name] = p
	if err = e.Store.Save(*st); err != nil {
		keep = true
		if IsCommitted(err) || IsUnconfirmed(err) {
			return p, &OperationError{Unconfirmed: true, Cause: fmt.Errorf("最新凭据存档的目录同步或结果未确认，未切换账户；完整认证保留在 %s", home)}
		}
		return p, fmt.Errorf("最新登录凭据保存失败，未切换账户；完整认证保留在 %s", home)
	}
	if err = e.contextErr(); err != nil {
		return p, err
	}
	if errors.Is(refreshErr, context.Canceled) || errors.Is(refreshErr, context.DeadlineExceeded) {
		return p, refreshErr
	}
	return p, nil
}

// Relogin updates the saved identity without activating a different account.
func (e Engine) Relogin(name string) error {
	unlock, err := e.Store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = e.checkPending(); err != nil {
		return err
	}
	st, err := e.state()
	if err != nil {
		return err
	}
	p, ok := st.Profiles[name]
	if !ok || p.Kind != "chatgpt" {
		return errors.New("请选择官方账户")
	}
	if runtime, ok := e.Runtime.(interface{ Preflight() error }); ok {
		if err := runtime.Preflight(); err != nil {
			return err
		}
	}
	if st.Current == name {
		if _, err = e.Runtime.Stop(); err != nil {
			return err
		}
	}
	before, err := snapshot(e.Home)
	if err != nil {
		return err
	}
	home, err := os.MkdirTemp(e.Store.Dir, "reauth-*")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(home)
		}
	}()
	if err = atomicWrite(filepath.Join(home, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n")); err != nil {
		return err
	}
	loginErr := e.Runtime.Login(home, false)
	raw, err := readSafe(filepath.Join(home, "auth.json"))
	if err != nil {
		if loginErr != nil {
			return loginErr
		}
		return err
	}
	expected, err := AuthIdentity(p.Auth)
	if err != nil {
		return err
	}
	actual, err := AuthIdentity(raw)
	if err != nil || actual != expected || authExpired(raw) {
		return errors.New("登录账户、工作空间或凭据有效期不符合目标账户")
	}
	p.Auth = raw
	p.AuthPendingSync = st.Current == name
	st.Profiles[name] = p
	if err = e.Store.Save(st); err != nil {
		keep = true
		if IsCommitted(err) && st.Current != name {
			return err
		}
		if IsCommitted(err) || IsUnconfirmed(err) {
			return &OperationError{Unconfirmed: true, Cause: fmt.Errorf("最新凭据存档的目录同步或结果未确认，当前认证未更新；完整认证保留在 %s", home)}
		}
		return fmt.Errorf("最新凭据保存失败；认证保留在 %s", home)
	}
	if err = e.contextErr(); err != nil {
		return err
	}
	if errors.Is(loginErr, context.Canceled) || errors.Is(loginErr, context.DeadlineExceeded) {
		return loginErr
	}
	if st.Current == name {
		if err = e.ensureStopped(); err != nil {
			return err
		}
		live, err := snapshot(e.Home)
		if err != nil || !same(live, before) {
			return errors.New("最新凭据已保存，检测到外部配置修改，当前认证未更新")
		}
		if err = atomicWrite(filepath.Join(e.Home, "auth.json"), raw); err != nil {
			var write *replacedWriteError
			if errors.As(err, &write) {
				written, readErr := readSafe(filepath.Join(e.Home, "auth.json"))
				if readErr == nil && bytes.Equal(written, raw) {
					return &OperationError{Committed: true, Cause: errors.New("最新认证已更新，但目录同步未确认")}
				}
				return &OperationError{Unconfirmed: true, Cause: errors.New("最新凭据已保存，当前认证替换结果未确认；请检查状态")}
			}
			return errors.New("最新凭据已保存，当前认证更新失败；请重新切换该账户")
		}
	}
	return nil
}
