package measurement

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDownloadRequiresFullBudgetAndCountsOnlyReceivedBytes(t *testing.T) {
	for _, test := range []struct {
		body string
		code string
	}{{strings.Repeat("x", 32), ""}, {strings.Repeat("x", 31), "download_incomplete"}} {
		client := &http.Client{Transport: exitTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(test.body))}, nil
		})}
		value, err := Download(context.Background(), client, 32)
		if test.code == "" {
			if err != nil || value.Bytes != 32 || value.BytesPerSecond <= 0 {
				t.Fatal(value, err)
			}
		} else if err == nil || err.Error() != test.code {
			t.Fatal("short transfer reported success", value, err)
		}
	}
}

func TestDownloadPreservesSanitizedNodeCertificateFailure(t *testing.T) {
	client := &http.Client{Transport: exitTransport(func(*http.Request) (*http.Response, error) {
		return nil, &Failure{Code: "tls_certificate_invalid", Reason: "untrusted"}
	})}
	_, err := Download(context.Background(), client, 32)
	var certificate *Failure
	if !errors.As(err, &certificate) || certificate.Reason != "untrusted" {
		t.Fatal("certificate failure collapsed", err)
	}
}
