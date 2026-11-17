package controller

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Local is a Go sentinel, not an IANA identifier. macOS publishes the actual
// system zone via /etc/localtime; an explicit TZ takes precedence for isolation.
func localTimezone() string {
	if zone := strings.TrimPrefix(os.Getenv("TZ"), ":"); zone != "" && !filepath.IsAbs(zone) {
		if _, err := time.LoadLocation(zone); err == nil {
			return zone
		}
	}
	if path, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(path, "/zoneinfo/"); ok {
			if _, err = time.LoadLocation(zone); err == nil {
				return zone
			}
		}
	}
	return time.Local.String()
}
