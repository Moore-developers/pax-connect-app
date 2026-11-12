package singbox

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"pax-connect/daemon/internal/measurement"
)

type localHTTPOutbound struct{ adapter.Outbound }

func (localHTTPOutbound) DialContext(ctx context.Context, kind string, address M.Socksaddr) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, kind, address.String())
}

type rewrittenHTTPClient struct {
	client *http.Client
	target string
}

func (c rewrittenHTTPClient) Do(request *http.Request) (*http.Response, error) {
	local, _ := http.NewRequestWithContext(request.Context(), request.Method, c.target, nil)
	local.Header = request.Header.Clone()
	return c.client.Do(local)
}
func TestMeasurementDoesNotForceConnectionCloseOnFrozenHTTPTask(t *testing.T) {
	// Reproduce the observed wire-level trigger: TLS succeeded on the host, but
	// forced-close streams ended before headers/body. A local HTTP fixture makes
	// that exact request policy deterministic without a remote node or credentials.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Connection") == "close" {
			connection, _, _ := w.(http.Hijacker).Hijack()
			_ = connection.Close()
			return
		}
		_, _ = io.WriteString(w, `{"ip":"8.8.8.8","country":"United States","region":"California","city":"Mountain View"}`)
	}))
	defer server.Close()
	transport := measurementHTTPTransport(localHTTPOutbound{})
	defer transport.CloseIdleConnections()
	value, code := measurement.NewExitLookup().Query(context.Background(), rewrittenHTTPClient{&http.Client{Transport: transport}, server.URL}, measurement.ExitQuery{Scope: "fixture"})
	if code != "" || value.IP != "8.8.8.8" {
		t.Fatal("forced-close tunneled request failed before response", code)
	}
}
