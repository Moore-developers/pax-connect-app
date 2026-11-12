package aiprofile

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"
)

// sourceOfEndpoint describes the configured route, not a verified provider bill.
func sourceOfEndpoint(cli CLI, endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || !validEndpoint(endpoint) {
		return "unknown"
	}
	if u.Scheme == "https" && (u.Port() == "" || u.Port() == "443") && (u.Path == "" || u.Path == "/" || u.Path == "/v1" || u.Path == "/v1/") {
		if cli == Claude && strings.EqualFold(u.Hostname(), "api.anthropic.com") || cli == Codex && strings.EqualFold(u.Hostname(), "api.openai.com") {
			return "official_api"
		}
	}
	return "relay_api"
}

// observeSource records only a change in the observed route/ownership. Secrets and
// fingerprints never enter this timeline. Repeated checks and restarts add no event.
func (m *Manager) observeSource(ctx context.Context, b Binding) error {
	if b.CLI == DSH {
		return nil
	}
	a := Attribution{ServiceSource: b.ServiceSource, Multiplier: 1}
	if a.ServiceSource == "" {
		a.ServiceSource = "unknown"
	}
	if b.State == "applied" || b.State == "pending" {
		p, ok := m.profiles[b.ProfileID]
		if ok && p.Kind != "native" && m.recovery.Applied[b.CLI].EventAt != 0 {
			a.ProfileID, a.ProfileName, a.Multiplier, a.Models = p.ID, p.Name, p.Multiplier, p.ModelMultipliers
		}
	}
	var previousSource, previousID, kind string
	var at, since int64
	err := m.events.QueryRowContext(ctx, "SELECT service_source,profile_id,kind,at,source_since FROM profile_events WHERE cli=? AND kind IN ('apply','observe') ORDER BY at DESC,event_key DESC LIMIT 1", b.CLI).Scan(&previousSource, &previousID, &kind, &at, &since)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return StoreInvalid
	}
	if errors.Is(err, sql.ErrNoRows) && b.State == "missing" {
		return nil
	}
	if err == nil && a.ServiceSource == previousSource && a.ProfileID == previousID {
		return nil
	}
	// Legacy explicit applies gain a label without invalidating an unchanged route.
	if err == nil && previousSource == "" && previousID != "" && previousID == a.ProfileID {
		a.Since = 0
	} else {
		a.Since = time.Now().UnixMilli()
	}
	return m.recordEvent(ctx, "observe:"+string(b.CLI)+":"+newID(), b.CLI, time.Now().UnixNano(), a, "observe")
}
