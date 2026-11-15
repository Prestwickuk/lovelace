package channels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// GetLiveAdapter constructs a configured LiveAdapter for the given provider code.
func GetLiveAdapter(providerCode string, cfg AdapterConfig) (LiveAdapter, bool) {
	code := strings.ToLower(strings.TrimSpace(providerCode))
	switch code {
	case "beds24":
		return NewBeds24Adapter(cfg), true
	case "smoobu":
		return NewSmoobuAdapter(cfg), true
	case "lodgify":
		return NewLodgifyAdapter(cfg), true
	case "channex":
		return NewChannexAdapter(cfg), true
	default:
		return nil, false
	}
}

// AdapterRegistry returns live adapters for every registered provider.
func AdapterRegistry(cfg AdapterConfig) map[string]LiveAdapter {
	return map[string]LiveAdapter{
		"beds24":  NewBeds24Adapter(cfg),
		"smoobu":  NewSmoobuAdapter(cfg),
		"lodgify": NewLodgifyAdapter(cfg),
		"channex": NewChannexAdapter(cfg),
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func decodeJSONStrict(provider, op string, body []byte, dst any) error {
	if len(body) == 0 {
		return newProviderError(ErrInvalidResponse, provider, op, 0, "EMPTY_RESPONSE", "provider returned empty response body")
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return newProviderError(ErrInvalidResponse, provider, op, 0, "INVALID_JSON", "provider returned malformed JSON response")
	}
	return nil
}

func computePayloadDigest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func capabilitySupportedNames(items []CapabilityItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.Status == CapabilitySupported {
			out = append(out, it.Name)
		}
	}
	return out
}

func legacyAuthorizeError(provider string) (string, error) {
	return "", newProviderError(ErrNotImplemented, provider, "authorize_url", 0, "NO_OAUTH_REDIRECT", "provider uses API/refresh-token credentials rather than browser OAuth redirect")
}

func legacyExchangeError(provider string) (map[string]string, error) {
	return nil, newProviderError(ErrNotImplemented, provider, "exchange_code", 0, "NO_OAUTH_CODE", "provider uses API/refresh-token credentials rather than OAuth authorization code")
}

func normalizeReservationStatus(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "cancelled", "canceled", "void", "rejected", "deleted", "black":
		return "CANCELLED"
	case "request", "inquiry", "pending", "tentative", "open":
		return "PENDING"
	case "confirmed", "new", "modified", "checked_in", "checked_out", "booked", "reserved":
		return "CONFIRMED"
	default:
		return "CONFIRMED"
	}
}

func normalizeSourceChannel(raw, providerFallback string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case s == "":
		return providerFallback
	case strings.Contains(s, "airbnb"):
		return "airbnb"
	case strings.Contains(s, "booking"):
		return "booking_com"
	case strings.Contains(s, "vrbo") || strings.Contains(s, "homeaway") || strings.Contains(s, "abritel"):
		return "vrbo"
	case strings.Contains(s, "expedia"):
		return "expedia"
	case strings.Contains(s, "tripadvisor"):
		return "tripadvisor"
	case strings.Contains(s, "holidu"):
		return "holidu"
	case strings.Contains(s, "beds24"):
		return "beds24"
	case strings.Contains(s, "smoobu"):
		return "smoobu"
	case strings.Contains(s, "lodgify"):
		return "lodgify"
	case strings.Contains(s, "channex"):
		return "channex"
	default:
		cleaned := strings.ReplaceAll(strings.ReplaceAll(s, " ", "_"), "-", "_")
		if len(cleaned) > 32 {
			cleaned = cleaned[:32]
		}
		return cleaned
	}
}

func parseFlexibleTimestamp(raw string) *time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}

func parseDateOnlyString(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 10 {
		if _, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return s[:10]
		}
	}
	return ""
}

func flexibleID(v json.RawMessage) string {
	s := strings.TrimSpace(string(v))
	if s == "" || s == "null" {
		return ""
	}
	if strings.HasPrefix(s, "\"") {
		var out string
		if err := json.Unmarshal(v, &out); err == nil {
			return strings.TrimSpace(out)
		}
	}
	return strings.Trim(s, "\"")
}

func flexibleMoneyMinor(raw json.RawMessage, currency string) int64 {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0
	}
	if strings.HasPrefix(s, "\"") {
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			s = strings.TrimSpace(str)
		}
	}
	if s == "" {
		return 0
	}
	if m, err := ParseDecimalToMinor(s, currency); err == nil {
		return m
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if m, err2 := ParseDecimalToMinor(fmt.Sprintf("%.4f", f), currency); err2 == nil {
			return m
		}
	}
	return 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func requireOperationContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
