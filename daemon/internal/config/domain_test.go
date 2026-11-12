package config

import "testing"

func TestMainDomainUsesPublicSuffixBoundaries(t *testing.T) {
	for raw, want := range map[string]string{
		"wxs.x.com": "x.com", "api.X.COM.": "x.com", "www.example.co.uk": "example.co.uk",
		"cdn.alice.github.io": "alice.github.io", "cdn.bob.github.io": "bob.github.io",
		"www.食狮.com.cn": "xn--85x722f.com.cn", "localhost": "localhost", "co.uk": "co.uk",
		"192.0.2.1": "", "2001:db8::1": "", "https://x.com/path": "", "bad..example.com": "", "": "",
	} {
		if got := MainDomain(raw); got != want {
			t.Errorf("MainDomain(%q)=%q want %q", raw, got, want)
		}
	}
}
