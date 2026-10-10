package switcher

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func prepareRecovery(t *testing.T, e Engine, committed bool) Journal {
	t.Helper()
	st, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	before, err := snapshot(e.Home)
	if err != nil {
		t.Fatal(err)
	}
	after, err := e.plan(before, st.Profiles["bob"], "bob")
	if err != nil {
		t.Fatal(err)
	}
	if err = applySnapshot(e.Home, after); err != nil {
		t.Fatal(err)
	}
	j := Journal{Before: before, After: after, State: st, Committed: committed}
	if committed {
		st.Current = "bob"
		if err = e.Store.Save(st); err != nil {
			t.Fatal(err)
		}
	}
	return j
}

func TestRecoveryBindingsBeforeAnyRuntimeOrSnapshotMutation(t *testing.T) {
	for _, committed := range []bool{false, true} {
		for _, mode := range []string{"wrong-target", "wrong-journal", "unbound", "legacy-journal", "legacy-vault", "cancelled"} {
			t.Run(mode+map[bool]string{false: "/rollback", true: "/committed"}[committed], func(t *testing.T) {
				e, r := fixture(t)
				j := prepareRecovery(t, e, committed)
				st, _ := e.Store.Load()
				allowed := mode == "legacy-journal" || mode == "legacy-vault"
				switch mode {
				case "wrong-target":
					e.Home = t.TempDir()
					if err := applySnapshot(e.Home, j.After); err != nil {
						t.Fatal(err)
					}
				case "wrong-journal":
					j.State.Home = t.TempDir()
				case "unbound":
					st.Home = ""
					j.State.Home = ""
				case "legacy-journal":
					j.State.Home = ""
				case "legacy-vault":
					st.Home = ""
				case "cancelled":
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					e = e.WithContext(ctx)
				}
				if err := e.Store.Save(st); err != nil {
					t.Fatal(err)
				}
				if err := e.Store.write("pending.enc", j); err != nil {
					t.Fatal(err)
				}
				before, _ := snapshot(e.Home)
				vault, _ := readSafe(filepath.Join(e.Store.Dir, "vault.enc"))
				journal, _ := readSafe(filepath.Join(e.Store.Dir, "pending.enc"))
				err := e.Recover()
				if allowed {
					if err != nil || e.Store.HasJournal() {
						t.Fatalf("bound legacy recovery failed: %v", err)
					}
					got, _ := e.Store.Load()
					if got.Home != e.Home {
						t.Fatal("legacy binding not persisted")
					}
					return
				}
				after, _ := snapshot(e.Home)
				vaultAfter, _ := readSafe(filepath.Join(e.Store.Dir, "vault.enc"))
				journalAfter, _ := readSafe(filepath.Join(e.Store.Dir, "pending.enc"))
				if err == nil || !NeedsRecovery(err) || r.stops != 0 || !same(before, after) || !bytes.Equal(vault, vaultAfter) || !bytes.Equal(journal, journalAfter) {
					t.Fatalf("unsafe recovery: %v stops=%d", err, r.stops)
				}
			})
		}
	}
}

type interruptedRefresh struct {
	*authRuntime
	err    error
	cancel context.CancelFunc
}

func (r *interruptedRefresh) Refresh(home string) error {
	r.refreshes++
	if err := atomicWrite(filepath.Join(home, "auth.json"), r.newAuth); err != nil {
		return err
	}
	if r.cancel != nil {
		r.cancel()
	}
	return r.err
}

func TestInterruptedRefreshValidatesAndRetainsWrittenCredentials(t *testing.T) {
	for _, mode := range []string{"rpc-disconnect", "deadline", "wrong-identity", "save-before-rename", "save-after-rename"} {
		t.Run(mode, func(t *testing.T) {
			e, base, before := expiredTarget(t)
			r := &interruptedRefresh{authRuntime: base, err: errors.New("RPC disconnected")}
			e.Runtime = r
			if mode == "deadline" {
				r.err = context.DeadlineExceeded
			}
			if mode == "wrong-identity" {
				r.newAuth = expiringAuth("other", "wrong", time.Now().Add(time.Hour))
			}
			if strings.HasPrefix(mode, "save-") {
				phase := "before_rename"
				if mode == "save-after-rename" {
					phase = "after_rename"
				}
				e.Store.fault = func(stage string) error {
					if stage == "atomic:vault.enc:"+phase {
						return os.ErrPermission
					}
					return nil
				}
			}
			err := e.Use("bob")
			st, _ := e.Store.Load()
			live, _ := snapshot(e.Home)
			if r.logins != 0 {
				t.Fatal("unexpected browser fallback after written refresh")
			}
			if mode == "rpc-disconnect" {
				if err != nil || st.Current != "bob" || !bytes.Equal(st.Profiles["bob"].Auth, r.newAuth) {
					t.Fatalf("fresh credentials ignored: %v", err)
				}
				return
			}
			if err == nil || st.Current != "alice" || !same(live, before) || e.Store.HasJournal() || IsCommitted(err) {
				t.Fatalf("failed authentication switched: %v", err)
			}
			switch mode {
			case "deadline":
				if !errors.Is(err, context.DeadlineExceeded) || !bytes.Equal(st.Profiles["bob"].Auth, r.newAuth) {
					t.Fatal("deadline discarded credentials")
				}
			case "wrong-identity":
				if bytes.Equal(st.Profiles["bob"].Auth, r.newAuth) {
					t.Fatal("wrong account persisted")
				}
			default:
				paths, _ := filepath.Glob(filepath.Join(e.Store.Dir, "reauth-*"))
				if len(paths) != 1 {
					t.Fatal("save error removed recovery credentials")
				}
				got, _ := readSafe(filepath.Join(paths[0], "auth.json"))
				if !bytes.Equal(got, r.newAuth) {
					t.Fatal("retained credentials incomplete")
				}
				if mode == "save-after-rename" && (!IsUnconfirmed(err) || !bytes.Equal(st.Profiles["bob"].Auth, r.newAuth)) {
					t.Fatal("credential save mistaken for switched account")
				}
			}
		})
	}
}

