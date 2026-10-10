//go:build darwin

package switcher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/zalando/go-keyring"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommittedAuditWarningNeverMeansRollback(t *testing.T) {
	e, _ := fixture(t)
	e.Store.fault = func(stage string) error {
		if stage == "audit:success" {
			return os.ErrPermission
		}
		return nil
	}
	err := e.Use("bob")
	if !IsCommitted(err) || NeedsRecovery(err) {
		t.Fatalf("wrong outcome: %v", err)
	}
	st, _ := e.Store.Load()
	if st.Current != "bob" || e.Store.HasJournal() {
		t.Fatal("committed account was rolled back")
	}
}
func TestSwitchPersistenceFaultsAndIdempotentRecovery(t *testing.T) {
	for _, point := range []string{"before:config.toml", "after:config.toml", "before:auth.json", "after:auth.json", "snapshot_applied", "verified", "state_saved", "journal_prepared", "journal_committed", "journal_cleared"} {
		t.Run(point, func(t *testing.T) {
			e, _ := fixture(t)
			before, _ := snapshot(e.Home)
			hit := false
			e.checkpoint = func(stage string) error {
				if stage == point && !hit {
					hit = true
					return os.ErrPermission
				}
				return nil
			}
			err := e.Use("bob")
			if err == nil || !hit {
				t.Fatal("fault not exercised")
			}
			e.checkpoint = nil
			if e.Store.HasJournal() {
				if recoverErr := e.Recover(); recoverErr != nil {
					t.Fatal(recoverErr)
				}
			}
			st, _ := e.Store.Load()
			after, _ := snapshot(e.Home)
			committed := point == "journal_committed" || point == "journal_cleared"
			if committed {
				if !IsCommitted(err) || st.Current != "bob" {
					t.Fatalf("lost commit: %v %s", err, st.Current)
				}
			} else if st.Current != "alice" || !same(before, after) {
				t.Fatal("mixed account after recovery")
			}
			if e.Store.HasJournal() {
				t.Fatal("journal retained")
			}
			_ = e.Recover()
			again, _ := snapshot(e.Home)
			if !same(after, again) {
				t.Fatal("repeated recovery changed files")
			}
		})
	}
	for _, point := range []string{"write:vault.enc", "write:pending.enc", "clear:pending.enc", "audit:started"} {
		t.Run(point, func(t *testing.T) {
			e, _ := fixture(t)
			before, _ := snapshot(e.Home)
			hit := false
			e.Store.fault = func(stage string) error {
				if stage == point && !hit {
					hit = true
					return os.ErrPermission
				}
				return nil
			}
			err := e.Use("bob")
			if err == nil || !hit {
				t.Fatal("storage failure ignored")
			}
			e.Store.fault = nil
			if e.Store.HasJournal() {
				if er := e.Recover(); er != nil {
					t.Fatal(er)
				}
			}
			st, _ := e.Store.Load()
			after, _ := snapshot(e.Home)
			if point == "clear:pending.enc" {
				if st.Current != "bob" || !IsCommitted(err) {
					t.Fatal("clear error lost committed outcome")
				}
			} else if st.Current != "alice" || !same(before, after) {
				t.Fatal("write failure mutated account")
			}
		})
	}
}

