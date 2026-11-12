package config

import (
	"net/netip"
	"strings"
	"time"
	"unicode"
)

// ExitLocation is public exit evidence, bound to its IP/config/network. It never
// stores coordinates, raw provider responses or request credentials.
type ExitLocation struct {
	IP, CountryCode, Country, Region, City, ISP, ASN string
	Source, GeoSource, GeoError, Network             string
	At, GeoAt                                        time.Time
	ConfigRevision                                   uint64
}

func (v *ExitLocation) Valid() bool {
	if v == nil {
		return true
	}
	ip, err := netip.ParseAddr(v.IP)
	if err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || v.At.IsZero() {
		return false
	}
	for _, s := range []string{v.CountryCode, v.Country, v.Region, v.City, v.ISP, v.ASN, v.Source, v.GeoSource, v.GeoError, v.Network} {
		if len(s) > 128 || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return false
		}
	}
	return true
}