func TestCancelledCurrentRefreshCanRetryWithLatestSavedCredential(t *testing.T) {
	e, base, _ := expiredTarget(t)
	old := expiringAuth("alice", "old", time.Now().Add(-time.Hour))
	if err := atomicWrite(filepath.Join(e.Home, "auth.json"), old); err != nil {
		t.Fatal(err)
	}
	base.newAuth = expiringAuth("alice", "rotated", time.Now().Add(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Runtime = &interruptedRefresh{authRuntime: base, err: context.Canceled, cancel: cancel}
	if err := e.WithContext(ctx).Use("alice"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel missing: %v", err)
	}
	live, _ := readSafe(filepath.Join(e.Home, "auth.json"))
	if !bytes.Equal(live, old) {
		t.Fatal("cancel modified live credential")
	}
	e.Runtime = base
	if err := e.Use("alice"); err != nil {
		t.Fatal(err)
	}
	live, _ = readSafe(filepath.Join(e.Home, "auth.json"))
	if base.refreshes != 1 || base.logins != 0 || !bytes.Equal(live, base.newAuth) {
		t.Fatal("retry overwrote rotated credential with old cache")
	}
}

func TestImportFileBoundedRegularDescriptor(t *testing.T) {
	for _, mode := range []string{"valid", "limit", "oversized", "fifo", "directory", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input")
			switch mode {
			case "valid":
				os.WriteFile(path, []byte("model='m'"), 0600)
			case "limit":
				os.WriteFile(path, bytes.Repeat([]byte("a"), 1<<20), 0600)
			case "oversized":
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.Truncate(1 << 30); err != nil {
					t.Fatal(err)
				}
				f.Close()
			case "fifo":
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				os.Mkdir(path, 0700)
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				os.WriteFile(target, []byte("secret"), 0600)
				os.Symlink(target, path)
			}
			done := make(chan error, 1)
			go func() {
				b, err := ReadImportFile(path)
				if err == nil && len(b) > 1<<20 {
					err = errors.New("unbounded input")
				}
				done <- err
			}()
			select {
			case err := <-done:
				if (mode == "valid" || mode == "limit") != (err == nil) {
					t.Fatalf("bad file result: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("special input blocked")
			}
		})
	}
}

func (r *interruptedRefresh) Login(home string, _ bool) error {
	r.logins++
	if err := atomicWrite(filepath.Join(home, "auth.json"), r.newAuth); err != nil {
		return err
	}
	if r.cancel != nil {
		r.cancel()
	}
	return r.err
}

func TestReloginInterruptedOrPostRenameSaveProtectsCredential(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, mode := range []string{"cancel-after-login", "save-after-rename"} {
			t.Run(mode+map[bool]string{false: "/inactive", true: "/active"}[active], func(t *testing.T) {
				e, base, before := expiredTarget(t)
				name := "bob"
				if active {
					name = "alice"
					base.newAuth = expiringAuth("alice", "new", time.Now().Add(time.Hour))
				}
				r := &interruptedRefresh{authRuntime: base}
				e.Runtime = r
				if mode == "cancel-after-login" {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					e = e.WithContext(ctx)
					r.cancel = cancel
					r.err = context.Canceled
				}
				if mode == "save-after-rename" {
					e.Store.fault = func(stage string) error {
						if stage == "atomic:vault.enc:after_rename" {
							return os.ErrPermission
						}
						return nil
					}
				}
				err := e.Relogin(name)
				st, _ := e.Store.Load()
				after, _ := snapshot(e.Home)
				if !bytes.Equal(st.Profiles[name].Auth, r.newAuth) || !same(before, after) || st.Current != "alice" || e.Store.HasJournal() {
					t.Fatal("relogin discarded credentials or activated account")
				}
				if mode == "cancel-after-login" && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel lost: %v", err)
				}
				if mode == "save-after-rename" && (active && !IsUnconfirmed(err) || !active && !IsCommitted(err)) {
					t.Fatalf("save outcome incorrect: %v", err)
				}
			})
		}
	}
}
