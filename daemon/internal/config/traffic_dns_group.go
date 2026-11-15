package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// TrafficDNSGroupID fixes the existing domain/type/result group identity.
func TrafficDNSGroupID(domain, queryType, result string) string {
	value, _ := json.Marshal([]string{domain, queryType, result})
	sum := sha256.Sum256(value)
	return "dns-group:" + hex.EncodeToString(sum[:])
}
