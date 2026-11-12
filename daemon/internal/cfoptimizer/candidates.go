package cfoptimizer

import "net/netip"

// Seeds is a deterministic bounded catalog, not successful connection evidence.
// Opening the picker never scans the CIDRs or performs DNS/network I/O.
func Seeds(current string) []string {
	values := []string{}
	add := func(ip string) {
		if !IsAddress(ip) {
			return
		}
		for _, v := range values {
			if v == ip {
				return
			}
		}
		values = append(values, ip)
	}
	add(current)
	for _, prefix := range networks {
		address := prefix.Masked().Addr().Next()
		for i := 0; i < 4; i++ {
			add(address.String())
			address = address.Next()
		}
		if len(values) >= 64 {
			break
		}
	}
	if len(values) > 64 {
		values = values[:64]
	}
	return values
}

// NormalizeCandidate rejects non-CF and nonliteral addresses before any probe.
func NormalizeCandidate(raw string) (string, bool) {
	a, err := netip.ParseAddr(raw)
	if err != nil || !IsAddress(raw) {
		return "", false
	}
	return a.Unmap().String(), true
}
