package measurement

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// BenchmarkHTTPSCapacity is a controlled TLS/HTTP workload, not a claim about
// external nodes or the cost of constructing a sing-box protocol instance.
func BenchmarkHTTPSCapacity(b *testing.B) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	for _, parallel := range []int{20, 32, 48, 64, 100} {
		b.Run(fmt.Sprint(parallel), func(b *testing.B) {
			client := server.Client()
			transport := client.Transport.(*http.Transport).Clone()
			transport.DisableKeepAlives = true
			client.Transport = transport
			defer transport.CloseIdleConnections()
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				jobs := make(chan struct{}, 200)
				for i := 0; i < 200; i++ {
					jobs <- struct{}{}
				}
				close(jobs)
				var done sync.WaitGroup
				for i := 0; i < parallel; i++ {
					done.Add(1)
					go func() {
						defer done.Done()
						for range jobs {
							if _, err := Latency(context.Background(), client, []string{server.URL}); err != nil {
								b.Error(err)
							}
						}
					}()
				}
				done.Wait()
			}
		})
	}
}
