package config

import (
	"encoding/base64"
	"testing"
)

func TestProductionRejectsEncodedDevKeyAndMissingTrustedProxies(t *testing.T) {
	t.Setenv("TIRBACH_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://lovelace_app:strong_prod_pw_9988@db:5432/tir_bach?sslmode=require")
	t.Setenv("TIRBACH_COOKIE_SECURE", "true")
	t.Setenv("TIRBACH_AUTO_SEED", "false")
	t.Setenv("TIRBACH_TRUST_PROXY", "false")

	// Base64-encoded dev fallback key must still be rejected after decoding!
	b64DevKey := base64.StdEncoding.EncodeToString([]byte("tirbach-dev-only-32byte-key-0001"))
	t.Setenv("TIRBACH_ENCRYPTION_KEY", b64DevKey)
	if _, err := Load(); err == nil {
		t.Fatal("expected base64-encoded dev encryption key to be rejected in production")
	}

	// Low-entropy repeated-byte key must be rejected in production.
	t.Setenv("TIRBACH_ENCRYPTION_KEY", "abababababababababababababababababababababababababababababababab")
	if _, err := Load(); err == nil {
		t.Fatal("expected low-entropy encryption key to be rejected in production")
	}

	// Valid 32-byte key succeeds when TIRBACH_TRUST_PROXY=false...
	t.Setenv("TIRBACH_ENCRYPTION_KEY", "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90")
	if _, err := Load(); err != nil {
		t.Fatalf("expected valid production config to load: %v", err)
	}

	// ...and fails closed if TIRBACH_TRUST_PROXY=true without explicit TIRBACH_TRUSTED_PROXIES!
	t.Setenv("TIRBACH_TRUST_PROXY", "true")
	t.Setenv("TIRBACH_TRUSTED_PROXIES", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected TIRBACH_TRUST_PROXY=true without TIRBACH_TRUSTED_PROXIES to fail in production")
	}

	// Non-Stripe API base URL in production must fail closed.
	t.Setenv("TIRBACH_TRUST_PROXY", "false")
	t.Setenv("TIRBACH_STRIPE_API_BASE_URL", "https://attacker.example.com")
	if _, err := Load(); err == nil {
		t.Fatal("expected untrusted TIRBACH_STRIPE_API_BASE_URL to fail in production")
	}
}
