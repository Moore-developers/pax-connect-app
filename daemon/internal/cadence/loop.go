package cadence

import (
	"context"
	"time"
)

// Loop is one declared periodic job. Optional fields left nil mean: no wake, never gated, errors
// and changes ignored.
type Loop struct {
	Name     string               // declared name, passed to hooks
	Interval func() time.Duration // evaluated before every wait, so it may follow state
	Wake     []<-chan struct{}    // any value runs the next round early
	RunFirst bool                 // run once before the first wait
	// Gate returns "" to run, or a reason to skip this round (proxy off, sleeping, network down).
	Gate func() string
	// Run does one round and reports whether observable state changed.
	Run func(ctx context.Context) (changed bool, err error)

	OnChange func()                       // called after a round that reported changed
	OnSkip   func(name, reason string)    // called when the gate skips a round
	OnError  func(name string, err error) // called when Run fails; errors never stop the loop
	After    After                        // nil uses a real timer
}

// Serve runs the loop until ctx ends. Rounds never overlap: the next wait starts after Run returns.
func (l Loop) Serve(ctx context.Context) {
	after := l.After
	if after == nil {
		after = realAfter
	}
	if l.RunFirst && ctx.Err() == nil {
		l.round(ctx)
	}
	for waitWith(ctx, after, l.Interval(), l.Wake) {
		l.round(ctx)
	}
}

func (l Loop) round(ctx context.Context) {
	if l.Gate != nil {
		if reason := l.Gate(); reason != "" {
			if l.OnSkip != nil {
				l.OnSkip(l.Name, reason)
			}
			return
		}
	}
	changed, err := l.Run(ctx)
	if err != nil {
		if l.OnError != nil && ctx.Err() == nil {
			l.OnError(l.Name, err)
		}
		return
	}
	if changed && l.OnChange != nil {
		l.OnChange()
	}
}
