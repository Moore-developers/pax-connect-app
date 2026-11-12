package core

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

// Never echo the upstream text: it may contain credentials, tags, URLs or host
// paths. Expose a bounded vocabulary of causes, retaining useful diagnostics
// without attempting fragile redaction of arbitrary kernel error messages.
func startFailureDetail(err error) string {
	switch {
	case errors.Is(err, syscall.EADDRINUSE):
		return "address_in_use"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	case errors.Is(err, os.ErrNotExist):
		return "resource_unavailable"
	}
	message := strings.ToLower(err.Error())
	// Darwin route installation returns EEXIST when another TUN already owns
	// the prefix. The route context matters: EEXIST alone can be a cache/file.
	if strings.Contains(message, "add route:") && (errors.Is(err, syscall.EEXIST) || strings.Contains(message, "file exists")) {
		return "route_conflict"
	}
	for _, pair := range [][2]string{{"address already in use", "address_in_use"}, {"permission denied", "permission_denied"}, {"no such file", "resource_unavailable"}, {"invalid", "configuration_rejected"}, {"parse", "configuration_rejected"}, {"decode", "configuration_rejected"}} {
		if strings.Contains(message, pair[0]) {
			return pair[1]
		}
	}
	return "kernel_start_failed"
}
