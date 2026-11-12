package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
)

// TLSMode defines an explicit product identity-verification choice.
type TLSMode string

const (
	TLSStrict TLSMode = "strict"
	TLSSkip   TLSMode = "skip_verify"
)

var (
	ErrTLSConfirmation = errors.New("tls_confirmation_required")
	ErrTLSStale        = errors.New("tls_policy_stale")
	ErrTLSStore        = errors.New("tls_policy_store_invalid")
	ErrTLSSave         = errors.New("tls_policy_save_failed")
)

// TLSPolicyView contains no credentials; SKIP with NeedsConfirmation cannot run.
type TLSPolicyView struct {
	Mode              TLSMode `json:"mode"`
	UserOverride      bool    `json:"user_override"`
	NeedsConfirmation bool    `json:"needs_confirmation"`
	IdentityToken     string  `json:"identity_token"`
	Revision          uint64  `json:"revision"`
}

// TLSRecord belongs to one stable route ID; identity changes overwrite its authorization.
type TLSRecord struct {
	IdentityToken string  `json:"identity_token"`
	Revision      uint64  `json:"revision"`
	Mode          TLSMode `json:"mode"`
	UserOverride  bool    `json:"user_override"`
}

// TLSPolicies is the small product policy document; store persists mutations atomically.
type TLSPolicies struct {
	SchemaVersion int                  `json:"schema_version"`
	Records       map[string]TLSRecord `json:"records"`
}

func NewTLSPolicies() TLSPolicies {
	return TLSPolicies{SchemaVersion: 1, Records: make(map[string]TLSRecord)}
}
func canonicalHost(value string) string {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if ip, err := netip.ParseAddr(value); err == nil {
		return ip.Unmap().String()
	}
	return value
}

// TLSIdentity binds consent to protocol, address, port and SNI, excluding names and secrets.
func TLSIdentity(p DebugProfile) string {
	data, _ := json.Marshal([]any{strings.ToLower(p.ProtocolName), canonicalHost(p.Server), p.Port, canonicalHost(p.ServerName)})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// SourceTLSPolicy resolves the declared source choice without local persistence.
// A local user override is applied by TLSPolicies.Resolve when available.
func SourceTLSPolicy(p RouteConfig) TLSPolicyView {
	mode := TLSStrict
	if p.TLSSkipSuggested && p.Security != "none" && p.Security != "reality" {
		mode = TLSSkip
	}
	return TLSPolicyView{Mode: mode, IdentityToken: TLSIdentity(p), Revision: 1}
}

// Resolve gives explicit user settings precedence over the current source policy.
// Source skip declarations are effective without being stored as user authorization.
func (d *TLSPolicies) Resolve(p DebugProfile) (TLSPolicyView, error) {
	if d.SchemaVersion != 1 || d.Records == nil || len(d.Records) > 1024 {
		return TLSPolicyView{}, ErrTLSStore
	}
	if p.ID == "" || len(p.ID) > 64 {
		return TLSPolicyView{}, ErrTLSStore
	}
	token := TLSIdentity(p)
	record, exists := d.Records[p.ID]
	if exists && (record.Revision == 0 || len(record.IdentityToken) != 64 || (record.Mode != TLSStrict && record.Mode != TLSSkip) || !record.UserOverride && record.Mode == TLSSkip) {
		return TLSPolicyView{}, ErrTLSStore
	}
	if (!exists && len(d.Records) >= 1024) || record.Revision == ^uint64(0) {
		return TLSPolicyView{}, ErrTLSStore
	}
	if !exists || record.IdentityToken != token {
		record = TLSRecord{IdentityToken: token, Revision: record.Revision + 1, Mode: TLSStrict}
		d.Records[p.ID] = record
	}
	view := SourceTLSPolicy(p)
	view.Revision = record.Revision
	view.UserOverride = record.UserOverride
	if record.UserOverride {
		view.Mode = record.Mode
	}

	return view, nil
}

// Update is compare-and-save on the actual identity; explicit skip requires confirmation.
func (d *TLSPolicies) Update(p DebugProfile, token string, revision uint64, mode TLSMode, confirm bool) error {
	view, err := d.Resolve(p)
	if err != nil {
		return err
	}
	if token != view.IdentityToken || revision != view.Revision {
		return ErrTLSStale
	}
	if mode != TLSStrict && mode != TLSSkip {
		return ErrTLSStore
	}
	if mode == TLSSkip && !confirm {
		return ErrTLSConfirmation
	}
	record := d.Records[p.ID]
	if record.UserOverride && record.Mode == mode {
		return nil
	}
	record.UserOverride = true
	record.Mode = mode
	record.Revision++
	d.Records[p.ID] = record
	return nil
}
