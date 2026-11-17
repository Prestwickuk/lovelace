package channels

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const MaxWebhookTimestampSkew = 5 * time.Minute

// GenerateNonceUUID creates a cryptographically random RFC 4122 UUIDv4 string for X-Nonce headers.
func GenerateNonceUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand failed while generating channel nonce: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// SHA256Hex returns the lowercase hexadecimal SHA-256 digest of body.
// Empty slice yields e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855.
func SHA256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// EncodeCanonicalQueryRFC3986 encodes query parameters sorted by encoded key and encoded value
// using strict RFC 3986 percent-encoding (%20 for spaces, unreserved A-Z a-z 0-9 - . _ ~).
func EncodeCanonicalQueryRFC3986(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return encodeRFC3986(keys[i]) < encodeRFC3986(keys[j])
	})

	pairs := make([]string, 0, len(values))
	for _, k := range keys {
		ek := encodeRFC3986(k)
		vals := append([]string(nil), values[k]...)
		if len(vals) == 0 {
			pairs = append(pairs, ek+"=")
			continue
		}
		sort.Slice(vals, func(i, j int) bool {
			return encodeRFC3986(vals[i]) < encodeRFC3986(vals[j])
		})
		for _, v := range vals {
			pairs = append(pairs, ek+"="+encodeRFC3986(v))
		}
	}
	return strings.Join(pairs, "&")
}

func encodeRFC3986(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	const upperHex = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&0x0F])
		}
	}
	return b.String()
}

// SmoobuCanonicalString builds the newline-separated canonical string per Smoobu HMAC-SHA256 docs.
func SmoobuCanonicalString(method, reqPath, canonicalQuery, timestamp, nonce, apiKey string, body []byte) string {
	bodyHash := SHA256Hex(body)
	return strings.Join([]string{
		strings.ToUpper(strings.TrimSpace(method)),
		reqPath,
		canonicalQuery,
		timestamp,
		nonce,
		bodyHash,
		apiKey,
	}, "\n")
}

// SignSmoobuRequest returns the Base64-encoded HMAC-SHA256 signature for X-Signature.
func SignSmoobuRequest(method, reqPath, canonicalQuery, timestamp, nonce, apiKey, apiSecret string, body []byte) string {
	canonical := SmoobuCanonicalString(method, reqPath, canonicalQuery, timestamp, nonce, apiKey, body)
	mac := hmac.New(sha256.New, []byte(apiSecret))
	mac.Write([]byte(canonical))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// VerifySmoobuSignature validates an incoming or peer Smoobu HMAC-SHA256 signed request,
// enforcing <= 5m timestamp skew, constant-time HMAC equality, and single-use nonce replay protection.
func VerifySmoobuSignature(
	ctx context.Context,
	method, reqPath, canonicalQuery string,
	headers map[string]string,
	body []byte,
	expectedAPIKey, apiSecret string,
	now time.Time,
	checkNonce NonceChecker,
) error {
	normHeaders := normalizeHeaderMap(headers)
	key := normHeaders["x-api-key"]
	tsStr := normHeaders["x-timestamp"]
	nonce := normHeaders["x-nonce"]
	sigB64 := normHeaders["x-signature"]

	if key == "" || tsStr == "" || nonce == "" || sigB64 == "" {
		return newProviderError(ErrWebhookVerificationFailed, "smoobu", "verify_hmac", 401, "HMAC_HEADERS_MISSING", "missing required Smoobu HMAC headers")
	}
	if !constantTimeEqual(key, expectedAPIKey) {
		return newProviderError(ErrWebhookVerificationFailed, "smoobu", "verify_hmac", 401, "HMAC_API_KEY_MISMATCH", "API key mismatch")
	}

	ts, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		return newProviderError(ErrWebhookVerificationFailed, "smoobu", "verify_hmac", 401, "HMAC_TIMESTAMP_INVALID", "invalid X-Timestamp format")
	}
	skew := now.UTC().Sub(ts.UTC())
	if skew < 0 {
		skew = -skew
	}
	if skew > MaxWebhookTimestampSkew {
		return newProviderError(ErrWebhookVerificationFailed, "smoobu", "verify_hmac", 401, "HMAC_TIMESTAMP_EXPIRED", "request timestamp outside 5 minute tolerance window")
	}

	expectedSig := SignSmoobuRequest(method, reqPath, canonicalQuery, tsStr, nonce, key, apiSecret, body)
	if !constantTimeEqual(sigB64, expectedSig) {
		return newProviderError(ErrWebhookVerificationFailed, "smoobu", "verify_hmac", 401, "HMAC_SIGNATURE_MISMATCH", "HMAC signature mismatch")
	}

	if checkNonce != nil {
		fresh, err := checkNonce(ctx, "smoobu", nonce, ts.UTC().Add(MaxWebhookTimestampSkew*2))
		if err != nil {
			return newProviderError(ErrWebhookVerificationFailed, "smoobu", "verify_hmac", 500, "NONCE_CHECK_FAILED", "nonce verification failed")
		}
		if !fresh {
			return newProviderError(ErrWebhookVerificationFailed, "smoobu", "verify_hmac", 401, "HMAC_NONCE_REPLAY", "replayed X-Nonce rejected")
		}
	}
	return nil
}

