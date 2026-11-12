// Package coreclient owns the user paxd's authenticated root-core RPC session.
// It uses Pax-owned models and JSON, never sing-box types, subprocesses or sudo.
// cmd assembles it; controller serializes the product lifecycle that consumes it.
package coreclient
