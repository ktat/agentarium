package server

import (
	"net/http"
	"testing"
)

func TestIsTrustedNetworkHost(t *testing.T) {
	trusted := []string{
		"localhost", "127.0.0.1", "127.0.0.5", "::1",
		"10.0.0.1", "10.255.255.255",
		"172.16.0.1", "172.31.255.255",
		"192.168.0.1", "192.168.1.100",
		"169.254.0.1",
		"fc00::1", "fd00::1",
		"fe80::1",
	}
	for _, h := range trusted {
		if !isTrustedNetworkHost(h) {
			t.Errorf("want trusted: %q", h)
		}
	}
	untrusted := []string{
		"8.8.8.8", "1.1.1.1",
		"example.com", "evil.example",
		"172.32.0.1",
		"",
	}
	for _, h := range untrusted {
		if isTrustedNetworkHost(h) {
			t.Errorf("want untrusted: %q", h)
		}
	}
}

func TestIsLocalOriginOrAbsent(t *testing.T) {
	req := &http.Request{Header: http.Header{}}
	if !IsLocalOriginOrAbsent(req) {
		t.Error("no headers should be allowed")
	}
	req = &http.Request{Header: http.Header{"Origin": []string{"http://localhost:8780"}}}
	if !IsLocalOriginOrAbsent(req) {
		t.Error("localhost origin should be allowed")
	}
	req = &http.Request{Header: http.Header{"Origin": []string{"https://evil.example"}}}
	if IsLocalOriginOrAbsent(req) {
		t.Error("public origin should be rejected")
	}
	req = &http.Request{Header: http.Header{"Origin": []string{"null"}}}
	if IsLocalOriginOrAbsent(req) {
		t.Error("null origin should be rejected")
	}
	req = &http.Request{Header: http.Header{"Referer": []string{"http://192.168.1.5/"}}}
	if !IsLocalOriginOrAbsent(req) {
		t.Error("private referer should be allowed")
	}
	req = &http.Request{Header: http.Header{"Referer": []string{"http://example.com/"}}}
	if IsLocalOriginOrAbsent(req) {
		t.Error("public referer should be rejected")
	}
}
