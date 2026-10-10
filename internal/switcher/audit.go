package switcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type AuditEvent struct {
	Time          string `json:"time"`
	Operation     string `json:"operation"`
	ID            string `json:"id"`
	From          string `json:"from,omitempty"`
	To            string `json:"to,omitempty"`
	Status        string `json:"status"`
	Stage         string `json:"stage,omitempty"`
	Force         bool   `json:"force,omitempty"`
	Pending       bool   `json:"pending,omitempty"`
	Current       string `json:"current,omitempty"`
	RolledBack    bool   `json:"rolled_back,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
	SourceSession string `json:"source_session,omitempty"`
	TargetSession string `json:"target_session,omitempty"`
}

func auditID() string { var b [12]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func (s *Store) appendAudit(event AuditEvent) error {
	if s.fault != nil {
		if err := s.fault("audit:" + event.Status); err != nil {
			return err
		}
	}
	if err := s.rotateAudit(); err != nil {
		return err
	}
	event.Time = time.Now().UTC().Format(time.RFC3339Nano)
	path := filepath.Join(s.Dir, "switch.log")
	if st, e := os.Lstat(path); e == nil && (!st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0) {
		return errors.New("日志文件不是普通文件")
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return errors.New("无法写入切换日志")
	}
	defer f.Close()
	if e = f.Chmod(0600); e != nil {
		return errors.New("无法设置日志权限")
	}
	if e = json.NewEncoder(f).Encode(event); e != nil {
		return errors.New("无法保存切换日志")
	}
	return f.Sync()
}

// Audit records contain identifiers and stages, never raw errors or config/auth.
func (e Engine) finishAudit(event AuditEvent, stage string, failure error) error {
	event.Status = "success"
	if failure != nil {
		event.Status = "failed"
		event.ErrorCode = stage + "_failed"
		if IsCommitted(failure) {
			event.Status = "committed_warning"
			event.ErrorCode = "audit_warning"
		} else if NeedsRecovery(failure) {
			event.Status = "pending"
		} else if IsUnconfirmed(failure) {
			event.Status = "unconfirmed"
		}
		var conflict *ProcessConflictError
		if errors.As(failure, &conflict) {
			event.ErrorCode = "process_conflict"
		}
		if errors.Is(failure, context.Canceled) && !IsCommitted(failure) && !NeedsRecovery(failure) && !IsUnconfirmed(failure) {
			event.Status = "canceled"
			event.ErrorCode = "canceled"
		}
		var uncertain *SessionCopyUnconfirmedError
		if errors.As(failure, &uncertain) {
			event.Status = "unconfirmed"
			event.ErrorCode = "session_copy_unconfirmed"
		}
	}
	event.Stage = stage
	event.Pending = e.Store.HasJournal()
	if st, err := e.Store.Load(); err == nil {
		event.Current = st.Current
	}
	if err := e.Store.appendAudit(event); err != nil {
		return errors.New("操作结果日志保存失败；请检查当前状态")
	}
	return nil
}
