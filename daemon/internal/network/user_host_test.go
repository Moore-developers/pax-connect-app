package network

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"testing"
)

// Explicit opt-in: read-only host evidence, never a normal unit-test prerequisite.
func TestUserHostObservations(t *testing.T) {
	if os.Getenv("PAX_USER_HOST_CHECK") != "1" {
		t.Skip("manual read-only host check")
	}
	if os.Geteuid() == 0 {
		t.Fatal("must run as normal user")
	}
	t.Logf("euid=%d", os.Geteuid())
	ctx := context.Background()
	if physical, err := PhysicalNetwork(ctx); err != nil {
		t.Errorf("PhysicalNetwork: %v", err)
	} else {
		t.Logf("PhysicalNetwork: available interface=%s IPv6State=%s IPv4Only=%t (addresses omitted)", physical.Name, physical.IPv6State, physical.IPv4Only())
		if err := ValidatePhysicalBinding(ctx, physical); err != nil {
			t.Errorf("physical binding: %v", err)
		} else {
			t.Log("physical binding socket options: available (no traffic)")
		}
	}
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)
	if err != nil {
		t.Errorf("AF_ROUTE: %v", err)
	} else {
		_ = unix.Close(fd)
		t.Log("AF_ROUTE: opened and closed; observation only")
	}
}
