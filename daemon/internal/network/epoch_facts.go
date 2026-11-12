package network

import (
	"net"
	"sort"
)

// stableAddressFacts keeps IPv4 addresses and IPv6 prefixes only. IPv6 privacy
// (temporary) addresses rotate daily; hashing them would invalidate retained
// CF evidence without any real network change. Link-local addresses are skipped.
func stableAddressFacts(addresses []net.Addr) []string {
	seen := map[string]bool{}
	var facts []string
	for _, a := range addresses {
		ip, network, err := net.ParseCIDR(a.String())
		if err != nil || ip.IsLinkLocalUnicast() {
			continue
		}
		fact := a.String()
		if ip.To4() == nil {
			fact = network.String()
		}
		if !seen[fact] {
			seen[fact] = true
			facts = append(facts, fact)
		}
	}
	sort.Strings(facts)
	return facts
}
