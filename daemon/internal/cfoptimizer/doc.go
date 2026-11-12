// Package cfoptimizer owns bounded Cloudflare discovery, precision ranking,
// stage-separated retained evidence and manual edge measurement policies.
// Callers inject network probes, clock and randomness; it owns no tasks, route
// configuration, credentials, persistence I/O, protobuf or sing-box types.
// Edge reachability never proves a complete proxy route or application success.
package cfoptimizer
