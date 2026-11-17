package channels

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRequestTimeout   = 15 * time.Second
	defaultMaxResponseBytes = 4 << 20 // 4 MiB
	defaultMaxRetries       = 3
	maxRetryBackoffWait     = 10 * time.Second
)

type providerEndpoint struct {
	DefaultBaseURL string
	AllowedHosts   []string
}

var officialProviderEndpoints = map[string]providerEndpoint{
	"beds24": {
		DefaultBaseURL: "https://api.beds24.com/v2",
		AllowedHosts:   []string{"api.beds24.com"},
	},
	"smoobu": {
		DefaultBaseURL: "https://login.smoobu.com/api",
		AllowedHosts:   []string{"login.smoobu.com"},
	},
	"lodgify": {
		DefaultBaseURL: "https://api.lodgify.com",
		AllowedHosts:   []string{"api.lodgify.com"},
	},
	"channex": {
		DefaultBaseURL: "https://app.channex.io/api/v1",
		AllowedHosts:   []string{"app.channex.io", "staging.channex.io"},
	},
	"channex:staging": {
		DefaultBaseURL: "https://staging.channex.io/api/v1",
		AllowedHosts:   []string{"staging.channex.io", "app.channex.io"},
	},
}

// SafeHTTPClient executes outbound channel requests with TLS>=1.2, redirect blocking,
// host allowlisting, response-size bounding, and rate-limit-aware bounded retries.
type SafeHTTPClient struct {
	client           *http.Client
	clock            func() time.Time
	sleep            func(context.Context, time.Duration) error
	jitter           func(time.Duration) time.Duration
	nonceFunc        func() string
	maxRetries       int
	requestTimeout   time.Duration
	maxResponseBytes int64
	allowHTTPTests   bool
	baseURLOverrides map[string]string
}

func NewSafeHTTPClient(cfg AdapterConfig) *SafeHTTPClient {
	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	sleepFn := cfg.Sleep
	if sleepFn == nil {
		sleepFn = func(ctx context.Context, d time.Duration) error {
			if d <= 0 {
				return nil
			}
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	jitterFn := cfg.Jitter
	if jitterFn == nil {
		jitterFn = func(base time.Duration) time.Duration {
			if base <= 0 {
				return 0
			}
			half := base / 2
			if half <= 0 {
				return base
			}
			return half + time.Duration(rand.Int64N(int64(half)+1))
		}
	}
	nonceFn := cfg.NonceFunc
	if nonceFn == nil {
		nonceFn = GenerateNonceUUID
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	reqTimeout := cfg.RequestTimeout
	if reqTimeout <= 0 {
		reqTimeout = defaultRequestTimeout
	}
	maxBytes := cfg.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponseBytes
	}

	rawDialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	dialCtx := rawDialer.DialContext
	if !cfg.AllowHTTPForTests {
		dialCtx = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				if err != nil {
					return nil, err
				}
				return nil, errors.New("no IP addresses resolved for provider host")
			}
			for _, ip := range ips {
				if isBlockedOutboundAddr(ip) {
					return nil, fmt.Errorf("blocked private or loopback provider IP %s", ip.String())
				}
			}
			var lastErr error
			for _, ip := range ips {
				conn, dErr := rawDialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
				if dErr == nil {
					return conn, nil
				}
				lastErr = dErr
			}
			return nil, lastErr
		}
	}
	baseTransport := &http.Transport{
		Proxy:       nil,
		DialContext: dialCtx,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: reqTimeout,
	}

	var baseHTTP *http.Client
	if cfg.HTTPClient != nil {
		copied := *cfg.HTTPClient
		copied.Timeout = reqTimeout
		copied.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return ErrRedirectBlocked
		}
		baseHTTP = &copied
	} else {
		baseHTTP = &http.Client{
			Timeout:   reqTimeout,
			Transport: baseTransport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return ErrRedirectBlocked
			},
		}
	}

	overrides := make(map[string]string, len(cfg.ProviderBaseURLs))
	for k, v := range cfg.ProviderBaseURLs {
		overrides[strings.ToLower(strings.TrimSpace(k))] = strings.TrimRight(strings.TrimSpace(v), "/")
	}

	return &SafeHTTPClient{
		client:           baseHTTP,
		clock:            clock,
		sleep:            sleepFn,
		jitter:           jitterFn,
		nonceFunc:        nonceFn,
		maxRetries:       maxRetries,
		requestTimeout:   reqTimeout,
		maxResponseBytes: maxBytes,
		allowHTTPTests:   cfg.AllowHTTPForTests,
		baseURLOverrides: overrides,
	}
}

func (c *SafeHTTPClient) Now() time.Time {
	return c.clock().UTC()
}

