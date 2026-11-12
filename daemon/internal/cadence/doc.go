// Package cadence is the daemon's single scheduling primitive: a named loop with a state-dependent
// interval, optional early-wake channels, a gate and a changed-reporting run function, plus the
// one shared wait helper. Business intervals live in intervals.go. It depends on the standard
// library only.
package cadence
