package provider

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ryanycheng/Syrogo/internal/config"
)

func NewHTTPClient(proxy config.OutboundProxyConfig) (*http.Client, error) {
	proxyURL := strings.TrimSpace(proxy.URL)
	if proxyURL == "" {
		return nil, nil
	}
	parsedProxyURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy url: %w", err)
	}
	if parsedProxyURL.Host == "" {
		return nil, fmt.Errorf("proxy url host is required")
	}
	switch parsedProxyURL.Scheme {
	case "http", "https", "socks5":
	default:
		return nil, fmt.Errorf("proxy url scheme %q is unsupported", parsedProxyURL.Scheme)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(parsedProxyURL)
	return &http.Client{Transport: transport}, nil
}
