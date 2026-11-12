package measurement

import "context"

// DiagnosticProgress is an immutable update from actual foreground work.
// Empty states leave concurrent phases unchanged. Settled probes may have failed.
type DiagnosticProgress struct {
	ProbesCompleted, ProbesTotal int
	ExitState, SourcesState      string
}

type diagnosticObserverKey struct{}

// WithDiagnosticObserver attaches the owner of this diagnostic request only.
func WithDiagnosticObserver(ctx context.Context, observe func(DiagnosticProgress)) context.Context {
	return context.WithValue(ctx, diagnosticObserverKey{}, observe)
}

// ObserveDiagnostic publishes a phase transition or a settled request count.
func ObserveDiagnostic(ctx context.Context, progress DiagnosticProgress) {
	if observe, ok := ctx.Value(diagnosticObserverKey{}).(func(DiagnosticProgress)); ok {
		observe(progress)
	}
}
