// Package mcpinstall owns the private MCP launcher registration and verified bundle lookup.
// It reuses storage for atomic private files and never starts App, daemon or core.
// The App installation helper and stdio adapter share this boundary.
package mcpinstall
