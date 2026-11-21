// Package paxgate provides an explicit, runtime-owned connection admission seam.
// It is separate from passive observations and has no policy or background work.
package paxgate

import "context"

type contextKey struct{}

// Guard admits one logical outbound and retains its cancellation until release.
type Guard func(context.Context, string, func() error) (context.Context, func(), error)

// WithGuard installs the owner before the runtime is constructed.
func WithGuard(ctx context.Context, guard Guard) context.Context {
	return context.WithValue(ctx, contextKey{}, guard)
}

// Acquire runs after matching, before tracking or dialing. Call release on close.
func Acquire(ctx context.Context, tag string, closeConnection func() error) (context.Context, func(), error) {
	if guard, ok := ctx.Value(contextKey{}).(Guard); ok {
		return guard(ctx, tag, closeConnection)
	}
	return ctx, func() {}, nil
}
