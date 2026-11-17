// Package mcpclientconfig inspects and merges only Pax MCP registrations in
// external client configuration. It owns bounded file observations, backups and
// conflict-checked writes, not API Profiles, credentials or client processes.
// The signed launcher composes it for App-owned installation actions.
package mcpclientconfig
