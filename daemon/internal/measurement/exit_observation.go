package measurement

import (
	"context"
	"time"
)

// ExitAttempt is controlled evidence and immutable scheduling counts; never a URL or IP.
type ExitAttempt struct {
	Provider, Code, Event string
	HTTPStatus, Attempt   int
	Duration              time.Duration
	Pool                  ExitPoolState
}
type exitObserverKey struct{}

// WithExitObserver composes route/physical product observers and native telemetry.
func WithExitObserver(ctx context.Context, observe func(ExitAttempt)) context.Context {
	previous, _ := ctx.Value(exitObserverKey{}).(func(ExitAttempt))
	return context.WithValue(ctx, exitObserverKey{}, func(value ExitAttempt) {
		if previous != nil {
			previous(value)
		}
		observe(value)
	})
}
func observeExit(ctx context.Context, value ExitAttempt) {
	if observer, ok := ctx.Value(exitObserverKey{}).(func(ExitAttempt)); ok {
		observer(value)
	}
}
