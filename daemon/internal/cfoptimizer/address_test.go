package cfoptimizer

import "testing"

func TestConfiguredCloudflareAddress(t *testing.T) {
	for _, test := range []struct {
		server string
		want   bool
	}{
		{"104.16.1.2", true}, {"2606:4700::1", true}, {"::ffff:104.16.1.2", true}, {"8.8.8.8", false}, {"CF线路", false}, {"cloudflare.com", false}, {"192.168.1.1", false},
	} {
		if got := IsAddress(test.server); got != test.want {
			t.Fatal(test.server, got)
		}
	}
}
