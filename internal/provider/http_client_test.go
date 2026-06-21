package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
)

func TestNewHTTPClientReturnsNilWithoutProxy(t *testing.T) {
	client, err := NewHTTPClient(config.OutboundProxyConfig{})
	if err != nil {
		t.Fatalf("NewHTTPClient() error = %v", err)
	}
	if client != nil {
		t.Fatalf("NewHTTPClient() = %#v, want nil", client)
	}
}

func TestNewHTTPClientRejectsInvalidProxyURL(t *testing.T) {
	cases := []struct {
		name     string
		proxyURL string
		wantErr  string
	}{
		{name: "invalid-url", proxyURL: "http://[::1", wantErr: "parse proxy url: parse \"http://[::1\": missing ']' in host"},
		{name: "missing-host", proxyURL: "http://", wantErr: "proxy url host is required"},
		{name: "unsupported-scheme", proxyURL: "ftp://proxy.example:21", wantErr: "proxy url scheme \"ftp\" is unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewHTTPClient(config.OutboundProxyConfig{URL: tc.proxyURL})
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("NewHTTPClient() error = %v, want %s", err, tc.wantErr)
			}
		})
	}
}

func TestNewHTTPClientUsesProxy(t *testing.T) {
	seen := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	client, err := NewHTTPClient(config.OutboundProxyConfig{URL: proxy.URL})
	if err != nil {
		t.Fatalf("NewHTTPClient() error = %v", err)
	}
	if client == nil {
		t.Fatalf("NewHTTPClient() = nil, want client")
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://upstream.example/v1/models", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_ = resp.Body.Close()

	got := <-seen
	if got != "http://upstream.example/v1/models" {
		t.Fatalf("proxy saw URL %q, want upstream absolute URL", got)
	}
}
