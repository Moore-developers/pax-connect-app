// Package core owns the root process RPC boundary, protected resources and owner lifetime.
// It admits only its recorded user's Unix peers and serializes profile transitions.
// It does not manage App intent, credentials, third-party processes or routes;
// Typed profile admission/canonicalization and sing-box lifecycle stay behind
// injected singbox adapters; no upstream option types cross this boundary.
package core
