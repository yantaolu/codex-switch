package switcher

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewRecoverWrongHome(t *testing.T) {
	e, runtime := fixture(t)
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
	if err = e.Store.write("pending.enc", Journal{Before: before, After: after, State: st}); err != nil {
		t.Fatal(err)
	}
	e.Home = t.TempDir()
	if err = applySnapshot(e.Home, after); err != nil {
		t.Fatal(err)
	}
	observedBefore, _ := snapshot(e.Home)
	err = e.Recover()
	observedAfter, _ := snapshot(e.Home)
	if err == nil || runtime.stops != 0 || !same(observedBefore, observedAfter) {
		t.Errorf("wrong-home recovery was not rejected: error=%v stop_calls=%d target_changed=%v", err, runtime.stops, !same(observedBefore, observedAfter))
	}
}

func TestReviewProfileAfterRenameOutcome(t *testing.T) {
	e, _ := fixture(t)
	e.Store.fault = func(stage string) error {
		if stage == "atomic:vault.enc:after_rename" {
			return os.ErrPermission
		}
		return nil
	}
	err := e.SetRemark("bob", "新备注")
	st, loadErr := e.Store.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if st.Profiles["bob"].Remark == "新备注" && err != nil && !IsCommitted(err) {
		t.Errorf("profile was saved but reported ordinary failure: %v", err)
	}
}

type reviewRefreshRuntime struct {
	*authRuntime
	cancel context.CancelFunc
}

func (r *reviewRefreshRuntime) Refresh(home string) error {
	if err := atomicWrite(filepath.Join(home, "auth.json"), r.newAuth); err != nil {
		return err
	}
	r.cancel()
	return context.Canceled
}
func TestReviewCancelledRefreshDropsNewCredentials(t *testing.T) {
	e, r, before := expiredTarget(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.loginErr = context.Canceled
	e.Runtime = &reviewRefreshRuntime{authRuntime: r, cancel: cancel}
	e = e.WithContext(ctx)
	err := e.Use("bob")
	st, loadErr := e.Store.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	live, _ := snapshot(e.Home)
	if !errors.Is(err, context.Canceled) || !same(live, before) {
		t.Fatal("fixture did not cancel without switching")
	}
	if !bytes.Equal(st.Profiles["bob"].Auth, r.newAuth) || r.logins != 0 {
		t.Errorf("new credentials were discarded after interrupted refresh: latest_saved=%v login_attempts=%d", bytes.Equal(st.Profiles["bob"].Auth, r.newAuth), r.logins)
	}
}
