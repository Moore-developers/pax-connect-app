package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
)

// Replace only networksetup in the subprocess PATH; this exercises the real
// DarwinSystemProxy.Disable call chain without touching host preferences.
func TestPaxProxyDisablePreservesExternalEndpoint(t *testing.T) {
	for _, mode := range []string{"external", "owned", "mixed", "unreadable"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			writes := filepath.Join(directory, "writes")
			script := `#!/bin/sh
case "$1" in
 -listallhardwareports) printf 'Hardware Port: Wi-Fi\nDevice: en0\nEthernet Address: fixture\n' ;;
 -get*)
  if [ "$PAX_FIXTURE_MODE" = unreadable ]; then exit 1; fi
  port=17890
  if [ "$PAX_FIXTURE_MODE" = external ]; then port=10808; fi
  if [ "$PAX_FIXTURE_MODE" = mixed ] && [ "$1" = -getsecurewebproxy ]; then port=10808; fi
  if [ "$PAX_FIXTURE_MODE" = mixed ] && [ "$1" = -getsocksfirewallproxy ]; then
    printf 'Enabled: No\nServer: 127.0.0.1\nPort: 17890\n'
  else
    printf 'Enabled: Yes\nServer: 127.0.0.1\nPort: %s\n' "$port"
  fi ;;
 -set*) printf '%s\n' "$*" >> "$PAX_FIXTURE_WRITES" ;;
 *) exit 2 ;;
esac
`
			if err := os.WriteFile(filepath.Join(directory, "networksetup"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory)
			t.Setenv("PAX_FIXTURE_MODE", mode)
			t.Setenv("PAX_FIXTURE_WRITES", writes)
			p := &DarwinSystemProxy{interfaceName: "en0", serverAddr: M.ParseSocksaddr("127.0.0.1:17890"), supportSOCKS: true, isEnabled: true}
			err := p.Disable()
			if (err != nil) != (mode == "unreadable") {
				t.Fatalf("mode %s: err=%v", mode, err)
			}
			raw, readErr := os.ReadFile(writes)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			want := ""
			if mode == "owned" {
				want = "-setsocksfirewallproxystate Wi-Fi off\n-setwebproxystate Wi-Fi off\n-setsecurewebproxystate Wi-Fi off\n"
			}
			if mode == "mixed" {
				want = "-setwebproxystate Wi-Fi off\n"
			}
			if string(raw) != want {
				t.Fatalf("mode %s mutated proxy settings: %q; want %q", mode, raw, want)
			}
			if mode == "external" && strings.Contains(string(raw), "off") {
				t.Fatal("disabled third-party proxy")
			}
		})
	}
}
