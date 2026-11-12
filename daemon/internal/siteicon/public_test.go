package siteicon_test

import (
	"context"
	"os"
	"pax-connect/daemon/internal/siteicon"
	"pax-connect/daemon/internal/subfetch"
	"testing"
)

func TestPublicOriginIconOptIn(t *testing.T) {
	if os.Getenv("PAX_SITEICON_PUBLIC") != "1" {
		t.Skip("explicit public-origin test")
	}
	raw, err := subfetch.New().FetchIcon(context.Background(), "github.com")
	if err != nil {
		t.Fatal(err)
	}
	png, err := siteicon.PNG(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(png) == 0 {
		t.Fatal("empty sanitized image")
	}
	t.Logf("public origin github.com raw=%d sanitized PNG=%d bytes", len(raw), len(png))
}
