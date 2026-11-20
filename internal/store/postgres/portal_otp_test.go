package postgres_test

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

func mustRandom32Bytes(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal("failed to read CSPRNG test key bytes")
	}
	return key
}

func TestPortalOTPHMAC_KeyIsolationNormalizationAndDomainSeparation(t *testing.T) {
	keyA := mustRandom32Bytes(t)
	keyB := mustRandom32Bytes(t)
	chID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuidv7: %v", err)
	}
	otherChID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuidv7: %v", err)
	}

	digestA, err := postgres.HashPortalOTP(keyA, chID, "nia.portal@example.com", "482910")
	if err != nil {
		t.Fatalf("HashPortalOTP(keyA) failed: %v", err)
	}
	digestB, err := postgres.HashPortalOTP(keyB, chID, "nia.portal@example.com", "482910")
	if err != nil {
		t.Fatalf("HashPortalOTP(keyB) failed: %v", err)
	}
	if digestA == digestB {
		t.Fatal("expected distinct HMAC digests under independent server keys")
	}

	// Normalization determinism for the same key + challenge.
	digestNormalized, err := postgres.HashPortalOTP(keyA, chID, "  Nia.Portal@Example.Com \n", "\t482910 ")
	if err != nil {
		t.Fatalf("HashPortalOTP normalized input failed: %v", err)
	}
	if digestNormalized != digestA {
		t.Fatal("expected normalized email and whitespace-trimmed OTP to produce identical HMAC digest")
	}

	// Context binding: different challenge ID or different email produces a different digest and fails verification.
	digestOtherChallenge, err := postgres.HashPortalOTP(keyA, otherChID, "nia.portal@example.com", "482910")
	if err != nil {
		t.Fatalf("HashPortalOTP(otherChID) failed: %v", err)
	}
	if digestOtherChallenge == digestA {
		t.Fatal("expected different challenge ID to produce a distinct HMAC digest")
	}
	digestOtherEmail, err := postgres.HashPortalOTP(keyA, chID, "other.guest@example.com", "482910")
	if err != nil {
		t.Fatalf("HashPortalOTP(otherEmail) failed: %v", err)
	}
	if digestOtherEmail == digestA {
		t.Fatal("expected different email address to produce a distinct HMAC digest")
	}

	ch := domain.GuestPortalChallenge{
		ID:              chID,
		NormalizedEmail: "nia.portal@example.com",
		Purpose:         domain.PortalChallengeLogin,
		OTPHash:         digestA,
		OTPHashAlg:      domain.PortalOTPHashAlgHMACSHA256V1,
	}

	// Correct key + challenge + email + normalized OTP verifies.
	if ok, err := postgres.VerifyChallengeProof(keyA, ch, " 482910 ", "", ""); err != nil || !ok {
		t.Fatalf("expected VerifyChallengeProof with matching key to succeed (ok=%v err=%v)", ok, err)
	}
	// Wrong server key fails.
	if ok, err := postgres.VerifyChallengeProof(keyB, ch, "482910", "", ""); err != nil || ok {
		t.Fatalf("expected VerifyChallengeProof with wrong server key to fail (ok=%v err=%v)", ok, err)
	}
	// Wrong challenge ID fails.
	chWrongID := ch
	chWrongID.ID = otherChID
	if ok, err := postgres.VerifyChallengeProof(keyA, chWrongID, "482910", "", ""); err != nil || ok {
		t.Fatalf("expected VerifyChallengeProof with mismatched challenge ID to fail (ok=%v err=%v)", ok, err)
	}
	// Wrong email fails.
	chWrongEmail := ch
	chWrongEmail.NormalizedEmail = "other.guest@example.com"
	if ok, err := postgres.VerifyChallengeProof(keyA, chWrongEmail, "482910", "", ""); err != nil || ok {
		t.Fatalf("expected VerifyChallengeProof with mismatched email to fail (ok=%v err=%v)", ok, err)
	}
	// Wrong OTP fails.
	if ok, err := postgres.VerifyChallengeProof(keyA, ch, "482911", "", ""); err != nil || ok {
		t.Fatalf("expected VerifyChallengeProof with wrong OTP to fail (ok=%v err=%v)", ok, err)
	}

	// Domain separation between OTP HMAC and CSRF HMAC derivation.
	csrfPlain, csrfHash, err := postgres.DerivePortalCSRFToken(keyA, chID, digestA)
	if err != nil {
		t.Fatalf("DerivePortalCSRFToken failed: %v", err)
	}
	if csrfPlain == digestA || csrfHash == digestA {
		t.Fatal("expected CSRF token derivation domain to be disjoint from OTP HMAC domain")
	}

	// Magic-link + browser-nonce verification remains intact and rejects cross-browser calls.
	magicPlain, err := postgres.GeneratePortalRandomHex(32)
	if err != nil {
		t.Fatalf("GeneratePortalRandomHex: %v", err)
	}
	noncePlain, err := postgres.GeneratePortalRandomHex(32)
	if err != nil {
		t.Fatalf("GeneratePortalRandomHex: %v", err)
	}
	chMagic := ch
	chMagic.TokenHash = postgres.HashPortalValue(magicPlain)
	chMagic.BrowserNonceHash = postgres.HashPortalValue(noncePlain)
	if ok, err := postgres.VerifyChallengeProof(nil, chMagic, "", magicPlain, noncePlain); err != nil || !ok {
		t.Fatalf("expected browser-bound magic link to verify cleanly (ok=%v err=%v)", ok, err)
	}
	if ok, err := postgres.VerifyChallengeProof(nil, chMagic, "", magicPlain, "wrong-browser-nonce"); err != nil || ok {
		t.Fatalf("expected mismatched browser nonce to fail magic-link verification (ok=%v err=%v)", ok, err)
	}

	// Fail closed when safe HMAC key is unavailable (nil, empty, short, overlong, all-zero, or known dev keys).
	devHexKey, _ := hex.DecodeString("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	overlongKey := make([]byte, 64)
	overlongKey[0] = 0x42
	for _, badKey := range [][]byte{
		nil,
		{},
		make([]byte, 16),
		make([]byte, 31),
		overlongKey,
		make([]byte, 32), // all-zero 32-byte key
		[]byte("lovelace-dev-only-32byte-key-001"),
		devHexKey,
	} {
		if _, err := postgres.HashPortalOTP(badKey, chID, "nia.portal@example.com", "482910"); !errors.Is(err, postgres.ErrPortalOTPKeyInvalid) {
			t.Fatalf("expected ErrPortalOTPKeyInvalid on unsafe key (len=%d), got %v", len(badKey), err)
		}
		if ok, err := postgres.VerifyChallengeProof(badKey, ch, "482910", "", ""); ok || !errors.Is(err, postgres.ErrPortalOTPKeyInvalid) {
			t.Fatalf("expected VerifyChallengeProof to fail closed with ErrPortalOTPKeyInvalid on unsafe key (len=%d), got ok=%v err=%v", len(badKey), ok, err)
		}
		if _, _, err := postgres.DerivePortalCSRFToken(badKey, chID, digestA); !errors.Is(err, postgres.ErrPortalCSRFKeyInvalid) {
			t.Fatalf("expected ErrPortalCSRFKeyInvalid on unsafe CSRF key (len=%d), got %v", len(badKey), err)
		}
	}
}

