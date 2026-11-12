package network

import (
	"crypto/sha256"
	"fmt"
	"net"
	"sort"
	"strings"
)

// Epoch fingerprints physical interface availability and addresses, excluding
// product TUN interfaces. Failures refuse a switch instead of assuming stability.
func Epoch() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	var facts []string
	for _, iface := range interfaces {
		if strings.HasPrefix(iface.Name, "utun") || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return "", err
		}
		facts = append(facts, fmt.Sprintf("%s:%d:%s", iface.Name, iface.Flags, iface.HardwareAddr))
		for _, address := range addresses {
			facts = append(facts, iface.Name+":"+address.String())
		}
	}
	sort.Strings(facts)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(facts, "\n")))), nil
}
