package auth

import (
	"strings"
	"testing"
)

func TestRecoveryCodesGenerateNormalizeAndHash(t *testing.T) {
	codes, err := GenerateRecoveryCodes(DefaultRecoveryCodeCount)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != DefaultRecoveryCodeCount {
		t.Fatalf("expected %d codes, got %d", DefaultRecoveryCodeCount, len(codes))
	}

	key := []byte("0123456789abcdef0123456789abcdef")
	for _, c := range codes {
		if len(c) != 19 || strings.Count(c, "-") != 3 {
			t.Fatalf("unexpected format %q", c)
		}
		normUpper, ok := NormalizeRecoveryCode(c)
		if !ok {
			t.Fatalf("expected valid code %q", c)
		}
		// Lowercase without hyphens and with spaces normalizes identically.
		messy := "  " + strings.ToLower(strings.ReplaceAll(c, "-", " ")) + "\n"
		normMessy, ok := NormalizeRecoveryCode(messy)
		if !ok || normMessy != normUpper {
			t.Fatalf("expected messy input %q to normalize to %q, got %q (ok=%v)", messy, normUpper, normMessy, ok)
		}

		h1 := HashRecoveryCode(key, "11111111-1111-1111-1111-111111111111", normUpper)
		h2 := HashRecoveryCode(key, "22222222-2222-2222-2222-222222222222", normUpper)
		if h1 == h2 {
			t.Fatal("expected user-bound HMAC digests to differ across user IDs")
		}
		if !MatchRecoveryCodeHash(h1, h1) || MatchRecoveryCodeHash(h1, h2) {
			t.Fatal("unexpected MatchRecoveryCodeHash result")
		}
	}

	// Ambiguous or wrong-length inputs rejected.
	for _, bad := range []string{"", "ABCD-EFGH", "0000-1111-OOOO-IIII", "ABCD-EFGH-JKLM-NPQR-STUV"} {
		if _, ok := NormalizeRecoveryCode(bad); ok {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}