// SignChannexWebhook computes the lowercase hex HMAC-SHA256 digest over "{timestamp}.{raw_body}".
func SignChannexWebhook(timestamp string, body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyChannexWebhook verifies Channex x-hmac-timestamp + x-hmac-signature-256 headers.
// Supports multiple comma-separated signatures and dual active/previous secrets for rotation.
func VerifyChannexWebhook(headers map[string]string, body []byte, secrets []string, now time.Time) error {
	normHeaders := normalizeHeaderMap(headers)
	tsRaw := normHeaders["x-hmac-timestamp"]
	sigHeader := normHeaders["x-hmac-signature-256"]
	if tsRaw == "" || sigHeader == "" {
		return newProviderError(ErrWebhookVerificationFailed, "channex", "verify_webhook", 401, "CHANNEX_HMAC_MISSING", "missing x-hmac-timestamp or x-hmac-signature-256")
	}
	unixSec, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil || unixSec <= 0 {
		return newProviderError(ErrWebhookVerificationFailed, "channex", "verify_webhook", 401, "CHANNEX_TIMESTAMP_INVALID", "invalid x-hmac-timestamp")
	}
	ts := time.Unix(unixSec, 0).UTC()
	skew := now.UTC().Sub(ts)
	if skew < 0 {
		skew = -skew
	}
	if skew > MaxWebhookTimestampSkew {
		return newProviderError(ErrWebhookVerificationFailed, "channex", "verify_webhook", 401, "CHANNEX_TIMESTAMP_EXPIRED", "x-hmac-timestamp outside 5 minute tolerance window")
	}

	candidates := make([]string, 0, 4)
	for _, part := range strings.Split(sigHeader, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		p = strings.TrimPrefix(p, "sha256=")
		if p != "" {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return newProviderError(ErrWebhookVerificationFailed, "channex", "verify_webhook", 401, "CHANNEX_HMAC_EMPTY", "empty x-hmac-signature-256 header")
	}

	activeSecrets := nonEmptyUnique(secrets)
	if len(activeSecrets) == 0 {
		return newProviderError(ErrWebhookVerificationFailed, "channex", "verify_webhook", 401, "WEBHOOK_SECRET_NOT_CONFIGURED", "webhook secret not configured")
	}
	for _, secret := range activeSecrets {
		expected := SignChannexWebhook(tsRaw, body, secret)
		for _, candidate := range candidates {
			if constantTimeEqual(candidate, expected) {
				return nil
			}
		}
	}
	return newProviderError(ErrWebhookVerificationFailed, "channex", "verify_webhook", 401, "CHANNEX_HMAC_MISMATCH", "Channex webhook signature verification failed")
}

// VerifySharedSecretWebhook validates a configured static webhook secret (used when a provider does not natively sign webhooks)
// or a Lodgify-style hex HMAC-SHA256 body signature when supplied.
func VerifySharedSecretWebhook(providerCode string, headers map[string]string, body []byte, secrets []string) error {
	activeSecrets := nonEmptyUnique(secrets)
	if len(activeSecrets) == 0 {
		return newProviderError(ErrWebhookVerificationFailed, providerCode, "verify_webhook", 401, "WEBHOOK_SECRET_NOT_CONFIGURED", "webhook secret is not configured for connection")
	}
	normHeaders := normalizeHeaderMap(headers)

	// 1. Direct shared secret headers
	tokenCandidates := []string{
		normHeaders["x-lovelace-webhook-secret"],
		normHeaders["x-webhook-secret"],
	}
	if auth := normHeaders["authorization"]; strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		tokenCandidates = append(tokenCandidates, strings.TrimSpace(auth[len("bearer "):]))
	}
	for _, tok := range tokenCandidates {
		if tok == "" {
			continue
		}
		for _, s := range activeSecrets {
			if constantTimeEqual(tok, s) {
				return nil
			}
		}
	}

	// 2. Optional HMAC body signature headers (e.g. X-Lodgify-Signature / X-Signature-256)
	for _, headerName := range []string{"x-lodgify-signature", "x-signature-256"} {
		if sig := strings.TrimSpace(normHeaders[headerName]); sig != "" {
			clean := strings.ToLower(strings.TrimPrefix(sig, "sha256="))
			for _, s := range activeSecrets {
				mac := hmac.New(sha256.New, []byte(s))
				mac.Write(body)
				expectedHex := hex.EncodeToString(mac.Sum(nil))
				expectedB64 := base64.StdEncoding.EncodeToString(mac.Sum(nil))
				if constantTimeEqual(clean, expectedHex) || constantTimeEqual(sig, expectedB64) {
					return nil
				}
			}
		}
	}

	return newProviderError(ErrWebhookVerificationFailed, providerCode, "verify_webhook", 401, "WEBHOOK_SECRET_MISMATCH", "webhook authentication header missing or invalid")
}

func normalizeHeaderMap(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out
}

func constantTimeEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func nonEmptyUnique(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		t := strings.TrimSpace(it)
		if t == "" {
			continue
		}
		if _, exists := seen[t]; exists {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}
