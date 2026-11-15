package channels

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrAuthFailed                = errors.New("channel provider authentication failed")
	ErrTokenExpired              = errors.New("channel provider access token expired")
	ErrRateLimited               = errors.New("channel provider rate limit exceeded")
	ErrProviderUnavailable       = errors.New("channel provider unavailable")
	ErrInvalidResponse           = errors.New("channel provider returned invalid response")
	ErrRedirectBlocked           = errors.New("channel provider redirect blocked")
	ErrResponseTooLarge          = errors.New("channel provider response exceeded maximum allowed size")
	ErrOperationBlocked          = errors.New("channel provider operation blocked by provider requirements")
	ErrNotImplemented            = errors.New("channel provider operation not implemented in official api")
	ErrWebhookVerificationFailed = errors.New("channel webhook signature or secret verification failed")
	ErrInvalidInput              = errors.New("invalid channel operation input")
)

// ProviderError represents a sanitized, structured channel adapter error.
// It never stores raw provider payloads, credentials, headers, or guest PII.
type ProviderError struct {
	Kind         error
	ProviderCode string
	Operation    string
	HTTPStatus   int
	ErrorCode    string
	RetryAfter   time.Duration
	Message      string
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{}
	if e.ProviderCode != "" {
		parts = append(parts, e.ProviderCode)
	}
	if e.Operation != "" {
		parts = append(parts, e.Operation)
	}
	prefix := strings.Join(parts, ".")
	if prefix == "" {
		prefix = "channel"
	}
	if e.HTTPStatus > 0 {
		return fmt.Sprintf("%s (status=%d, code=%s): %s", prefix, e.HTTPStatus, sanitizeErrorCode(e.ErrorCode), e.Message)
	}
	if e.ErrorCode != "" {
		return fmt.Sprintf("%s (code=%s): %s", prefix, sanitizeErrorCode(e.ErrorCode), e.Message)
	}
	return fmt.Sprintf("%s: %s", prefix, e.Message)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

func newProviderError(kind error, provider, op string, status int, code, msg string) *ProviderError {
	return &ProviderError{
		Kind:         kind,
		ProviderCode: strings.ToLower(strings.TrimSpace(provider)),
		Operation:    strings.TrimSpace(op),
		HTTPStatus:   status,
		ErrorCode:    sanitizeErrorCode(code),
		Message:      strings.TrimSpace(msg),
	}
}

// ExtractErrorCode extracts a compact safe machine code for DB persistence and audit logs.
func ExtractErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var pe *ProviderError
	if errors.As(err, &pe) && pe.ErrorCode != "" {
		return sanitizeErrorCode(pe.ErrorCode)
	}
	switch {
	case errors.Is(err, ErrAuthFailed):
		return "AUTH_FAILED"
	case errors.Is(err, ErrTokenExpired):
		return "TOKEN_EXPIRED"
	case errors.Is(err, ErrRateLimited):
		return "RATE_LIMITED"
	case errors.Is(err, ErrProviderUnavailable):
		return "PROVIDER_UNAVAILABLE"
	case errors.Is(err, ErrInvalidResponse):
		return "INVALID_RESPONSE"
	case errors.Is(err, ErrRedirectBlocked):
		return "REDIRECT_BLOCKED"
	case errors.Is(err, ErrResponseTooLarge):
		return "RESPONSE_TOO_LARGE"
	case errors.Is(err, ErrOperationBlocked):
		return "OPERATION_BLOCKED"
	case errors.Is(err, ErrNotImplemented):
		return "NOT_IMPLEMENTED"
	case errors.Is(err, ErrWebhookVerificationFailed):
		return "WEBHOOK_INVALID"
	case errors.Is(err, ErrInvalidInput):
		return "INVALID_INPUT"
	default:
		return "PROVIDER_ERROR"
	}
}

func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrProviderUnavailable) || errors.Is(err, ErrTokenExpired) {
		return true
	}
	return false
}

func sanitizeErrorCode(code string) string {
	code = strings.TrimSpace(strings.ToUpper(code))
	if code == "" {
		return "PROVIDER_ERROR"
	}
	var b strings.Builder
	for _, r := range code {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
			if b.Len() >= 48 {
				break
			}
		} else if r == ' ' {
			b.WriteByte('_')
			if b.Len() >= 48 {
				break
			}
		}
	}
	if b.Len() == 0 {
		return "PROVIDER_ERROR"
	}
	return b.String()
}
