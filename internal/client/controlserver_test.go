package client

import (
	"net/http"
	"testing"
)

func TestNewControlServerTransportUsesDefaultIdleTimeout(t *testing.T) {
	transport, ok := newControlServerTransport("http", false).(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", transport)
	}

	if transport.IdleConnTimeout <= 0 {
		t.Fatalf("expected IdleConnTimeout to be configured, got %s", transport.IdleConnTimeout)
	}
	if transport.MaxIdleConns <= 0 {
		t.Fatalf("expected MaxIdleConns to be configured, got %d", transport.MaxIdleConns)
	}
}

func TestNewControlServerTransportConfiguresTLSSkipVerify(t *testing.T) {
	transport, ok := newControlServerTransport("https", true).(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", transport)
	}

	if transport.TLSClientConfig == nil {
		t.Fatal("expected TLSClientConfig to be configured")
	}
	if !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify to be true")
	}
}
