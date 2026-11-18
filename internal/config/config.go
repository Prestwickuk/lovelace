package config

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tirbach/tir-bach/internal/payments"
)

// Known insecure development secrets that must never be accepted in production.
var insecureDevSecrets = []string{
	"tir_bach_dev_password",
	"tirbach-dev-admin-2026",
	"dev-only-insecure-encryption-key-change-me",
	"tirbach-dev-only-32byte-key-0001",
	"dev-public-bff-secret",
	"whsec_sandbox_dev_only",
	"whsec_replace_me",
	"sk_live_or_test_replace_me",
	"0000000000000000000000000000000000000000000000000000000000000000",
	"00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
}

const defaultTrustedProxyCIDRs = "127.0.0.1/32,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"

// Config holds runtime configuration loaded from environment variables.
type Config struct {
	AppEnv               string
	ListenAddr           string
	DatabaseURL          string
	CookieSecure         bool
	SessionTTL           time.Duration
	SessionIdleTTL       time.Duration
	LoginMaxFailIP       int
	LoginMaxFailAcc      int
	LoginWindow          time.Duration
	TrustProxy             bool
	TrustedProxies         []net.IPNet
	AutoSeed               bool
	BootstrapAdminEmail    string
	BootstrapAdminName     string
	BootstrapAdminPassword string
	EncryptionKey          []byte
	PublicOrigin         string
	PublicAPISecret      string
	GuestPortalEnabled   bool
	StripeSecretKey      string
	StripeWebhookSecret  string
	StripeAPIBaseURL     string
	StripeSuccessURL     string
	StripeCancelURL      string
	StripeSandbox        bool
	EmailFrom            string
	EmailWebhookSecret   string
	SMTPAddr             string
	SMTPUsername         string
	SMTPPassword         string
	RequireEmail         bool
	AttachmentStorageDir string
	MaxAttachmentBytes   int64
}

// IsProduction reports whether the application runs in production mode.
func (c Config) IsProduction() bool {
	return strings.EqualFold(strings.TrimSpace(c.AppEnv), "production")
}

// IsStaging reports whether the application runs in staging mode.
func (c Config) IsStaging() bool {
	return strings.EqualFold(strings.TrimSpace(c.AppEnv), "staging")
}

