package payments

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateCheckoutReturnURL verifies a Stripe Checkout return/cancel URL against approved
// public application origins and configured property domains, failing closed on external
// redirects, insecure HTTP in production, protocol-relative URLs, subdomain spoofs, and malformed inputs.
func ValidateCheckoutReturnURL(rawURL string, allowedOrigins []string, approvedDomains []string, isProduction bool) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("checkout return URL is empty")
	}
	if strings.TrimSpace(rawURL) != rawURL {
		return "", fmt.Errorf("checkout return URL contains surrounding whitespace")
	}
	for _, r := range rawURL {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("checkout return URL contains control characters")
		}
	}
	if strings.HasPrefix(rawURL, "//") || strings.Contains(rawURL, "\\") {
		return "", fmt.Errorf("protocol-relative and backslash checkout URLs are prohibited")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("malformed checkout return URL: %w", err)
	}
	if !u.IsAbs() || u.Host == "" {
		return "", fmt.Errorf("checkout return URL must be an absolute URL with scheme and host")
	}
	if u.User != nil {
		return "", fmt.Errorf("checkout return URL must not contain userinfo")
	}

	scheme := strings.ToLower(u.Scheme)
	if isProduction {
		if scheme != "https" {
			return "", fmt.Errorf("checkout return URL must use https in production (got %q)", u.Scheme)
		}
	} else if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("checkout return URL scheme %q is not permitted", u.Scheme)
	}

	if strings.HasPrefix(u.Path, "//") {
		return "", fmt.Errorf("protocol-relative path in checkout return URL is prohibited")
	}

	reqHost := strings.ToLower(u.Hostname())
	reqHostPort := strings.ToLower(u.Host)
	reqPort := u.Port()
	if reqHost == "" {
		return "", fmt.Errorf("checkout return URL missing hostname")
	}
	if isProduction && (reqHost == "localhost" || reqHost == "127.0.0.1" || reqHost == "::1") {
		return "", fmt.Errorf("loopback host %q is prohibited in production", reqHost)
	}

	if !matchesApprovedOriginOrDomain(scheme, reqHost, reqHostPort, reqPort, allowedOrigins, approvedDomains, isProduction) {
		return "", fmt.Errorf("checkout return URL host %q does not match approved public origin or property domain", u.Host)
	}

	return u.String(), nil
}

func matchesApprovedOriginOrDomain(scheme, reqHost, reqHostPort, reqPort string, allowedOrigins []string, approvedDomains []string, isProduction bool) bool {
	for _, orig := range allowedOrigins {
		orig = strings.TrimSpace(orig)
		if orig == "" {
			continue
		}
		ou, err := url.Parse(orig)
		if err != nil || !ou.IsAbs() || ou.Host == "" || ou.User != nil {
			continue
		}
		origScheme := strings.ToLower(ou.Scheme)
		if isProduction && origScheme != "https" {
			continue
		}
		if scheme != origScheme {
			continue
		}
		if strings.ToLower(ou.Host) == reqHostPort {
			return true
		}
	}

	isStandardPort := reqPort == "" || (scheme == "https" && reqPort == "443") || (scheme == "http" && reqPort == "80")
	for _, dom := range approvedDomains {
		dom = strings.TrimSpace(strings.ToLower(dom))
		if dom == "" {
			continue
		}
		dom = strings.TrimPrefix(dom, "https://")
		dom = strings.TrimPrefix(dom, "http://")
		dom = strings.TrimSuffix(dom, "/")
		if strings.ContainsAny(dom, "/\\@? ") {
			continue
		}
		// Exact host:port match when domain includes explicit port; otherwise bare host matches standard port only.
		if strings.Contains(dom, ":") {
			if dom == reqHostPort {
				return true
			}
		} else if isStandardPort && dom == reqHost {
			return true
		}
	}
	return false
}
