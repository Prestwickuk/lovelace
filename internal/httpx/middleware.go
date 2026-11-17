package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/service"
)

const (
	SessionCookieName = "tirbach_session"
	MaxJSONBodyBytes  = 1 << 20 // 1 MiB
	MaxFormBodyBytes  = 1 << 20 // 1 MiB
)

type ctxKey int

const (
	ctxKeyMeta ctxKey = iota
	ctxKeyActor
)

// RequestMeta holds sanitized per-request transport metadata.
type RequestMeta struct {
	RequestID string
	IPAddress string
	UserAgent string
}

// MetaFromContext returns request metadata attached by middleware.
func MetaFromContext(ctx context.Context) RequestMeta {
	if m, ok := ctx.Value(ctxKeyMeta).(RequestMeta); ok {
		return m
	}
	return RequestMeta{}
}

// ActorFromContext returns the authenticated Actor if present.
func ActorFromContext(ctx context.Context) (service.Actor, bool) {
	a, ok := ctx.Value(ctxKeyActor).(service.Actor)
	return a, ok
}

// SecurityHeaders applies strict OWASP browser and API hardening headers without trusting X-Forwarded-* headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return SecurityHeadersWithConfig(config.Config{}, next)
}

// SecurityHeadersWithConfig applies strict OWASP browser and API hardening headers,
// trusting X-Forwarded-Proto for HSTS only when the immediate peer is a configured trusted proxy.
func SecurityHeadersWithConfig(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		isHTTPS := r.TLS != nil || (IsTrustedProxyPeer(r, cfg) && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https"))
		if isHTTPS {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// IsTrustedProxyPeer reports whether the immediate TCP peer belongs to cfg.TrustedProxies when cfg.TrustProxy is enabled.
func IsTrustedProxyPeer(r *http.Request, cfg config.Config) bool {
	if !cfg.TrustProxy {
		return false
	}
	remoteIP := parseRemoteIP(r.RemoteAddr)
	return remoteIP != nil && isIPInNets(remoteIP, cfg.TrustedProxies)
}

// IsSameOriginBrowserMutation guards pre-session endpoints (such as POST /login and POST /api/v1/auth/login)
// against cross-site Login CSRF when browser Origin, Referer, or Sec-Fetch-Site headers are present.
func IsSameOriginBrowserMutation(r *http.Request, cfg config.Config) bool {
	if sfs := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); sfs == "cross-site" {
		return false
	}
	expectedHost := strings.TrimSpace(r.Host)
	if IsTrustedProxyPeer(r, cfg) {
		if xfh := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); xfh != "" {
			if first, _, ok := strings.Cut(xfh, ","); ok {
				expectedHost = strings.TrimSpace(first)
			} else {
				expectedHost = xfh
			}
		}
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		if strings.EqualFold(origin, "null") {
			return false
		}
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || expectedHost == "" {
			return false
		}
		return strings.EqualFold(u.Host, expectedHost)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		ru, err := url.Parse(referer)
		if err != nil || ru.Host == "" || expectedHost == "" {
			return false
		}
		return strings.EqualFold(ru.Host, expectedHost)
	}
	return true
}

// RequestContext attaches request ID, sanitized client IP, and User-Agent to context.
func RequestContext(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := uuid.NewString()
		w.Header().Set("X-Request-ID", reqID)
		meta := RequestMeta{
			RequestID: reqID,
			IPAddress: ExtractClientIP(r, cfg.TrustProxy, cfg.TrustedProxies),
			UserAgent: truncate(r.UserAgent(), 256),
		}
		ctx := context.WithValue(r.Context(), ctxKeyMeta, meta)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WithSessionAuth resolves a valid session cookie into context if present.
func WithSessionAuth(svc *service.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookieName)
		if err == nil && c.Value != "" {
			if actor, _, err := svc.AuthenticateSession(r.Context(), c.Value); err == nil {
				meta := MetaFromContext(r.Context())
				actor.RequestID = meta.RequestID
				actor.IPAddress = meta.IPAddress
				actor.UserAgent = meta.UserAgent
				ctx := context.WithValue(r.Context(), ctxKeyActor, actor)
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// SetSessionCookie writes the hardened HttpOnly session cookie.
func SetSessionCookie(w http.ResponseWriter, cfg config.Config, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(cfg.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie expires the session cookie immediately.
func ClearSessionCookie(w http.ResponseWriter, cfg config.Config) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// VerifyCSRFToken validates a submitted CSRF token against the actor's session CSRF token.
func VerifyCSRFToken(actor service.Actor, submitted string) bool {
	return auth.ConstantTimeEqual(actor.CSRFToken, strings.TrimSpace(submitted))
}

// ExtractClientIP determines the client's IP address using strict right-to-left proxy traversal,
// honouring X-Forwarded-For / X-Real-IP ONLY when the immediate peer belongs to trustedProxies.
func ExtractClientIP(r *http.Request, trustProxy bool, trustedProxies []net.IPNet) string {
	remoteIP := parseRemoteIP(r.RemoteAddr)
	if trustProxy && remoteIP != nil && isIPInNets(remoteIP, trustedProxies) {
		if xffVals := r.Header.Values("X-Forwarded-For"); len(xffVals) > 0 {
			xff := strings.Join(xffVals, ",")
			parts := strings.Split(xff, ",")
			for i := len(parts) - 1; i >= 0; i-- {
				candidate := parseNormalizedIP(parts[i])
				if candidate == nil {
					// Malformed hop in X-Forwarded-For: fail closed to immediate peer IP without falling through to X-Real-IP.
					return remoteIP.String()
				}
				if !isIPInNets(candidate, trustedProxies) {
					return candidate.String()
				}
			}
		} else if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			if ip := parseNormalizedIP(xri); ip != nil && !isIPInNets(ip, trustedProxies) {
				return ip.String()
			}
		}
	}
	if remoteIP != nil {
		return remoteIP.String()
	}
	return r.RemoteAddr
}

// ResolveForwardedCustomerIP extracts an authenticated BFF customer IP header only when the
// caller was already authenticated as the trusted Next.js BFF (via X-Lovelace-Public-Key).
func ResolveForwardedCustomerIP(r *http.Request, bffAuthenticated bool, fallbackIP string) string {
	if bffAuthenticated {
		if raw := strings.TrimSpace(r.Header.Get("X-Lovelace-Client-IP")); raw != "" {
			if ip := parseNormalizedIP(raw); ip != nil {
				return ip.String()
			}
		}
	}
	return fallbackIP
}

// RateLimitKeyForIP normalizes an IP string for rate-limiting, grouping non-loopback IPv6
// addresses by /64 subnet so an attacker with a /64 prefix cannot rotate /128 addresses.
func RateLimitKeyForIP(raw string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	addr = addr.Unmap()
	if addr.Is6() && !addr.IsLoopback() {
		if p, err := addr.Prefix(64); err == nil {
			return p.Masked().String()
		}
	}
	return addr.String()
}

func parseNormalizedIP(raw string) net.IP {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	if addr, err := netip.ParseAddr(s); err == nil {
		unmapped := addr.Unmap()
		return net.IP(unmapped.AsSlice())
	}
	return net.ParseIP(s)
}

func parseRemoteIP(remoteAddr string) net.IP {
	raw := strings.TrimSpace(remoteAddr)
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		// Handle unbracketed IPv4-mapped IPv6 host:port such as ::ffff:10.0.0.5:44321
		if strings.HasPrefix(strings.ToLower(raw), "::ffff:") {
			rest := raw[7:]
			if h2, _, err2 := net.SplitHostPort(rest); err2 == nil {
				return parseNormalizedIP(h2)
			}
		}
		host = raw
	}
	return parseNormalizedIP(host)
}

func isIPInNets(ip net.IP, nets []net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