// Run the real transaction in a child and stop it without executing deferred
// rollback. The test key and home never refer to the user's Keychain or Codex.
func TestCrashChild(t *testing.T) {
	if os.Getenv("CXS_CRASH_CHILD") != "1" {
		return
	}
	root := os.Getenv("CXS_CRASH_ROOT")
	e := Engine{Store: &Store{Dir: filepath.Join(root, "store"), key: bytes.Repeat([]byte{3}, 32)}, Home: filepath.Join(root, "codex"), Helper: "/helper", Runtime: &fakeRuntime{}}
	point := os.Getenv("CXS_CRASH_POINT")
	e.checkpoint = func(stage string) error {
		if stage == point {
			os.Exit(91)
		}
		return nil
	}
	_ = e.Use("bob")
	os.Exit(92)
}
func TestAbruptExitAtEveryTransactionBoundary(t *testing.T) {
	for _, point := range []string{"journal_prepared", "after:config.toml", "after:auth.json", "state_saved", "journal_committed", "journal_cleared"} {
		t.Run(point, func(t *testing.T) {
			e, _ := fixture(t)
			before, _ := snapshot(e.Home)
			bin, _ := os.Executable()
			cmd := exec.Command(bin, "-test.run=^TestCrashChild$")
			cmd.Env = append(os.Environ(), "CXS_CRASH_CHILD=1", "CXS_CRASH_ROOT="+filepath.Dir(e.Home), "CXS_CRASH_POINT="+point)
			err := cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 91 {
				t.Fatalf("crash not triggered: %v", err)
			}
			if e.Store.HasJournal() {
				if err = e.Recover(); err != nil {
					t.Fatal(err)
				}
			}
			st, _ := e.Store.Load()
			after, _ := snapshot(e.Home)
			committed := point == "journal_committed" || point == "journal_cleared"
			if committed {
				if st.Current != "bob" {
					t.Fatal("committed crash reverted")
				}
			} else if st.Current != "alice" || !same(before, after) {
				t.Fatal("precommit crash left mixed state")
			}
		})
	}
}

type guardedRuntime struct {
	*fakeRuntime
	guardErr error
	checks   int
}

