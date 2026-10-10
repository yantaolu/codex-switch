package switcher

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func auth(user, token string) []byte {
	claim := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"` + user + `", "exp":4102444800}`))
	b, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]any{"account_id": "org", "access_token": "e30." + claim + ".sig", "id_token": "e30." + claim + ".sig", "refresh_token": token}})
	return b
}

type fakeRuntime struct {
	stops, starts                int
	stopErr, verifyErr, startErr error
	onStop                       func()
	onStart                      func()
}

func (r *fakeRuntime) Stop() (bool, error) {
	r.stops++
	if r.onStop != nil {
		r.onStop()
	}
	return true, r.stopErr
}
func (r *fakeRuntime) Start() error {
	r.starts++
	if r.onStart != nil {
		r.onStart()
	}
	return r.startErr
}
func (r *fakeRuntime) Verify(string) error      { return r.verifyErr }
func (r *fakeRuntime) Login(string, bool) error { return errors.New("not used") }
func fixture(t *testing.T) (Engine, *fakeRuntime) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "codex")
	os.Mkdir(home, 0700)
	storeDir := filepath.Join(dir, "store")
	os.Mkdir(storeDir, 0700)
	s := &Store{Dir: storeDir, key: bytes.Repeat([]byte{3}, 32)}
	r := &fakeRuntime{}
	e := Engine{Store: s, Home: home, Helper: "/usr/local/bin/codex-switch", Runtime: r}
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_provider = \"openai\"\nforced_login_method = \"chatgpt\"\n# user comment\n[mcp_servers.test]\ncommand = \"test\"\n"), 0600)
	os.WriteFile(filepath.Join(home, "auth.json"), auth("alice", "old"), 0600)
	if err := e.AddChatGPT("alice", true, false); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Load()
	st.Profiles["bob"] = Profile{Kind: "chatgpt", Auth: auth("bob", "bob-token")}
	s.Save(st)
	return e, r
}
func TestConfigPreservesMultilineAndComments(t *testing.T) {
	b := []byte("# root\nmodel = 'old' # note\nmodel_provider = \"openai\"\ndeveloper_instructions = '''\n[model_providers.codex_switch]\nmodel = 'do not edit'\n'''\n[mcp_servers.x]\ncommand = \"node\"\n[model_providers.other]\nname = 'untouched'\n")
	p := &Provider{BaseURL: "https://example.com/v1", Model: "m", APIKey: "secret"}
	out, err := PatchConfig(b, nil, p, "/bin/helper", "/tmp/store", "work")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "secret") {
		t.Fatal("key leaked")
	}
	if !strings.Contains(string(out), "# note") || !strings.Contains(string(out), "model = 'do not edit'") {
		t.Fatal("comments or multiline content lost")
	}
	d, err := ParseConfig(out)
	if err != nil || d["model"] != "m" {
		t.Fatalf("bad model: %v", err)
	}
	out, err = PatchConfig(out, map[string]any{"model": "official"}, nil, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	d, _ = ParseConfig(out)
	ps := d["model_providers"].(map[string]any)
	if ps[providerID] != nil || ps["other"] == nil {
		t.Fatal("provider preservation failure")
	}
}
func TestConfigQuotedRootKeys(t *testing.T) {
	out, err := PatchConfig([]byte("'model_provider' = 'old'\n[desktop]\nmodel = 'keep'\n"), nil, nil, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := ParseConfig(out)
	if d["model_provider"] != "openai" {
		t.Fatal("quoted root key not patched")
	}
}
func TestSwitchSavesRotatedTokensAndRestore(t *testing.T) {
	e, r := fixture(t)
	r.onStop = func() { os.WriteFile(filepath.Join(e.Home, "auth.json"), auth("alice", "rotated"), 0600) }
	if err := e.Use("bob"); err != nil {
		t.Fatal(err)
	}
	r.onStop = nil
	st, _ := e.Store.Load()
	if !bytes.Contains(st.Profiles["alice"].Auth, []byte("rotated")) {
		t.Fatal("rotation not preserved")
	}
	if r.starts != 0 {
		t.Fatal("switch unexpectedly started the APP")
	}
	if err := e.Restore(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(e.Home, "auth.json"))
	if !bytes.Contains(raw, []byte("rotated")) {
		t.Fatal("restored obsolete token")
	}
}
func TestProviderRoundTripAndHelper(t *testing.T) {
	e, _ := fixture(t)
	p := Provider{BaseURL: "https://example.com/v1", Model: "test-model", APIKey: "supersecret", Headers: map[string]string{"X-Test": "x"}}
	if err := e.AddProvider("custom", p); err != nil {
		t.Fatal(err)
	}
	if err := e.Use("custom"); err != nil {
		t.Fatal(err)
	}
	key, err := e.Credential("custom")
	if err != nil || key != p.APIKey {
		t.Fatal("helper failed", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(e.Home, "config.toml"))
	if bytes.Contains(cfg, []byte(p.APIKey)) {
		t.Fatal("key in config")
	}
	if err = e.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Credential("custom"); err == nil {
		t.Fatal("inactive helper accepted")
	}
	st, _ := e.Store.Load()
	if st.Current != "alice" {
		t.Fatal("restore failed")
	}
}
func TestVerificationFailureRollsBack(t *testing.T) {
	e, r := fixture(t)
	before, _ := snapshot(e.Home)
	r.verifyErr = errors.New("bad auth")
	if err := e.Use("bob"); err == nil {
		t.Fatal("expected failure")
	}
	after, _ := snapshot(e.Home)
	if !same(before, after) || e.Store.HasJournal() {
		t.Fatal("rollback failed")
	}
	st, _ := e.Store.Load()
	if st.Current != "alice" {
		t.Fatal("pointer not restored")
	}
}
func TestStopFailureDoesNotWrite(t *testing.T) {
	e, r := fixture(t)
	before, _ := snapshot(e.Home)
	r.stopErr = errors.New("active CLI")
	if e.Use("bob") == nil {
		t.Fatal("expected failure")
	}
	after, _ := snapshot(e.Home)
	if !same(before, after) {
		t.Fatal("changed while active")
	}
}
func TestRecoveryPartialWrite(t *testing.T) {
	e, _ := fixture(t)
	st, _ := e.Store.Load()
	before, _ := snapshot(e.Home)
	after, err := e.plan(before, st.Profiles["bob"], "bob")
	if err != nil {
		t.Fatal(err)
	}
	e.Store.write("pending.enc", Journal{Before: before, After: after, State: st})
	atomicWrite(filepath.Join(e.Home, "config.toml"), after.Config)
	if err = e.Recover(); err != nil {
		t.Fatal(err)
	}
	live, _ := snapshot(e.Home)
	if !same(before, live) {
		t.Fatal("recovery failed")
	}
}
func TestRecoveryRejectsExternalWrites(t *testing.T) {
	e, _ := fixture(t)
	st, _ := e.Store.Load()
	before, _ := snapshot(e.Home)
	after, _ := e.plan(before, st.Profiles["bob"], "bob")
	e.Store.write("pending.enc", Journal{Before: before, After: after, State: st})
	os.WriteFile(filepath.Join(e.Home, "config.toml"), []byte("model = 'external'\n"), 0600)
	if e.Recover() == nil {
		t.Fatal("overwrote external changes")
	}
	b, _ := os.ReadFile(filepath.Join(e.Home, "config.toml"))
	if string(b) != "model = 'external'\n" {
		t.Fatal("external edit lost")
	}
}
func TestVaultEncryptionIntegrityAndLock(t *testing.T) {
	e, _ := fixture(t)
	b, _ := os.ReadFile(filepath.Join(e.Store.Dir, "vault.enc"))
	if bytes.Contains(b, []byte("alice")) || bytes.Contains(b, []byte("refresh_token")) {
		t.Fatal("plaintext vault")
	}
	unlock, err := e.Store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if u, err := e.Store.Lock(); err == nil {
		u()
		t.Fatal("concurrent lock accepted")
	}
	unlock()
	b[len(b)-1] ^= 1
	os.WriteFile(filepath.Join(e.Store.Dir, "vault.enc"), b, 0600)
	if _, err = e.Store.Load(); err == nil {
		t.Fatal("tampering accepted")
	}
}
func TestRejectSymlinkAndUnsafeProviders(t *testing.T) {
	e, _ := fixture(t)
	target := filepath.Join(t.TempDir(), "other")
	os.WriteFile(target, []byte("keep"), 0600)
	os.Remove(filepath.Join(e.Home, "auth.json"))
	os.Symlink(target, filepath.Join(e.Home, "auth.json"))
	if e.Use("bob") == nil {
		t.Fatal("symlink accepted")
	}
	for _, url := range []string{"ftp://example.com/v1", "https://key:secret@example.com/v1", "https://example.com/v1#fragment"} {
		if ValidateProvider(Provider{BaseURL: url, Model: "m", APIKey: "k"}) == nil {
			t.Fatal("unsafe URL accepted")
		}
	}
}
func TestSameWorkspaceDifferentUsers(t *testing.T) {
	a, _ := AuthIdentity(auth("alice", "a"))
	b, _ := AuthIdentity(auth("bob", "b"))
	if a == b {
		t.Fatal("different users conflated")
	}
}

func TestSwitchStopsWithoutLaunching(t *testing.T) {
	e, r := fixture(t)
	r.onStart = func() { t.Fatal("APP must never be launched") }
	if err := e.Use("bob"); err != nil {
		t.Fatal(err)
	}
	st, err := e.Store.Load()
	if err != nil || st.Current != "bob" || e.Store.HasJournal() || r.stops != 1 || r.starts != 0 {
		t.Fatal("switch did not commit while leaving the APP stopped")
	}
	r.verifyErr = errors.New("bad auth")
	if err := e.Use("alice"); err == nil {
		t.Fatal("expected rollback")
	}
	st, _ = e.Store.Load()
	if st.Current != "bob" || r.starts != 0 {
		t.Fatal("rollback restarted APP or lost committed account")
	}
}
func TestCommittedRecoveryKeepsTarget(t *testing.T) {
	e, _ := fixture(t)
	st, _ := e.Store.Load()
	before, _ := snapshot(e.Home)
	after, _ := e.plan(before, st.Profiles["bob"], "bob")
	applySnapshot(e.Home, after)
	st.Current = "bob"
	e.Store.Save(st)
	e.Store.write("pending.enc", Journal{Before: before, After: after, State: st, Committed: true})
	if err := e.Recover(); err != nil {
		t.Fatal(err)
	}
	live, _ := snapshot(e.Home)
	if !same(live, after) {
		t.Fatal("committed state reverted")
	}
}
func TestStoreHomeBinding(t *testing.T) {
	e, _ := fixture(t)
	e.Home = t.TempDir()
	if e.AddProvider("new", Provider{BaseURL: "https://example.com/v1", Model: "m", APIKey: "k"}) == nil {
		t.Fatal("cross-home store accepted")
	}
}
func TestMalformedConfigRejectedBeforeStop(t *testing.T) {
	e, r := fixture(t)
	os.WriteFile(filepath.Join(e.Home, "config.toml"), []byte("not toml"), 0600)
	if e.Use("bob") == nil || r.stops != 0 {
		t.Fatal("invalid config stopped desktop")
	}
}
func TestFreshHelperCanReadWhileWriterLocked(t *testing.T) {
	e, _ := fixture(t)
	if err := e.AddProvider("custom", Provider{BaseURL: "https://example.com/v1", Model: "m", APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Use("custom"); err != nil {
		t.Fatal(err)
	}
	unlock, err := e.Store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	key, err := e.Credential("custom")
	if err != nil || key != "k" {
		t.Fatal("helper blocked by writer lock", err)
	}
}
func TestNativeCodexConfig(t *testing.T) {
	bin := os.Getenv("CODEX_SWITCH_NATIVE_CLI")
	if bin == "" {
		t.Skip("set CODEX_SWITCH_NATIVE_CLI to validate with installed Codex")
	}
	e, _ := fixture(t)
	st, _ := e.Store.Load()
	p := Profile{Kind: "provider", Provider: &Provider{BaseURL: "https://example.com/v1", Model: "sample", Models: []Model{{ID: "sample"}, {ID: "second"}}, APIKey: "isolated-dummy-key"}}
	before, _ := snapshot(e.Home)
	after, err := e.plan(before, p, "sample")
	if err != nil {
		t.Fatal(err)
	}
	if err = applySnapshot(e.Home, after); err != nil {
		t.Fatal(err)
	}
	st.Profiles["sample"] = p
	e.Store.Save(st)
	cmd := exec.Command(bin, "login", "status")
	cmd.Env = scopedEnv(e.Home)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("native Codex rejected isolated dummy provider config: %s", output)
	}
	assertNativeModels(t, bin, e.Home, "sample", "second")
}

func assertNativeModels(t *testing.T, bin, home string, expected ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server := exec.CommandContext(ctx, bin, "app-server")
	server.Env = scopedEnv(home)
	stdin, err := server.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = server.Wait() }()
	encoder := json.NewEncoder(stdin)
	encoder.Encode(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "codex_switch_test", "version": "0.2.0"}}})
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	for scanner.Scan() {
		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &reply) != nil {
			continue
		}
		if reply.ID == 1 {
			if len(reply.Error) > 0 {
				t.Fatalf("initialize failed: %s", reply.Error)
			}
			encoder.Encode(map[string]any{"method": "initialized"})
			encoder.Encode(map[string]any{"id": 2, "method": "model/list", "params": map[string]any{}})
		}
		if reply.ID == 2 {
			if len(reply.Error) > 0 {
				t.Fatalf("model/list failed: %s", reply.Error)
			}
			var result struct {
				Data []struct {
					Model string `json:"model"`
				} `json:"data"`
			}
			if json.Unmarshal(reply.Result, &result) != nil {
				t.Fatal("invalid model/list response")
			}
			found := map[string]bool{}
			for _, model := range result.Data {
				found[model.Model] = true
			}
			for _, id := range expected {
				if !found[id] {
					t.Fatalf("configured model %s absent: %s", id, reply.Result)
				}
			}
			return
		}
	}
	t.Fatal("native app-server did not return model list")
}