// AllowedPublicOrigins splits PublicOrigin into normalized scheme://host[:port] strings.
func (c Config) AllowedPublicOrigins() []string {
	var out []string
	for _, part := range strings.Split(c.PublicOrigin, ",") {
		p := strings.TrimRight(strings.TrimSpace(part), "/")
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// IsStripeConfigured reports whether valid environment-appropriate Stripe secret and webhook keys are configured.
func (c Config) IsStripeConfigured() bool {
	return IsValidStripeSecretKey(c.StripeSecretKey, c.IsProduction()) && IsValidStripeWebhookSecret(c.StripeWebhookSecret, c.IsProduction())
}

// ExpectedStripeLiveMode derives the expected Stripe webhook `livemode` boolean
// from the environment and configured Stripe API key. Production always expects true.
func (c Config) ExpectedStripeLiveMode() bool {
	if c.IsProduction() {
		return true
	}
	if live, ok := StripeKeyIsLive(c.StripeSecretKey); ok {
		return live
	}
	return false
}

// IsEmailConfigured reports whether an SMTP transport address is configured.
func (c Config) IsEmailConfigured() bool {
	return strings.TrimSpace(c.SMTPAddr) != ""
}

// Load reads configuration from environment variables and fails closed on insecure production settings.
func Load() (Config, error) {
	appEnv := strings.ToLower(envOrDefault("TIRBACH_ENV", "development"))
	defaultOrigin := "http://127.0.0.1:3000"
	if appEnv == "production" {
		defaultOrigin = ""
	}
	publicOrigin := envOrDefault("TIRBACH_PUBLIC_ORIGIN", defaultOrigin)
	cfg := Config{
		AppEnv:               appEnv,
		ListenAddr:           envOrDefault("TIRBACH_LISTEN_ADDR", "127.0.0.1:8080"),
		DatabaseURL:          strings.TrimSpace(os.Getenv("DATABASE_URL")),
		CookieSecure:         envBool("TIRBACH_COOKIE_SECURE", false),
		SessionTTL:           envDuration("TIRBACH_SESSION_TTL", 12*time.Hour),
		SessionIdleTTL:       envDuration("TIRBACH_SESSION_IDLE_TTL", 2*time.Hour),
		LoginMaxFailIP:       envInt("TIRBACH_LOGIN_MAX_FAIL_IP", 20),
		LoginMaxFailAcc:      envInt("TIRBACH_LOGIN_MAX_FAIL_ACCOUNT", 8),
		LoginWindow:          envDuration("TIRBACH_LOGIN_WINDOW", 15*time.Minute),
		TrustProxy:           envBool("TIRBACH_TRUST_PROXY", false),
		AutoSeed:             envBool("TIRBACH_AUTO_SEED", false),
		PublicOrigin:         strings.TrimRight(publicOrigin, "/"),
		PublicAPISecret:      strings.TrimSpace(os.Getenv("TIRBACH_PUBLIC_API_SECRET")),
		GuestPortalEnabled:   envBool("TIRBACH_GUEST_PORTAL_ENABLED", false),
		StripeSecretKey:      strings.TrimSpace(os.Getenv("TIRBACH_STRIPE_SECRET_KEY")),
		StripeWebhookSecret:  strings.TrimSpace(os.Getenv("TIRBACH_STRIPE_WEBHOOK_SECRET")),
		StripeAPIBaseURL:     strings.TrimRight(envOrDefault("TIRBACH_STRIPE_API_BASE_URL", "https://api.stripe.com"), "/"),
		StripeSuccessURL:     strings.TrimSpace(os.Getenv("TIRBACH_STRIPE_SUCCESS_URL")),
		StripeCancelURL:      strings.TrimSpace(os.Getenv("TIRBACH_STRIPE_CANCEL_URL")),
		StripeSandbox:        envBool("TIRBACH_STRIPE_SANDBOX", false),
		EmailFrom:            envOrDefault("TIRBACH_EMAIL_FROM", "bookings@tirbach.wales"),
		EmailWebhookSecret:   strings.TrimSpace(os.Getenv("TIRBACH_EMAIL_WEBHOOK_SECRET")),
		SMTPAddr:             strings.TrimSpace(os.Getenv("TIRBACH_SMTP_ADDR")),
		SMTPUsername:         strings.TrimSpace(os.Getenv("TIRBACH_SMTP_USERNAME")),
		SMTPPassword:         strings.TrimSpace(os.Getenv("TIRBACH_SMTP_PASSWORD")),
		RequireEmail:         envBool("TIRBACH_REQUIRE_EMAIL", false),
		AttachmentStorageDir: envOrDefault("TIRBACH_ATTACHMENT_DIR", filepath.Join(os.TempDir(), "lovelace-attachments")),
		MaxAttachmentBytes:   envInt64("TIRBACH_MAX_ATTACHMENT_BYTES", 10*1024*1024),
	}
	if !cfg.AutoSeed {
		bootEmail := strings.TrimSpace(os.Getenv("TIRBACH_BOOTSTRAP_ADMIN_EMAIL"))
		bootPass := os.Getenv("TIRBACH_BOOTSTRAP_ADMIN_PASSWORD")
		bootName := strings.TrimSpace(os.Getenv("TIRBACH_BOOTSTRAP_ADMIN_NAME"))
		if bootEmail == "" && bootPass == "" {
			legacyEmail := strings.TrimSpace(os.Getenv("TIRBACH_ADMIN_EMAIL"))
			legacyPass := os.Getenv("TIRBACH_ADMIN_PASSWORD")
			if legacyEmail != "" || legacyPass != "" {
				bootEmail = legacyEmail
				bootPass = legacyPass
				if bootName == "" {
					bootName = strings.TrimSpace(os.Getenv("TIRBACH_ADMIN_NAME"))
				}
			}
		}
		if bootName == "" && (bootEmail != "" || bootPass != "") {
			bootName = "Owner Administrator"
		}
		cfg.BootstrapAdminEmail = bootEmail
		cfg.BootstrapAdminName = bootName
		cfg.BootstrapAdminPassword = bootPass
	}
	if (cfg.BootstrapAdminEmail != "") != (cfg.BootstrapAdminPassword != "") {
		return Config{}, fmt.Errorf("TIRBACH_BOOTSTRAP_ADMIN_EMAIL and TIRBACH_BOOTSTRAP_ADMIN_PASSWORD must be provided together")
	}
	if cfg.BootstrapAdminPassword != "" {
		if utf8.RuneCountInString(cfg.BootstrapAdminPassword) < 12 || len(cfg.BootstrapAdminPassword) > 256 {
			return Config{}, fmt.Errorf("TIRBACH_BOOTSTRAP_ADMIN_PASSWORD must be between 12 characters and 256 bytes")
		}
		if (cfg.IsProduction() || cfg.IsStaging()) && isInsecureSecret(cfg.BootstrapAdminPassword) {
			return Config{}, fmt.Errorf("TIRBACH_BOOTSTRAP_ADMIN_PASSWORD uses a known insecure development value")
		}
	}
	if cfg.MaxAttachmentBytes < 1024 || cfg.MaxAttachmentBytes > 50*1024*1024 {
		return Config{}, fmt.Errorf("TIRBACH_MAX_ATTACHMENT_BYTES must be between 1024 and 52428800 bytes")
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL environment variable is required")
	}

	trustedRaw := strings.TrimSpace(os.Getenv("TIRBACH_TRUSTED_PROXIES"))
	if trustedRaw == "" && cfg.TrustProxy {
		trustedRaw = defaultTrustedProxyCIDRs
	}
	if trustedRaw != "" {
		nets, err := ParseTrustedProxyCIDRs(trustedRaw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid TIRBACH_TRUSTED_PROXIES: %w", err)
		}
		cfg.TrustedProxies = nets
	}

	rawKey := strings.TrimSpace(os.Getenv("TIRBACH_ENCRYPTION_KEY"))
	if rawKey != "" {
		key, err := parseEncryptionKey(rawKey)
		if err != nil {
			return Config{}, fmt.Errorf("invalid TIRBACH_ENCRYPTION_KEY: %w", err)
		}
		cfg.EncryptionKey = key
	} else if !cfg.IsProduction() && !cfg.IsStaging() {
		// Explicit local development/test fallback key for non-portal admin vault;
		// production, staging, and all Guest Portal OTP/CSRF operations fail closed.
		cfg.EncryptionKey = []byte("tirbach-dev-only-32byte-key-0001")
	}

	if cfg.IsStaging() {
		if cfg.StripeSandbox {
			return Config{}, fmt.Errorf("TIRBACH_STRIPE_SANDBOX cannot be enabled in staging")
		}
		if len(cfg.EncryptionKey) != 32 {
			return Config{}, fmt.Errorf("TIRBACH_ENCRYPTION_KEY (32-byte hex or base64) is required in staging")
		}
		if isInsecureSecret(rawKey) || isInsecureEncryptionKeyBytes(cfg.EncryptionKey) {
			return Config{}, fmt.Errorf("TIRBACH_ENCRYPTION_KEY uses a known insecure or low-entropy value")
		}
		if cfg.PublicAPISecret != "" && isInsecureSecret(cfg.PublicAPISecret) {
			return Config{}, fmt.Errorf("TIRBACH_PUBLIC_API_SECRET uses a known insecure development value")
		}
	}

	if cfg.IsProduction() {
		if !cfg.CookieSecure {
			return Config{}, fmt.Errorf("TIRBACH_COOKIE_SECURE must be true in production")
		}
		if cfg.AutoSeed {
			return Config{}, fmt.Errorf("TIRBACH_AUTO_SEED must be false in production")
		}
		if cfg.StripeSandbox {
			return Config{}, fmt.Errorf("TIRBACH_STRIPE_SANDBOX cannot be enabled in production")
		}
		if cfg.TrustProxy && strings.TrimSpace(os.Getenv("TIRBACH_TRUSTED_PROXIES")) == "" {
			return Config{}, fmt.Errorf("TIRBACH_TRUSTED_PROXIES must be explicitly configured when TIRBACH_TRUST_PROXY=true in production")
		}
		if len(cfg.EncryptionKey) != 32 {
			return Config{}, fmt.Errorf("TIRBACH_ENCRYPTION_KEY (32-byte hex or base64) is required in production")
		}
		if isInsecureSecret(rawKey) || isInsecureEncryptionKeyBytes(cfg.EncryptionKey) {
			return Config{}, fmt.Errorf("TIRBACH_ENCRYPTION_KEY uses a known insecure or low-entropy value")
		}
		if cfg.PublicAPISecret != "" && isInsecureSecret(cfg.PublicAPISecret) {
			return Config{}, fmt.Errorf("TIRBACH_PUBLIC_API_SECRET uses a known insecure development value")
		}
		for _, bad := range insecureDevSecrets {
			if strings.Contains(cfg.DatabaseURL, bad) {
				return Config{}, fmt.Errorf("DATABASE_URL contains a known insecure development password")
			}
		}
		if cfg.PublicOrigin != "" {
			if err := ValidatePublicOrigins(cfg.AllowedPublicOrigins(), true); err != nil {
				return Config{}, fmt.Errorf("invalid TIRBACH_PUBLIC_ORIGIN: %w", err)
			}
		}
	}

	if cfg.StripeAPIBaseURL != "" {
		if err := validateStripeAPIBaseURL(cfg.StripeAPIBaseURL, cfg.IsProduction()); err != nil {
			return Config{}, fmt.Errorf("invalid TIRBACH_STRIPE_API_BASE_URL: %w", err)
		}
	}

	// Validate Stripe configuration whenever keys are partially or fully supplied.
	if err := cfg.ValidateStripeKeys(); err != nil {
		return Config{}, err
	}
	if cfg.StripeSuccessURL != "" {
		if _, err := payments.ValidateCheckoutReturnURL(cfg.StripeSuccessURL, cfg.AllowedPublicOrigins(), nil, cfg.IsProduction()); err != nil {
			return Config{}, fmt.Errorf("invalid TIRBACH_STRIPE_SUCCESS_URL: %w", err)
		}
	}
	if cfg.StripeCancelURL != "" {
		if _, err := payments.ValidateCheckoutReturnURL(cfg.StripeCancelURL, cfg.AllowedPublicOrigins(), nil, cfg.IsProduction()); err != nil {
			return Config{}, fmt.Errorf("invalid TIRBACH_STRIPE_CANCEL_URL: %w", err)
		}
	}

	return cfg, nil
}

// ValidateStripeKeys validates Stripe secret and webhook secret pairing, prefix mode, and syntax.
func (c Config) ValidateStripeKeys() error {
	hasKey := c.StripeSecretKey != ""
	hasWebhook := c.StripeWebhookSecret != ""
	if !hasKey && !hasWebhook {
		return nil
	}
	if hasKey {
		live, ok := StripeKeyIsLive(c.StripeSecretKey)
		if !ok {
			return fmt.Errorf("invalid TIRBACH_STRIPE_SECRET_KEY format or placeholder value")
		}
		if c.IsProduction() && !live {
			return fmt.Errorf("Stripe test-mode credentials (sk_test_/rk_test_) are prohibited when TIRBACH_ENV=production; supply sk_live_ or suitably scoped rk_live_ keys")
		}
	}
	if hasWebhook && !IsValidStripeWebhookSecret(c.StripeWebhookSecret, c.IsProduction()) {
		return fmt.Errorf("invalid TIRBACH_STRIPE_WEBHOOK_SECRET format or placeholder value")
	}
	if c.IsProduction() {
		if hasKey && !hasWebhook {
			return fmt.Errorf("TIRBACH_STRIPE_WEBHOOK_SECRET is required whenever TIRBACH_STRIPE_SECRET_KEY is configured in production")
		}
		if hasWebhook && !hasKey {
			return fmt.Errorf("TIRBACH_STRIPE_SECRET_KEY is required whenever TIRBACH_STRIPE_WEBHOOK_SECRET is configured in production")
		}
	} else if !c.StripeSandbox && ((hasKey && !hasWebhook) || (hasWebhook && !hasKey)) {
		return fmt.Errorf("both TIRBACH_STRIPE_SECRET_KEY and TIRBACH_STRIPE_WEBHOOK_SECRET are required together when Stripe is configured")
	}
	return nil
}

// StripeKeyIsLive validates a Stripe secret/restricted API key and reports whether it is a live-mode key.
func StripeKeyIsLive(k string) (live bool, ok bool) {
	k = strings.TrimSpace(k)
	if len(k) < 16 || strings.ContainsAny(k, " \t\r\n") || isInsecureSecret(k) {
		return false, false
	}
	lower := strings.ToLower(k)
	for _, bad := range []string{"replace_me", "placeholder", "changeme", "dummy"} {
		if strings.Contains(lower, bad) {
			return false, false
		}
	}
	switch {
	case strings.HasPrefix(k, "sk_live_"), strings.HasPrefix(k, "rk_live_"):
		live = true
	case strings.HasPrefix(k, "sk_test_"), strings.HasPrefix(k, "rk_test_"):
		live = false
	default:
		return false, false
	}
	suffix := k[8:]
	if len(suffix) < 8 {
		return false, false
	}
	for i := 0; i < len(suffix); i++ {
		c := suffix[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
		if !isAlphaNum {
			return false, false
		}
	}
	return live, true
}

// IsValidStripeSecretKey checks Stripe secret/restricted key syntax, rejects placeholders,
// and in production rejects test-mode keys (`sk_test_`, `rk_test_`).
func IsValidStripeSecretKey(k string, isProduction bool) bool {
	live, ok := StripeKeyIsLive(k)
	if !ok {
		return false
	}
	if isProduction && !live {
		return false
	}
	return true
}

// IsValidStripeWebhookSecret checks Stripe webhook signing secret syntax and rejects placeholders.
func IsValidStripeWebhookSecret(s string, isProduction bool) bool {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	if len(s) < 12 || strings.Contains(lower, "replace_me") || lower == "whsec_replace_me" {
		return false
	}
	if isProduction && (isInsecureSecret(s) || strings.Contains(lower, "sandbox")) {
		return false
	}
	return strings.HasPrefix(s, "whsec_")
}

// ValidatePublicOrigins ensures all configured origins are clean scheme://host[:port] URLs and HTTPS in production.
func ValidatePublicOrigins(origins []string, isProduction bool) error {
	if len(origins) == 0 {
		return fmt.Errorf("at least one public origin is required")
	}
	for _, raw := range origins {
		u, err := url.Parse(raw)
		if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil {
			return fmt.Errorf("malformed public origin %q", raw)
		}
		if u.Path != "" && u.Path != "/" {
			return fmt.Errorf("public origin %q must not include a path", raw)
		}
		scheme := strings.ToLower(u.Scheme)
		if isProduction && scheme != "https" {
			return fmt.Errorf("public origin %q must use https in production", raw)
		}
		if scheme != "http" && scheme != "https" {
			return fmt.Errorf("unsupported public origin scheme in %q", raw)
		}
		host := strings.ToLower(u.Hostname())
		if isProduction && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
			return fmt.Errorf("loopback public origin %q is prohibited in production", raw)
		}
	}
	return nil
}

// ParseTrustedProxyCIDRs parses comma-separated IP addresses or CIDRs.
func ParseTrustedProxyCIDRs(raw string) ([]net.IPNet, error) {
	var nets []net.IPNet
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if strings.Contains(p, "/") {
			_, ipNet, err := net.ParseCIDR(p)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %w", p, err)
			}
			nets = append(nets, *ipNet)
			continue
		}
		ip := net.ParseIP(p)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP %q", p)
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		nets = append(nets, net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return nets, nil
}

func parseEncryptionKey(raw string) ([]byte, error) {
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("must decode to exactly 32 bytes (64 hex chars or 44 base64 chars)")
}

func isInsecureEncryptionKeyBytes(key []byte) bool {
	if len(key) != 32 {
		return true
	}
	if bytes.Equal(key, []byte("tirbach-dev-only-32byte-key-0001")) {
		return true
	}
	if devHex, err := hex.DecodeString("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"); err == nil && bytes.Equal(key, devHex) {
		return true
	}
	var seen [256]bool
	distinct := 0
	for _, b := range key {
		if !seen[b] {
			seen[b] = true
			distinct++
		}
	}
	return distinct < 8
}

func validateStripeAPIBaseURL(raw string, isProduction bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must be a valid absolute URL without userinfo, query, or fragment")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if isProduction {
		if scheme != "https" {
			return fmt.Errorf("must use https in production")
		}
		if strings.ToLower(u.Hostname()) != "api.stripe.com" {
			return fmt.Errorf("must point to api.stripe.com in production")
		}
	}
	return nil
}

func isInsecureSecret(v string) bool {
	lower := strings.ToLower(strings.TrimSpace(v))
	for _, bad := range insecureDevSecrets {
		if lower == strings.ToLower(bad) {
			return true
		}
	}
	return false
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	return v == "1" || v == "true" || v == "yes"
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envInt64(key string, fallback int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
