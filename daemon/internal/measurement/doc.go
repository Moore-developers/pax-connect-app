// Package measurement measures HTTPS first response, exit address and bounded
// throughput. It owns no routes, credentials, TUN, protobuf or sing-box types.
// Callers inject an HTTP client over a frozen protocol session and own its lifetime.
package measurement
