package httpx

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Prestwickuk/lovelace/internal/config"
)

func TestExtractClientIPAndIPv6RateLimitKey(t *testing.T) {
	nets, err := config.ParseTrustedProxyCIDRs("10.0.0.0/8,172.16.0.0/12")
	if err != nil {
		t.Fatalf("ParseTrustedProxyCIDRs: %v", err)
	}

	// Multiple X-Forwarded-For header lines must be joined rather than trusting client's forged first header.
	r, _ := http.NewRequest(http.MethodGet, "https://admin.example.com/", nil)
	r.RemoteAddr = "10.0.0.5:44321"
	r.Header.Add("X-Forwarded-For", "198.51.100.99")
	r.Header.Add("X-Forwarded-For", "203.0.113.42, 172.18.0.2")

	if got := ExtractClientIP(r, true, nets); got != "203.0.113.42" {
		t.Fatalf("ExtractClientIP multi-header = %q, want 203.0.113.42", got)
	}

	// Malformed XFF hop must fail closed to immediate peer IP rather than falling through to X-Real-IP.
	rBad, _ := http.NewRequest(http.MethodGet, "https://admin.example.com/", nil)
	rBad.RemoteAddr = "[::ffff:10.0.0.5]:44321"
	rBad.Header.Set("X-Forwarded-For", "198.51.100.99, bogus-hop")
	rBad.Header.Set("X-Real-IP", "198.51.100.88")
	if got := ExtractClientIP(rBad, true, nets); got != "10.0.0.5" {
		t.Fatalf("ExtractClientIP malformed XFF = %q, want 10.0.0.5", got)
	}

	// Non-loopback IPv6 addresses sharing a /64 prefix must map to the same rate-limit bucket key.
	k1 := RateLimitKeyForIP("2001:db8:abcd:1234::1")
	k2 := RateLimitKeyForIP("2001:db8:abcd:1234:ffff:eeee:dddd:cccc")
	if k1 != k2 || k1 != "2001:db8:abcd:1234::/64" {
		t.Fatalf("expected /64 grouping for IPv6, got k1=%q k2=%q", k1, k2)
	}
}

func TestPublicRateLimiterBoundedBuckets(t *testing.T) {
	lim := NewPublicRateLimiter(2, time.Minute)
	now := time.Now().UTC()
	for i := 0; i < maxRateLimiterBuckets+500; i++ {
		lim.Allow(fmt.Sprintf("ip-%d", i), now)
	}
	if len(lim.buckets) > maxRateLimiterBuckets {
		t.Fatalf("expected buckets capped at %d, got %d", maxRateLimiterBuckets, len(lim.buckets))
	}
}