func (r *guardedRuntime) EnsureStopped() error { r.checks++; return r.guardErr }
func TestClientLaunchedDuringAuthenticationPreventsWrite(t *testing.T) {
	e, r := fixture(t)
	guard := &guardedRuntime{fakeRuntime: r, guardErr: &ProcessConflictError{Details: "new client"}}
	e.Runtime = guard
	before, _ := snapshot(e.Home)
	err := e.Use("bob")
	var conflict *ProcessConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("missing process conflict: %v", err)
	}
	after, _ := snapshot(e.Home)
	if !same(before, after) {
		t.Fatal("wrote credentials with running client")
	}
	if NeedsRecovery(err) || e.Store.HasJournal() {
		t.Fatal("unchanged snapshot should be safe to clear without writing credentials")
	}
}
func TestPortableBackupWrongPasswordTamperingAndRestore(t *testing.T) {
	e, _ := fixture(t)
	path := filepath.Join(t.TempDir(), "accounts.cxsb")
	password := "test-password-long-enough"
	if err := e.ExportBackup(path, password); err != nil {
		t.Fatal(err)
	}
	raw, _ := readSafe(path)
	if bytes.Contains(raw, []byte("bob-token")) {
		t.Fatal("plaintext backup")
	}
	if _, err := DecodeBackup(raw, "wrong-password-long"); err == nil {
		t.Fatal("wrong password accepted")
	}
	damaged := append([]byte(nil), raw...)
	damaged[len(damaged)-1] ^= 1
	if _, err := DecodeBackup(damaged, password); err == nil {
		t.Fatal("corruption accepted")
	}
	other, _ := fixture(t)
	st, _ := other.Store.Load()
	st.Profiles = map[string]Profile{}
	st.Current = ""
	other.Store.Save(st)
	before, _ := snapshot(other.Home)
	if err := other.ImportBackup(path, password); err != nil {
		t.Fatal(err)
	}
	after, _ := snapshot(other.Home)
	restored, _ := other.Store.Load()
	if !same(before, after) || restored.Current != "" || len(restored.Profiles) != 2 || restored.Home != other.Home {
		t.Fatal("restore activated or lost accounts")
	}
	if err := other.ImportBackup(path, password); err == nil {
		t.Fatal("nonempty store overwritten")
	}
	if err := e.ExportBackup(path, password); err == nil {
		t.Fatal("old backup overwritten")
	}
}
func TestSchemaMigrationPreservesLegacyAliasesAndEncryptedBackup(t *testing.T) {
	e, _ := fixture(t)
	st, _ := e.Store.Load()
	st.Version = 1
	if err := e.Store.write("vault.enc", st); err != nil {
		t.Fatal(err)
	}
	original, _ := readSafe(filepath.Join(e.Store.Dir, "vault.enc"))
	loaded, err := e.Store.Load()
	if err != nil || loaded.Version != 2 {
		t.Fatal("migration failed")
	}
	if err = e.Store.Save(loaded); err != nil {
		t.Fatal(err)
	}
	backup, _ := readSafe(filepath.Join(e.Store.Dir, "vault.v1.enc"))
	if !bytes.Equal(original, backup) {
		t.Fatal("migration backup not exact")
	}
	if loaded.Profiles["alice"].Kind != "chatgpt" {
		t.Fatal("legacy ID lost")
	}
}
func TestUnicodeRemarksAndInactiveProviderEdit(t *testing.T) {
	e, r := fixture(t)
	p := Provider{BaseURL: "https://example.com/v1", Model: "m", APIKey: "test"}
	if err := e.AddProvider("备用中转", p); err != nil {
		t.Fatal(err)
	}
	id := ProfileID("备用中转")
	if ValidateName(id) != nil {
		t.Fatal("unsafe internal ID")
	}
	if err := e.SetRemark(id, "工作账户"); err != nil {
		t.Fatal(err)
	}
	p.APIKey = "changed"
	before, _ := snapshot(e.Home)
	if err := e.UpdateProvider(id, p); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Store.Load()
	after, _ := snapshot(e.Home)
	if st.Profiles[id].Remark != "工作账户" || st.Profiles[id].Provider.APIKey != "changed" || !same(before, after) || r.stops != 0 {
		t.Fatal("inactive edit activated account")
	}
}
func TestSafeFileDescriptorsAndLockSymlink(t *testing.T) {
	e, _ := fixture(t)
	target := filepath.Join(t.TempDir(), "target")
	os.WriteFile(target, []byte("unchanged"), 0600)
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(target, link)
	if _, err := readSafe(link); err == nil {
		t.Fatal("read followed symlink")
	}
	if err := atomicWrite(link, []byte("bad")); err == nil {
		t.Fatal("write followed symlink")
	}
	os.Remove(filepath.Join(e.Store.Dir, "lock"))
	os.Symlink(target, filepath.Join(e.Store.Dir, "lock"))
	if _, err := e.Store.Lock(); err == nil {
		t.Fatal("lock followed symlink")
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "unchanged" {
		t.Fatal("target mutated")
	}
}
func TestAuditRotationAndCatalogReferences(t *testing.T) {
	e, _ := fixture(t)
	os.WriteFile(filepath.Join(e.Store.Dir, "switch.log"), bytes.Repeat([]byte("x"), auditLimit+1), 0600)
	if err := e.Store.appendAudit(AuditEvent{ID: "new", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Store.Dir, "switch.log.1")); err != nil {
		t.Fatal("log not rotated")
	}
	p := Provider{BaseURL: "https://example.com/v1", Model: "m", APIKey: "test"}
	path, err := e.catalog(p)
	if err != nil {
		t.Fatal(err)
	}
	old := p
	old.Model = "old"
	oldPath, _ := e.catalog(old)
	st, _ := e.Store.Load()
	st.Profiles["relay"] = Profile{Kind: "provider", Provider: &p}
	if err = e.pruneCatalogs(st); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("referenced catalog removed")
	}
	if _, err = os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("orphan catalog retained")
	}
}
func writeSessionMetadata(t *testing.T, e Engine, i int, provider string) string {
	t.Helper()
	id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
	path := filepath.Join(e.Home, "sessions", fmt.Sprintf("%04d.jsonl", i))
	os.MkdirAll(filepath.Dir(path), 0700)
	raw := fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"model_provider\":%q,\"source\":\"cli\"}}\n", id, provider)
	os.WriteFile(path, []byte(raw), 0600)
	stamp := time.Unix(int64(i), 0)
	os.Chtimes(path, stamp, stamp)
	return id
}
func TestSessionCandidatesFilterBeforePagination(t *testing.T) {
	e, _ := fixture(t)
	for i := 0; i < 60; i++ {
		provider := providerID
		if i%2 == 0 {
			provider = "openai"
		}
		writeSessionMetadata(t, e, i, provider)
	}
	page, err := e.SessionCandidatesPage(context.Background(), 0, 20)
	if err != nil || page.Total != 30 || len(page.Items) != 20 {
		t.Fatalf("bad page: %+v %v", page, err)
	}
	next, err := e.SessionCandidatesPage(context.Background(), 20, 20)
	if err != nil || len(next.Items) != 10 {
		t.Fatal("bad second page")
	}
	for _, s := range append(page.Items, next.Items...) {
		if s.Provider == "openai" {
			t.Fatal("same provider displayed")
		}
	}
	id := writeSessionMetadata(t, e, 100, "openai")
	if _, err := e.ContinueSession(context.Background(), id); err == nil {
		t.Fatal("same provider forked")
	}
}
func TestForkDisconnectMalformedAndExplicitRejection(t *testing.T) {
	for _, test := range []struct {
		name, response string
		unknown        bool
	}{{"disconnect", "exit 0", true}, {"malformed", "printf '%s\\n' '{\"id\":2,\"result\":{}}'", true}, {"reject", "printf '%s\\n' '{\"id\":2,\"error\":{\"code\":-1,\"message\":\"secret\"}}'", false}} {
		t.Run(test.name, func(t *testing.T) {
			e, _ := fixture(t)
			id := writeSessionMetadata(t, e, 1, providerID)
			bin := filepath.Join(t.TempDir(), "codex")
			script := "#!/bin/sh\nwhile IFS= read -r line; do\ncase \"$line\" in\n*'\"method\":\"initialize\"'*) printf '%s\\n' '{\"id\":1,\"result\":{}}' ;;\n*'\"method\":\"thread/fork\"'*) " + test.response + " ;;\nesac\ndone\n"
			os.WriteFile(bin, []byte(script), 0700)
			e.Runtime = MacRuntime{Codex: bin, Home: e.Home}
			_, err := e.ContinueSession(context.Background(), id)
			var unknown *SessionCopyUnconfirmedError
			if err == nil || errors.As(err, &unknown) != test.unknown || strings.Contains(err.Error(), "secret") {
				t.Fatalf("wrong fork outcome: %v", err)
			}
		})
	}
}
func TestHomeParserAndCrossHomeWrapperSafety(t *testing.T) {
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, 2)
	raw = append(raw, []byte("/bin/codex\x00\x00codex\x00app-server\x00SECRET=hidden\x00CODEX_HOME=/tmp/isolated\x00\x00")...)
	home, err := homeFromProcArgs(raw)
	if err != nil || home != "/tmp/isolated" {
		t.Fatalf("home: %s %v", home, err)
	}
	all := processFixture()
	p := all[61]
	p.Excluded = true
	all[61] = p
	targets, err := forceTargets(all, "/Applications/ChatGPT.app", 12, 501)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{60, 61, 62} {
		if _, ok := targets[id]; ok {
			t.Fatal("unrelated home or shared wrapper selected")
		}
	}
}
func TestStableHelperSurvivesCellarUpgradeCleanup(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "homebrew")
	old := filepath.Join(prefix, "Cellar/codex-switch/0.7/bin/codex-switch")
	next := filepath.Join(prefix, "Cellar/codex-switch/0.8/bin/codex-switch")
	opt := filepath.Join(prefix, "opt/codex-switch")
	for _, path := range []string{old, next} {
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte("#!/bin/sh\nprintf helper-ok\n"), 0700)
	}
	os.MkdirAll(filepath.Dir(opt), 0700)
	os.Symlink(filepath.Dir(filepath.Dir(old)), opt)
	helper, err := stableHelperFor(old, old)
	if err != nil || canonicalHome(filepath.Dir(helper)) != canonicalHome(filepath.Join(opt, "bin")) || filepath.Base(helper) != "codex-switch" {
		t.Fatalf("unstable helper: %s %v", helper, err)
	}
	os.Remove(opt)
	os.Symlink(filepath.Dir(filepath.Dir(next)), opt)
	os.RemoveAll(filepath.Join(prefix, "Cellar/codex-switch/0.7"))
	result, err := exec.Command(helper).Output()
	if err != nil || string(result) != "helper-ok" {
		t.Fatal("helper failed after cleanup")
	}
}

