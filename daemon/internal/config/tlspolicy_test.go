package config

import "testing"

func tlsProfile() DebugProfile {
	return DebugProfile{NodeProfile: NodeProfile{ID: "debug", Name: "测试", ProtocolName: "hysteria2", Server: "EXAMPLE.com.", Port: 443}, ServerName: "Example.COM."}
}
func TestSourcePolicyAndExplicitOverridesRemainIdentityBound(t *testing.T) {
	p := tlsProfile()
	p.TLSSkipSuggested = true
	doc := NewTLSPolicies()
	view, err := doc.Resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	if view.NeedsConfirmation || view.Mode != TLSSkip || view.UserOverride {
		t.Fatal("source skip was blocked or became a user override")
	}
	if err := doc.Update(p, view.IdentityToken, view.Revision, TLSSkip, false); err != ErrTLSConfirmation {
		t.Fatal("missing confirmation accepted")
	}
	if err := doc.Update(p, view.IdentityToken, view.Revision, TLSSkip, true); err != nil {
		t.Fatal(err)
	}
	view, err = doc.Resolve(p)
	if err != nil || view.NeedsConfirmation || !view.UserOverride {
		t.Fatal("confirmed skip lost")
	}
	p.Name = "renamed"
	p.Server = "example.com"
	p.ServerName = "example.com"
	renamed, _ := doc.Resolve(p)
	if renamed.IdentityToken != view.IdentityToken || renamed.NeedsConfirmation {
		t.Fatal("rename/equivalent host invalidated")
	}
	p.Server = "other.example"
	changed, _ := doc.Resolve(p)
	if changed.Mode != TLSSkip || changed.UserOverride {
		t.Fatal("identity change retained consent")
	}
	p = tlsProfile()
	p.TLSSkipSuggested = true
	returned, _ := doc.Resolve(p)
	if returned.Mode != TLSSkip || returned.UserOverride {
		t.Fatal("A-B-A revived consent")
	}
}
func TestTLSLocalStrictOverridesSourceAndCASRejectsStale(t *testing.T) {
	p := tlsProfile()
	p.TLSSkipSuggested = true
	doc := NewTLSPolicies()
	view, _ := doc.Resolve(p)
	if err := doc.Update(p, view.IdentityToken, view.Revision, TLSStrict, false); err != nil {
		t.Fatal(err)
	}
	updated, _ := doc.Resolve(p)
	if updated.Mode != TLSStrict || updated.NeedsConfirmation {
		t.Fatal("local strict ignored")
	}
	if err := doc.Update(p, view.IdentityToken, view.Revision, TLSSkip, true); err != ErrTLSStale {
		t.Fatal("stale request accepted")
	}
}

func TestTLSModeAndSkipConsentSurviveOnlySameCurrentIdentity(t *testing.T) {
	p := tlsProfile()
	doc := NewTLSPolicies()
	view, _ := doc.Resolve(p)
	if err := doc.Update(p, view.IdentityToken, view.Revision, TLSSkip, true); err != nil {
		t.Fatal(err)
	}
	view, _ = doc.Resolve(p)
	if err := doc.Update(p, view.IdentityToken, view.Revision, TLSStrict, false); err != nil {
		t.Fatal(err)
	}
	view, _ = doc.Resolve(p)
	if err := doc.Update(p, view.IdentityToken, view.Revision, TLSSkip, false); err != ErrTLSConfirmation {
		t.Fatal("old consent revived after strict")
	}
}

func TestDefaultCertificateValidationDoesNotFallback(t *testing.T) {
	p := tlsProfile()
	doc := NewTLSPolicies()
	view, err := doc.Resolve(p)
	if err != nil || view.Mode != TLSStrict || view.UserOverride || view.NeedsConfirmation {
		t.Fatal("default verification changed", err)
	}
	p.TLSSkipSuggested = true
	source, _ := doc.Resolve(p)
	if source.Mode != TLSSkip || source.UserOverride {
		t.Fatal("source policy not applied")
	}
	p.TLSSkipSuggested = false
	restored, _ := doc.Resolve(p)
	if restored.Mode != TLSStrict || restored.UserOverride {
		t.Fatal("removed source skip remained effective")
	}
}