func TestPortalOTPHMAC_CannotValidateOrBruteForceWithoutHMACKey(t *testing.T) {
	const secretOTP = "739104"
	chID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuidv7: %v", err)
	}
	const normEmail = "offline.target@example.com"

	// Mint the stored challenge digest using a server-held key that is not retained in the DB row.
	var storedChallenge domain.GuestPortalChallenge
	{
		serverKey := mustRandom32Bytes(t)
		otpDigest, err := postgres.HashPortalOTP(serverKey, chID, normEmail, secretOTP)
		if err != nil {
			t.Fatalf("HashPortalOTP failed: %v", err)
		}
		storedChallenge = domain.GuestPortalChallenge{
			ID:              chID,
			NormalizedEmail: normEmail,
			Purpose:         domain.PortalChallengeLogin,
			OTPHash:         otpDigest,
			OTPHashAlg:      domain.PortalOTPHashAlgHMACSHA256V1,
		}
	}

	// 1. Validation without the server HMAC key (or using keys derived from DB-visible fields) fails even for the exact OTP.
	idDerivedKey := sha256.Sum256([]byte(storedChallenge.ID.String()))
	emailDerivedKey := sha256.Sum256([]byte(storedChallenge.NormalizedEmail))
	attackerRandomKey := mustRandom32Bytes(t)

	for _, candidateKey := range [][]byte{
		nil,
		make([]byte, 32),
		idDerivedKey[:],
		emailDerivedKey[:],
		attackerRandomKey,
	} {
		ok, _ := postgres.VerifyChallengeProof(candidateKey, storedChallenge, secretOTP, "", "")
		if ok {
			t.Fatal("CRITICAL: OTP verified without the server-held HMAC key")
		}
	}

	// 2. Offline enumeration across all 1,000,000 6-digit codes using only the DB-visible fields
	// (storedChallenge.ID, storedChallenge.NormalizedEmail, storedChallenge.OTPHash) recovers nothing.
	storedBytes, err := hex.DecodeString(storedChallenge.OTPHash)
	if err != nil || len(storedBytes) != sha256.Size {
		t.Fatal("stored OTPHash must decode to 32 bytes")
	}

	legacyPrefix := "lovelace-portal-otp:" + storedChallenge.ID.String() + ":" + storedChallenge.NormalizedEmail + ":"
	v2Prefix := fmt.Sprintf("lovelace-portal-otp-hmac-v2|%s|%d:%s|6:", storedChallenge.ID.String(), len(storedChallenge.NormalizedEmail), storedChallenge.NormalizedEmail)

	for codeInt := 0; codeInt < 1_000_000; codeInt++ {
		candidateCode := fmt.Sprintf("%06d", codeInt)
		legacySum := sha256.Sum256([]byte(legacyPrefix + candidateCode))
		if hmac.Equal(legacySum[:], storedBytes) {
			t.Fatal("CRITICAL: stored OTP digest matched unkeyed legacy SHA-256 candidate during offline enumeration")
		}
		unkeyedV2Sum := sha256.Sum256([]byte(v2Prefix + candidateCode))
		if hmac.Equal(unkeyedV2Sum[:], storedBytes) {
			t.Fatal("CRITICAL: stored OTP digest matched unkeyed v2 SHA-256 candidate during offline enumeration")
		}
	}

	// 3. Zero compatibility path for pre-remediation unkeyed SHA-256 digests: even if a legacy unkeyed
	// digest were present in memory, VerifyChallengeProof rejects both INVALIDATED_UNKEYED and forged alg tags.
	verifyKey := mustRandom32Bytes(t)
	legacyDigest := sha256.Sum256([]byte(legacyPrefix + secretOTP))
	legacyHex := hex.EncodeToString(legacyDigest[:])

	legacyInvalidated := storedChallenge
	legacyInvalidated.OTPHash = legacyHex
	legacyInvalidated.OTPHashAlg = domain.PortalOTPHashAlgInvalidatedUnkeyed
	if ok, err := postgres.VerifyChallengeProof(verifyKey, legacyInvalidated, secretOTP, "", ""); err != nil || ok {
		t.Fatalf("expected INVALIDATED_UNKEYED challenge to be rejected (ok=%v err=%v)", ok, err)
	}

	legacyForgedAlg := storedChallenge
	legacyForgedAlg.OTPHash = legacyHex
	legacyForgedAlg.OTPHashAlg = domain.PortalOTPHashAlgHMACSHA256V1
	if ok, err := postgres.VerifyChallengeProof(verifyKey, legacyForgedAlg, secretOTP, "", ""); err != nil || ok {
		t.Fatalf("expected unkeyed SHA-256 digest to fail HMAC verification even with HMAC-SHA256-V1 tag (ok=%v err=%v)", ok, err)
	}
}
