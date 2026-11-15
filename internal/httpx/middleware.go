package httpx

import (
	"context"
	"net"
	"net/http"
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

// SecurityHeaders applies strict OWASP browser and API hardening headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
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
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			for i := len(parts) - 1; i >= 0; i-- {
				candidate := net.ParseIP(strings.TrimSpace(parts[i]))
				if candidate == nil {
					break
				}
				if !isIPInNets(candidate, trustedProxies) {
					return candidate.String()
				}
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			if ip := net.ParseIP(xri); ip != nil && !isIPInNets(ip, trustedProxies) {
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
			if ip := net.ParseIP(raw); ip != nil {
				return ip.String()
			}
		}
	}
	return fallbackIP
}

func parseRemoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	return net.ParseIP(host)
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
