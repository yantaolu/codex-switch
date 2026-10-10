package switcher

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSingleVaultMutationsReportWritePhase(t *testing.T) {
	for _, operation := range []string{"add", "remove", "remark", "usage", "provider", "models", "restore"} {
		for _, phase := range []string{"before_rename", "after_rename", "before_directory_sync"} {
			t.Run(operation+"/"+phase, func(t *testing.T) {
				e, _ := fixture(t)
				p := Provider{BaseURL: "https://example.com/v1", Model: "m", APIKey: "fake-key"}
				if err := e.AddProvider("relay", p); err != nil {
					t.Fatal(err)
				}
				var execute func() error
				var applied func(State) bool
				switch operation {
				case "add":
					execute = func() error { return e.AddProvider("new", p) }
					applied = func(st State) bool { _, ok := st.Profiles["new"]; return ok }
				case "remove":
					execute = func() error { return e.Remove("bob") }
					applied = func(st State) bool { _, ok := st.Profiles["bob"]; return !ok }
				case "remark":
					execute = func() error { return e.SetRemark("bob", "new remark") }
					applied = func(st State) bool { return st.Profiles["bob"].Remark == "new remark" }
				case "usage":
					execute = func() error { return e.UpdateUsage("relay", UsageConfig{Type: "off"}) }
					applied = func(st State) bool { return st.Profiles["relay"].Usage != nil }
				case "provider":
					p.BaseURL = "https://next.example/v1"
					execute = func() error { return e.UpdateProvider("relay", p) }
					applied = func(st State) bool { return st.Profiles["relay"].Provider.BaseURL == p.BaseURL }
				case "models":
					execute = func() error { return e.UpdateModels("relay", []Model{{ID: "new"}}, "new") }
					applied = func(st State) bool { return st.Profiles["relay"].Provider.Model == "new" }
				case "restore":
					path := filepath.Join(t.TempDir(), "backup.enc")
					password := "test-password-long"
					if err := e.ExportBackup(path, password); err != nil {
						t.Fatal(err)
					}
					if err := e.Store.Save(State{Version: 2, Home: e.Home, Profiles: map[string]Profile{}}); err != nil {
						t.Fatal(err)
					}
					execute = func() error { return e.ImportBackup(path, password) }
					applied = func(st State) bool { return len(st.Profiles) == 3 && st.Current == "" }
				}
				before, _ := snapshot(e.Home)
				hit := false
				e.Store.fault = func(stage string) error {
					if stage == "atomic:vault.enc:"+phase {
						hit = true
						return os.ErrPermission
					}
					return nil
				}
				err := execute()
				st, loadErr := e.Store.Load()
				after, _ := snapshot(e.Home)
				committed := phase != "before_rename"
				if !hit || err == nil || loadErr != nil || IsCommitted(err) != committed || applied(st) != committed || NeedsRecovery(err) || !same(before, after) {
					t.Fatalf("state/outcome mismatch: %v loaded=%v applied=%v", err, loadErr, applied(st))
				}
				if ResultFor(err).Status == OutcomeUnchanged && committed {
					t.Fatal("frontend result lost commit")
				}
			})
		}
	}
}

func TestReplacedVaultReadbackMismatchIsUnconfirmed(t *testing.T) {
	e, _ := fixture(t)
	path := filepath.Join(e.Store.Dir, "vault.enc")
	before, _ := readSafe(path)
	e.Store.fault = func(stage string) error {
		if stage == "atomic:vault.enc:after_rename" {
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			return os.ErrPermission
		}
		return nil
	}
	err := e.SetRemark("bob", "new")
	if !IsUnconfirmed(err) || IsCommitted(err) || ResultFor(err).Status != OutcomeUnconfirmed {
		t.Fatalf("unconfirmed write falsely reported: %v", err)
	}
	got, _ := readSafe(path)
	if !bytes.Equal(got, before) {
		t.Fatal("mismatch fixture failed")
	}
}

func TestCancelAfterSnapshotOrVerificationRollsBack(t *testing.T) {
	for _, phase := range []string{"snapshot_applied", "verified"} {
		t.Run(phase, func(t *testing.T) {
			e, _ := fixture(t)
			before, _ := snapshot(e.Home)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e.checkpoint = func(stage string) error {
				if stage == phase {
					cancel()
				}
				return nil
			}
			err := e.WithContext(ctx).Use("bob")
			after, _ := snapshot(e.Home)
			st, _ := e.Store.Load()
			if !errors.Is(err, context.Canceled) || IsCommitted(err) || NeedsRecovery(err) || e.Store.HasJournal() || st.Current != "alice" || !same(before, after) {
				t.Fatalf("cancel failed to rollback: %v", err)
			}
		})
	}
}
