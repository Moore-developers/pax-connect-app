// Package mcpserver exposes bounded, redacted queries and authorized route/network/Profile intents over MCP stdio.
// It converts MCP values to the authenticated user-daemon RPC boundary; it does
// not own business state, read SQLite, launch Pax or manage the root core.
// cmd/pax-mcp composes it. Query continuations are private, bounded read caches.
package mcpserver
