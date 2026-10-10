package switcher

import (
	"context"
	"errors"
)

// OperationError distinguishes an applied operation from an incomplete transaction.
// Frontends must never offer a blind retry for Pending or Committed outcomes.
type OperationError struct {
	Committed   bool
	Pending     bool
	RolledBack  bool
	Unconfirmed bool
	Cause       error
}

func (e *OperationError) Error() string { return e.Cause.Error() }
func (e *OperationError) Unwrap() error { return e.Cause }
func IsCommitted(err error) bool        { var o *OperationError; return errors.As(err, &o) && o.Committed }
func NeedsRecovery(err error) bool      { var o *OperationError; return errors.As(err, &o) && o.Pending }
func IsUnconfirmed(err error) bool {
	var o *OperationError
	var copy *SessionCopyUnconfirmedError
	return (errors.As(err, &o) && o.Unconfirmed) || errors.As(err, &copy)
}

func (e Engine) ensureStopped() error {
	if guard, ok := e.Runtime.(interface{ EnsureStopped() error }); ok {
		return guard.EnsureStopped()
	}
	return nil
}

type Outcome string

const (
	OutcomeUnchanged   Outcome = "unchanged"
	OutcomeRolledBack  Outcome = "rolled_back"
	OutcomeCommitted   Outcome = "committed"
	OutcomePending     Outcome = "pending"
	OutcomeUnconfirmed Outcome = "unconfirmed"
)

type OperationResult struct {
	Status           Outcome
	Current          string
	Target           string
	RecoveryRequired bool
	Warning          string
	Err              error
}

func ResultFor(err error) OperationResult {
	result := OperationResult{Status: OutcomeUnchanged, Err: err}
	if err == nil {
		result.Status = OutcomeCommitted
		return result
	}
	var operation *OperationError
	if errors.As(err, &operation) {
		result.RecoveryRequired = operation.Pending
		switch {
		case operation.Committed:
			result.Status = OutcomeCommitted
			result.Warning = err.Error()
			result.Err = nil
		case operation.Pending:
			result.Status = OutcomePending
		case operation.RolledBack:
			result.Status = OutcomeRolledBack
		case operation.Unconfirmed:
			result.Status = OutcomeUnconfirmed
		}
	}
	var uncertain *SessionCopyUnconfirmedError
	if errors.As(err, &uncertain) {
		result.Status = OutcomeUnconfirmed
	}
	return result
}

// Switch supplies a UI-independent result for a future GUI without GUI dependencies.
func (e Engine) Switch(ctx context.Context, name string, force bool) OperationResult {
	var err error
	if force {
		err = e.UseForcedContext(ctx, name)
	} else {
		err = e.UseContext(ctx, name)
	}
	result := ResultFor(err)
	result.Target = name
	if st, readErr := e.Store.Load(); readErr == nil {
		result.Current = st.Current
	}
	return result
}
