package box

import "github.com/sagernet/sing-box/adapter"

// DNSRouter exposes the existing instance owner; it does not create a resolver.
func (s *Box) DNSRouter() adapter.DNSRouter { return s.dnsRouter }
