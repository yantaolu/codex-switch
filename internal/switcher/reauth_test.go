package switcher

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func expiringAuth(user, refresh string, expiry time.Time) []byte {
	raw := auth(user, refresh)
	var d map[string]any
	json.Unmarshal(raw, &d)
	claims, _ := json.Marshal(map[string]any{"sub": user, "exp": expiry.Unix()})
	obj(d["tokens"])["access_token"] = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
	raw, _ = json.Marshal(d)
	return raw
}

type authRuntime struct {
	*fakeRuntime
	refreshes, logins    int
	refreshErr, loginErr error
	newAuth              []byte
}

func (r *authRuntime) Refresh(home string) error {
	r.refreshes++
	if r.refreshErr != nil {
		return r.refreshErr
	}
	return atomicWrite(filepath.Join(home, "auth.json"), r.newAuth)
}
func (r *authRuntime) Login(home string, _ bool) error {
	r.logins++
	if r.loginErr != nil {
		return r.loginErr
	}
	return atomicWrite(filepath.Join(home, "auth.json"), r.newAuth)
}
func expiredTarget(t *testing.T) (Engine, *authRuntime, Snapshot) {
	t.Helper()
	e, base := fixture(t)
	st, _ := e.Store.Load()
	p := st.Profiles["bob"]
	p.Auth = expiringAuth("bob", "expired-refresh", time.Now().Add(-time.Hour))
	st.Profiles["bob"] = p
	e.Store.Save(st)
	r := &authRuntime{fakeRuntime: base, newAuth: expiringAuth("bob", "latest-refresh", time.Now().Add(time.Hour))}
	e.Runtime = r
	before, _ := snapshot(e.Home)
	return e, r, before
}
func TestExpiredAccountRefreshThenSwitch(t *testing.T) {
	e, r, _ := expiredTarget(t)
	if err := e.Use("bob"); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Store.Load()
	live, _ := readSafe(filepath.Join(e.Home, "auth.json"))
	if r.refreshes != 1 || r.logins != 0 || st.Current != "bob" || !bytes.Equal(st.Profiles["bob"].Auth, r.newAuth) || !bytes.Equal(live, r.newAuth) {
		t.Fatal("renewed credentials not saved and switched")
	}
}
func TestExpiredAccountFallsBackToLogin(t *testing.T) {
	e, r, _ := expiredTarget(t)
	r.refreshErr = errors.New("refresh revoked")
	if err := e.Use("bob"); err != nil {
		t.Fatal(err)
	}
	if r.refreshes != 1 || r.logins != 1 {
		t.Fatal("browser login fallback missing")
	}
}
func TestReauthenticationFailureKeepsCurrentAccount(t *testing.T) {
	for _, mode := range []string{"cancel", "wrong-account", "expired-result"} {
		t.Run(mode, func(t *testing.T) {
			e, r, before := expiredTarget(t)
			r.refreshErr = errors.New("unavailable")
			switch mode {
			case "cancel":
				r.loginErr = errors.New("cancelled")
			case "wrong-account":
				r.newAuth = expiringAuth("different-user", "wrong", time.Now().Add(time.Hour))
			case "expired-result":
				r.newAuth = expiringAuth("bob", "expired", time.Now().Add(-time.Hour))
			}
			if e.Use("bob") == nil {
				t.Fatal("invalid reauthentication accepted")
			}
			after, _ := snapshot(e.Home)
			st, _ := e.Store.Load()
			if !same(before, after) || st.Current != "alice" || e.Store.HasJournal() {
				t.Fatal("failed login changed current account")
			}
		})
	}
}
func TestSwitchRollbackKeepsRenewedTargetCredentials(t *testing.T) {
	e, r, before := expiredTarget(t)
	r.verifyErr = errors.New("verification failed")
	if e.Use("bob") == nil {
		t.Fatal("expected switch failure")
	}
	st, _ := e.Store.Load()
	after, _ := snapshot(e.Home)
	if st.Current != "alice" || !same(before, after) || !bytes.Equal(st.Profiles["bob"].Auth, r.newAuth) {
		t.Fatal("rollback discarded renewed credentials")
	}
}
func TestValidAccountDoesNotReauthenticate(t *testing.T) {
	e, r, _ := expiredTarget(t)
	st, _ := e.Store.Load()
	p := st.Profiles["bob"]
	p.Auth = r.newAuth
	st.Profiles["bob"] = p
	e.Store.Save(st)
	if err := e.Use("bob"); err != nil {
		t.Fatal(err)
	}
	if r.refreshes != 0 || r.logins != 0 {
		t.Fatal("valid login unnecessarily renewed")
	}
}
func TestCurrentAccountRollbackUsesRenewedCredentials(t *testing.T) {
	e, r, _ := expiredTarget(t)
	old := expiringAuth("alice", "old-current-refresh", time.Now().Add(-time.Hour))
	os.WriteFile(filepath.Join(e.Home, "auth.json"), old, 0600)
	r.newAuth = expiringAuth("alice", "renewed-current-refresh", time.Now().Add(time.Hour))
	r.verifyErr = errors.New("fail after renewing current")
	if e.Use("alice") == nil {
		t.Fatal("expected verification failure")
	}
	live, _ := readSafe(filepath.Join(e.Home, "auth.json"))
	st, _ := e.Store.Load()
	if st.Current != "alice" || !bytes.Equal(live, r.newAuth) || !bytes.Equal(st.Profiles["alice"].Auth, r.newAuth) {
		t.Fatal("rollback restored a rotated old refresh token")
	}
}

func TestManagedRefreshHelper(t *testing.T) {
	if os.Getenv("CXS_REFRESH_HELPER") != "1" {
		return
	}
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var req struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			os.Exit(2)
		}
		if req.Method == "initialized" {
			continue
		}
		if req.Method == "account/read" {
			if req.Params["refreshToken"] != true {
				os.Exit(3)
			}
			home := os.Getenv("CODEX_HOME")
			raw, err := readSafe(filepath.Join(home, "auth.json"))
			if err != nil || !authExpired(raw) {
				os.Exit(4)
			}
			if atomicWrite(filepath.Join(home, "auth.json"), expiringAuth("alice", "managed-new-refresh", time.Now().Add(time.Hour))) != nil {
				os.Exit(5)
			}
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"id": req.ID, "result": map[string]any{}})
	}
	os.Exit(0)
}
func TestNativeManagedRefreshProtocol(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CXS_REFRESH_HELPER", "1")
	t.Setenv("CXS_REFRESH_BIN", bin)
	wrapper := filepath.Join(t.TempDir(), "codex")
	if err = os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \"$CXS_REFRESH_BIN\" -test.run='^TestManagedRefreshHelper$'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	atomicWrite(filepath.Join(home, "auth.json"), expiringAuth("alice", "old-refresh", time.Now().Add(-time.Hour)))
	if err = (MacRuntime{Codex: wrapper}).Refresh(home); err != nil {
		t.Fatal(err)
	}
	raw, _ := readSafe(filepath.Join(home, "auth.json"))
	if authExpired(raw) || !bytes.Contains(raw, []byte("managed-new-refresh")) {
		t.Fatal("native refresh did not persist renewed auth")
	}
}