func (c *SafeHTTPClient) NextNonce() string {
	return c.nonceFunc()
}

// ResolveEndpoint returns a validated base URL and parsed URL components for the provider key.
func (c *SafeHTTPClient) ResolveEndpoint(providerKey string) (*url.URL, error) {
	key := strings.ToLower(strings.TrimSpace(providerKey))
	spec, ok := officialProviderEndpoints[key]
	if !ok {
		return nil, newProviderError(ErrInvalidInput, key, "resolve_endpoint", 0, "UNKNOWN_PROVIDER", "unknown provider endpoint")
	}

	raw := spec.DefaultBaseURL
	isOverride := false
	if ov := c.baseURLOverrides[key]; ov != "" {
		raw = ov
		isOverride = true
	} else if baseKey, _, found := strings.Cut(key, ":"); found {
		if ov := c.baseURLOverrides[baseKey]; ov != "" {
			raw = ov
			isOverride = true
		}
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, newProviderError(ErrInvalidInput, key, "resolve_endpoint", 0, "INVALID_ENDPOINT_URL", "invalid endpoint URL")
	}

	if isOverride && c.allowHTTPTests {
		host := strings.ToLower(parsed.Hostname())
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, newProviderError(ErrInvalidInput, key, "resolve_endpoint", 0, "INVALID_SCHEME", "unsupported test scheme")
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			return nil, newProviderError(ErrInvalidInput, key, "resolve_endpoint", 0, "UNSAFE_TEST_HOST", "test endpoint overrides must target loopback only")
		}
		return parsed, nil
	}

	if parsed.Scheme != "https" {
		return nil, newProviderError(ErrInvalidInput, key, "resolve_endpoint", 0, "HTTPS_REQUIRED", "provider endpoint must use HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := false
	for _, ah := range spec.AllowedHosts {
		if host == ah {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, newProviderError(ErrInvalidInput, key, "resolve_endpoint", 0, "HOST_NOT_ALLOWLISTED", "provider host not in official allowlist")
	}
	return parsed, nil
}

type httpResponseData struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// RequestSpec describes one provider HTTP call built per attempt via headerBuilder.
// Re-running headerBuilder on retry guarantees fresh X-Timestamp / X-Nonce / X-Signature values.
type RequestSpec struct {
	ProviderCode string
	EndpointKey  string
	Operation    string
	Method       string
	Path         string
	Query        url.Values
	Body         []byte
	ContentType  string
	Idempotent   bool
	BuildHeaders func(attempt int, reqURL *url.URL, canonicalQuery string, body []byte) (http.Header, error)
}

func (c *SafeHTTPClient) Do(ctx context.Context, spec RequestSpec) (*httpResponseData, error) {
	endpointKey := spec.EndpointKey
	if endpointKey == "" {
		endpointKey = spec.ProviderCode
	}
	baseURL, err := c.ResolveEndpoint(endpointKey)
	if err != nil {
		return nil, err
	}

	fullPath := strings.TrimRight(baseURL.Path, "/")
	if spec.Path != "" {
		if strings.Contains(spec.Path, "..") || strings.ContainsRune(spec.Path, '\\') {
			return nil, newProviderError(ErrInvalidInput, spec.ProviderCode, spec.Operation, 0, "INVALID_PATH_SEGMENT", "provider request path contains disallowed traversal characters")
		}
		if !strings.HasPrefix(spec.Path, "/") {
			fullPath += "/" + spec.Path
		} else {
			fullPath += spec.Path
		}
	}
	canonicalQuery := EncodeCanonicalQueryRFC3986(spec.Query)

	target := *baseURL
	target.Path = fullPath
	target.RawQuery = canonicalQuery

	attempts := c.maxRetries
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, newProviderError(ErrProviderUnavailable, spec.ProviderCode, spec.Operation, 0, "CONTEXT_CANCELLED", "request cancelled")
		}

		reqCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
		var bodyReader io.Reader
		if len(spec.Body) > 0 {
			bodyReader = bytes.NewReader(spec.Body)
		}
		req, err := http.NewRequestWithContext(reqCtx, spec.Method, target.String(), bodyReader)
		if err != nil {
			cancel()
			return nil, newProviderError(ErrInvalidInput, spec.ProviderCode, spec.Operation, 0, "REQUEST_BUILD_FAILED", "failed to construct HTTP request")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "LOVELACE-PMS/4.0")
		if spec.ContentType != "" {
			req.Header.Set("Content-Type", spec.ContentType)
		} else if len(spec.Body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}

		if spec.BuildHeaders != nil {
			extraHeaders, err := spec.BuildHeaders(attempt, &target, canonicalQuery, spec.Body)
			if err != nil {
				cancel()
				return nil, err
			}
			for k, vals := range extraHeaders {
				for _, v := range vals {
					req.Header.Add(k, v)
				}
			}
		}

		resp, err := c.client.Do(req)
		if err != nil {
			cancel()
			if errors.Is(err, ErrRedirectBlocked) {
				return nil, newProviderError(ErrRedirectBlocked, spec.ProviderCode, spec.Operation, 0, "REDIRECT_BLOCKED", "HTTP redirect blocked to protect credentials")
			}
			lastErr = newProviderError(ErrProviderUnavailable, spec.ProviderCode, spec.Operation, 0, "NETWORK_ERROR", "transport request failed")
			if attempt < attempts-1 {
				if waitErr := c.backoffSleep(ctx, attempt, 0); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, lastErr
		}

		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
		resp.Body.Close()
		cancel()

		if readErr != nil {
			lastErr = newProviderError(ErrProviderUnavailable, spec.ProviderCode, spec.Operation, resp.StatusCode, "RESPONSE_READ_ERROR", "failed reading provider response")
			if spec.Idempotent && attempt < attempts-1 {
				if waitErr := c.backoffSleep(ctx, attempt, 0); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, lastErr
		}
		if int64(len(bodyBytes)) > c.maxResponseBytes {
			return nil, newProviderError(ErrResponseTooLarge, spec.ProviderCode, spec.Operation, resp.StatusCode, "RESPONSE_TOO_LARGE", fmt.Sprintf("response exceeded %d bytes", c.maxResponseBytes))
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := parseRetryAfter(resp.Header, c.Now())
			pe := newProviderError(ErrRateLimited, spec.ProviderCode, spec.Operation, resp.StatusCode, "RATE_LIMITED", "provider rate limit hit")
			pe.RetryAfter = retryAfter
			if attempt < attempts-1 {
				if waitErr := c.backoffSleep(ctx, attempt, retryAfter); waitErr != nil {
					return nil, pe
				}
				lastErr = pe
				continue
			}
			return nil, pe
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return &httpResponseData{
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
				Body:       bodyBytes,
			}, newProviderError(ErrAuthFailed, spec.ProviderCode, spec.Operation, resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "provider rejected authentication credentials")
		}

		if resp.StatusCode >= 500 {
			lastErr = newProviderError(ErrProviderUnavailable, spec.ProviderCode, spec.Operation, resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "provider returned server error")
			if spec.Idempotent && attempt < attempts-1 {
				if waitErr := c.backoffSleep(ctx, attempt, parseRetryAfter(resp.Header, c.Now())); waitErr != nil {
					return nil, lastErr
				}
				continue
			}
			return nil, lastErr
		}

		return &httpResponseData{
			StatusCode: resp.StatusCode,
			Header:     resp.Header.Clone(),
			Body:       bodyBytes,
		}, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, newProviderError(ErrProviderUnavailable, spec.ProviderCode, spec.Operation, 0, "RETRY_EXHAUSTED", "provider request exhausted retries")
}

func (c *SafeHTTPClient) backoffSleep(ctx context.Context, attempt int, retryAfter time.Duration) error {
	wait := retryAfter
	if wait <= 0 {
		base := 150 * time.Millisecond * time.Duration(1<<uint(attempt))
		wait = c.jitter(base)
	}
	if wait > maxRetryBackoffWait {
		wait = maxRetryBackoffWait
	}
	return c.sleep(ctx, wait)
}

func parseRetryAfter(header http.Header, now time.Time) time.Duration {
	if header == nil {
		return 0
	}
	if raw := strings.TrimSpace(header.Get("x-five-min-limit-resets-in")); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	if raw := strings.TrimSpace(header.Get("X-RateLimit-Retry-After")); raw != "" {
		if val, err := strconv.ParseInt(raw, 10, 64); err == nil && val > 0 {
			if val > 1_000_000_000 { // Unix timestamp in seconds
				d := time.Unix(val, 0).UTC().Sub(now.UTC())
				if d > 0 {
					return d
				}
				return time.Second
			}
			return time.Duration(val) * time.Second
		}
	}
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
		if t, err := http.ParseTime(raw); err == nil {
			d := t.UTC().Sub(now.UTC())
			if d > 0 {
				return d
			}
		}
	}
	return 0
}

func isBlockedOutboundAddr(addr netip.Addr) bool {
	a := addr.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return true
	}
	// Block RFC 6598 CGNAT (100.64.0.0/10).
	if a.Is4() {
		b := a.As4()
		if b[0] == 100 && b[1] >= 64 && b[1] <= 127 {
			return true
		}
	}
	return false
}
