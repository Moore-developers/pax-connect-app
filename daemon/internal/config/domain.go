package config

import (
	"net/netip"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// MainDomain returns the registrable domain using the bundled public suffix list,
// including private suffixes (separate github.io tenants must not be merged).
// Local/suffix-only names remain intact; IPs and malformed hosts are not domains.
// Original observed hosts remain in FlowFact for diagnostics and rule matching.
func MainDomain(raw string) string {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if _, err := netip.ParseAddr(host); err == nil {
		return ""
	}
	host, err := idna.Lookup.ToASCII(host)
	if err != nil || host == "" || len(host) > 253 {
		return ""
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, c := range label {
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
				continue
			}
			return ""
		}
	}
	if root, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return root
	}
	return host
}