func TestControlledProcessHelper(t *testing.T) {
	if os.Getenv("CXS_PROCESS_HELPER") != "1" {
		return
	}
	for {
		time.Sleep(time.Second)
	}
}
func TestControlledForceStopKeepsOtherHome(t *testing.T) {
	if os.Getenv("CXS_PROCESS_ACCEPTANCE") != "1" {
		t.Skip("explicit isolated process acceptance")
	}
	root := t.TempDir()
	home := filepath.Join(root, "target")
	otherHome := filepath.Join(root, "other")
	os.Mkdir(home, 0700)
	os.Mkdir(otherHome, 0700)
	executable, _ := os.Executable()
	bin := filepath.Join(root, "codex")
	if err := os.Link(executable, bin); err != nil {
		t.Fatal(err)
	}
	start := func(home string) *exec.Cmd {
		cmd := exec.Command(bin, "-test.run=^TestControlledProcessHelper$")
		cmd.Env = append(scopedEnv(home), "CXS_PROCESS_HELPER=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	target := start(home)
	other := start(otherHome)
	defer target.Process.Kill()
	defer func() { other.Process.Kill(); other.Wait() }()
	runtime := MacRuntime{Home: home}
	all, err := runtime.scopedProcesses()
	if err != nil {
		t.Fatal(err)
	}
	selected, err := forceTargets(all, "", os.Getpid(), os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[target.Process.Pid].PID != target.Process.Pid {
		t.Fatalf("refuse uncontrolled process scope: %d targets", len(selected))
	}
	done := make(chan error, 1)
	go func() { done <- target.Wait() }()
	if _, err := runtime.ForceStop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("controlled target remained")
	}
	if err := other.Process.Signal(os.Signal(syscall.Signal(0))); err != nil {
		t.Fatal("other home process was stopped")
	}
}

func TestHomebrewCredentialAcceptance(t *testing.T) {
	phase := os.Getenv("CXS_BREW_PHASE")
	if phase == "" {
		t.Skip("explicit Homebrew/Keychain acceptance")
	}
	root := os.Getenv("CXS_BREW_ROOT")
	if root == "" || !filepath.IsAbs(root) {
		t.Fatal("isolated root required")
	}
	dir := filepath.Join(root, "store")
	absolute, _ := filepath.Abs(dir)
	sum := sha256.Sum256([]byte(absolute))
	account := hex.EncodeToString(sum[:])
	if phase == "cleanup" {
		if err := keyring.Delete("codex-switch", account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			t.Fatal("test Keychain entry cleanup failed")
		}
		return
	}
	store, err := OpenStore(dir, phase == "prepare")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "codex")
	os.MkdirAll(home, 0700)
	bin := os.Getenv("CXS_BREW_BIN")
	helper, err := stableHelperFor(bin, bin)
	if err != nil {
		t.Fatal(err)
	}
	if phase == "prepare" {
		p := Provider{BaseURL: "https://example.invalid/v1", Model: "m", APIKey: "isolated-acceptance-token"}
		st := State{Version: 2, Home: home, Current: "relay", Profiles: map[string]Profile{"relay": {Kind: "provider", Provider: &p}}}
		e := Engine{Store: store, Home: home, Helper: helper, Runtime: &fakeRuntime{}}
		planned, err := e.plan(Snapshot{}, st.Profiles["relay"], "relay")
		if err != nil {
			t.Fatal(err)
		}
		if err = applySnapshot(home, planned); err != nil {
			t.Fatal(err)
		}
		if err = store.Save(st); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := readSafe(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	provider := obj(obj(config["model_providers"])[providerID])
	auth := obj(provider["auth"])
	saved, _ := auth["command"].(string)
	if saved != helper || strings.Contains(saved, "/Cellar/") {
		t.Fatal("helper not recorded at stable opt prefix")
	}
	cmd := exec.Command(saved, "--store", dir, "--home", home, "credential", "relay")
	cmd.Env = append(scopedEnv(home), "PATH=")
	token, err := cmd.Output()
	if err != nil || string(token) != "isolated-acceptance-token" {
		t.Fatal("credential helper failed after installation or upgrade")
	}
	st, err := store.Load()
	if err != nil || st.Current != "relay" {
		t.Fatal("vault lost during upgrade")
	}
}

func TestAtomicWriteFaultsBeforeAndAfterRename(t *testing.T) {
	for _, name := range []string{"config.toml", "auth.json"} {
		for _, phase := range []string{"before_write", "before_sync", "before_rename", "after_rename", "before_directory_sync"} {
			t.Run(name+"/"+phase, func(t *testing.T) {
				e, _ := fixture(t)
				before, _ := snapshot(e.Home)
				hit := false
				e.checkpoint = func(stage string) error {
					if stage == name+":"+phase && !hit {
						hit = true
						return os.ErrPermission
					}
					return nil
				}
				err := e.Use("bob")
				if err == nil || !hit {
					t.Fatal("atomic failure ignored")
				}
				e.checkpoint = nil
				if e.Store.HasJournal() {
					if err = e.Recover(); err != nil {
						t.Fatal(err)
					}
				}
				st, _ := e.Store.Load()
				after, _ := snapshot(e.Home)
				if st.Current != "alice" || !same(before, after) {
					t.Fatal("rename failure left a mixed account")
				}
			})
		}
	}
}
func TestCommitMarkerFaultBeforeAndAfterRename(t *testing.T) {
	for _, phase := range []string{"before_write", "before_sync", "before_rename", "after_rename", "before_directory_sync"} {
		t.Run(phase, func(t *testing.T) {
			e, _ := fixture(t)
			before, _ := snapshot(e.Home)
			writes := 0
			hit := false
			e.Store.fault = func(stage string) error {
				if stage == "write:pending.enc" {
					writes++
				}
				if writes == 2 && stage == "atomic:pending.enc:"+phase && !hit {
					hit = true
					return os.ErrPermission
				}
				return nil
			}
			err := e.Use("bob")
			if !hit || !NeedsRecovery(err) {
				t.Fatalf("missing uncertain commit: %v", err)
			}
			e.Store.fault = nil
			if err = e.Recover(); err != nil {
				t.Fatal(err)
			}
			st, _ := e.Store.Load()
			after, _ := snapshot(e.Home)
			committed := phase == "after_rename" || phase == "before_directory_sync"
			if committed {
				if st.Current != "bob" {
					t.Fatal("renamed commit marker discarded")
				}
			} else if st.Current != "alice" || !same(before, after) {
				t.Fatal("uncommitted transaction not restored")
			}
		})
	}
}

func TestDiagnosticsIsReadOnlyAndFindsLegacyHelper(t *testing.T) {
	e, _ := fixture(t)
	p := Provider{BaseURL: "https://example.com/v1", Model: "m", APIKey: "test"}
	if err := e.AddProvider("relay", p); err != nil {
		t.Fatal(err)
	}
	if err := e.Use("relay"); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Store.Load()
	raw, _ := readSafe(filepath.Join(e.Home, "config.toml"))
	raw = bytes.ReplaceAll(raw, []byte(e.Helper), []byte("/missing/Cellar/codex-switch/0.7/bin/codex-switch"))
	atomicWrite(filepath.Join(e.Home, "config.toml"), raw)
	before, _ := snapshot(e.Home)
	vault, _ := readSafe(filepath.Join(e.Store.Dir, "vault.enc"))
	diagnostic, err := e.Inspect()
	if err != nil || !diagnostic.AuthMatches || !diagnostic.HelperMigration || diagnostic.Current != st.Current {
		t.Fatalf("incorrect diagnosis: %+v %v", diagnostic, err)
	}
	after, _ := snapshot(e.Home)
	next, _ := readSafe(filepath.Join(e.Store.Dir, "vault.enc"))
	if !same(before, after) || !bytes.Equal(vault, next) {
		t.Fatal("diagnosis changed credentials")
	}
}
