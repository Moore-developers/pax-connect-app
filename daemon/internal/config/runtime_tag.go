package config

import (
	"crypto/sha256"
	"fmt"
)

// RuntimeTag is an opaque route/config identity, never a node name or address.
func RuntimeTag(p RouteConfig) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", p.ID, p.ResolvedConfigRevision)))
	return fmt.Sprintf("pax-node-%x", digest[:16])
}
