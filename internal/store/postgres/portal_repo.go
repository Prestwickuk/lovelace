package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

var (
	ErrPortalOTPKeyInvalid  = errors.New("portal OTP HMAC key is missing or unsafe")
	ErrPortalCSRFKeyInvalid = errors.New("portal CSRF HMAC key is missing or unsafe")
)

func isSafeHMACKey(key []byte) bool {
	if len(key) < sha256.Size {
		return false
	}
	var nonZero byte
	for _, b := range key {
		nonZero |= b
	}
	return nonZero != 0
}

func isSixDigitOTP(s string) bool {
	if len(s) != 6 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// NormalizePortalOTP canonicalizes user-entered 6-digit OTP input by trimming surrounding whitespace.
func NormalizePortalOTP(otpCode string) string {
	return strings.TrimSpace(otpCode)
}

// HashPortalValue computes a lowercase SHA-256 hex digest of a trimmed high-entropy secret value
// (used only for 256-bit random session tokens, magic-link tokens, and browser nonces).
func HashPortalValue(plain string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(plain)))
	return hex.EncodeToString(sum[:])
}

func computePortalOTPMAC(hmacKey []byte, challengeID uuid.UUID, normalizedEmail, otpCode string) ([]byte, error) {
	if !isSafeHMACKey(hmacKey) {
		return nil, ErrPortalOTPKeyInvalid
	}
	if challengeID == uuid.Nil {
		return nil, errors.New("challenge ID is required for portal OTP HMAC")
	}
	normEmail := domain.NormalizeEmail(normalizedEmail)
	if normEmail == "" {
		return nil, errors.New("normalized email is required for portal OTP HMAC")
	}
	normOTP := NormalizePortalOTP(otpCode)
	if normOTP == "" {
		return nil, errors.New("OTP code is required for portal OTP HMAC")
	}
	mac := hmac.New(sha256.New, hmacKey)
	payload := fmt.Sprintf(
		"lovelace-portal-otp-hmac-v2|%s|%d:%s|%d:%s",
		challengeID.String(),
		len(normEmail), normEmail,
		len(normOTP), normOTP,
	)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil), nil
}

// HashPortalOTP computes a server-keyed HMAC-SHA256 digest over a domain-separated,
// length-delimited payload binding (challengeID, normalizedEmail, normalizedOTP).
// Fails closed if hmacKey is missing/short/zero or if inputs are invalid.
func HashPortalOTP(hmacKey []byte, challengeID uuid.UUID, normalizedEmail, otpCode string) (string, error) {
	normOTP := NormalizePortalOTP(otpCode)
	if !isSixDigitOTP(normOTP) {
		if !isSafeHMACKey(hmacKey) {
			return "", ErrPortalOTPKeyInvalid
		}
		return "", errors.New("portal OTP must be exactly 6 ASCII digits")
	}
	sum, err := computePortalOTPMAC(hmacKey, challengeID, normalizedEmail, normOTP)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum), nil
}

// DerivePortalCSRFToken deterministically derives the session-bound CSRF token from a server
// HMAC key, session ID, and session_token_hash so PostgreSQL stores only csrf_secret_hash.
// Uses a distinct HMAC domain from HashPortalOTP and fails closed if hmacKey is unsafe.
func DerivePortalCSRFToken(hmacKey []byte, sessionID uuid.UUID, sessionTokenHash string) (csrfPlain string, csrfHash string, err error) {
	if !isSafeHMACKey(hmacKey) {
		return "", "", ErrPortalCSRFKeyInvalid
	}
	cleanSessHash := strings.ToLower(strings.TrimSpace(sessionTokenHash))
	if sessionID == uuid.Nil || cleanSessHash == "" {
		return "", "", errors.New("session ID and session token hash are required for CSRF derivation")
	}
	mac := hmac.New(sha256.New, hmacKey)
	_, _ = mac.Write([]byte("lovelace-portal-csrf:" + sessionID.String() + ":" + cleanSessHash))
	csrfPlain = hex.EncodeToString(mac.Sum(nil))
	csrfHash = HashPortalValue(csrfPlain)
	return csrfPlain, csrfHash, nil
}

// GeneratePortalRandomHex generates nBytes of CSPRNG entropy encoded as lowercase hex.
func GeneratePortalRandomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GeneratePortalOTPCode generates an unbiased 6-digit numeric OTP ("000000".."999999").
func GeneratePortalOTPCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func constantTimeHexEqual(a, b string) bool {
	if a == "" || b == "" || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(a)), []byte(strings.ToLower(b))) == 1
}

// reconcileMergedPortalIdentitiesTx reconciles guest_portal_identities when sourceID merges into targetID:
// - If only sourceID had a portal identity and its verified email matches mergedNormEmail, repoint it to targetID.
// - Otherwise delete sourceID's active sessions, challenges, and identity so targetID remains authoritative.
func (s *Store) reconcileMergedPortalIdentitiesTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, sourceID, targetID uuid.UUID,
	mergedNormEmail string,
) error {
	var srcIdentID, tgtIdentID uuid.UUID
	var srcNormVerified string
	hasSrcIdent := false
	hasTgtIdent := false

	if err := tx.QueryRow(ctx, `
		SELECT id, normalized_verified_email
		FROM guest_portal_identities
		WHERE property_id = $1 AND canonical_guest_id = $2
		FOR UPDATE
	`, propertyID, sourceID).Scan(&srcIdentID, &srcNormVerified); err == nil {
		hasSrcIdent = true
	} else if err != pgx.ErrNoRows {
		return MapErr(err)
	}

	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM guest_portal_identities
		WHERE property_id = $1 AND canonical_guest_id = $2
		FOR UPDATE
	`, propertyID, targetID).Scan(&tgtIdentID); err == nil {
		hasTgtIdent = true
	} else if err != pgx.ErrNoRows {
		return MapErr(err)
	}

	if !hasSrcIdent {
		return nil
	}
	if !hasTgtIdent && srcNormVerified != "" && srcNormVerified == mergedNormEmail {
		if _, err := tx.Exec(ctx, `
			UPDATE guest_portal_identities
			SET canonical_guest_id = $3,
			    version = version + 1,
			    updated_at = NOW()
			WHERE property_id = $1 AND id = $2
		`, propertyID, srcIdentID, targetID); err != nil {
			return MapErr(err)
		}
		return nil
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM guest_portal_sessions
		WHERE property_id = $1 AND portal_identity_id = $2
	`, propertyID, srcIdentID); err != nil {
		return MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM guest_portal_challenges
		WHERE property_id = $1 AND portal_identity_id = $2
	`, propertyID, srcIdentID); err != nil {
		return MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM guest_portal_identities
		WHERE property_id = $1 AND id = $2
	`, propertyID, srcIdentID); err != nil {
		return MapErr(err)
	}
	return nil
}

// purgeGuestPortalAndConfirmationAccessTx immediately revokes confirmation_access_tokens and
// deletes portal challenges/sessions for a guest cluster, and deletes (or suspends while
// erasure_requested_at awaits outbox settlement) guest_portal_identities.
func (s *Store) purgeGuestPortalAndConfirmationAccessTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	clusterIDs []uuid.UUID,
	deleteIdentity bool,
	nowUTC time.Time,
) error {
	if len(clusterIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE confirmation_access_tokens
		SET revoked_at = COALESCE(revoked_at, $3)
		WHERE property_id = $1
		  AND revoked_at IS NULL
		  AND (
			reservation_id IN (SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2))
			OR hold_id IN (SELECT id FROM booking_holds WHERE property_id = $1 AND guest_id = ANY($2))
		  )
	`, propertyID, clusterIDs, nowUTC); err != nil {
		return MapErr(err)
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM guest_portal_challenges
		WHERE property_id = $1
		  AND (
			guest_id = ANY($2)
			OR portal_identity_id IN (
				SELECT id FROM guest_portal_identities WHERE property_id = $1 AND canonical_guest_id = ANY($2)
			)
			OR reservation_id IN (
				SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2)
			)
		  )
	`, propertyID, clusterIDs); err != nil {
		return MapErr(err)
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM guest_portal_sessions
		WHERE property_id = $1
		  AND (
			portal_identity_id IN (
				SELECT id FROM guest_portal_identities WHERE property_id = $1 AND canonical_guest_id = ANY($2)
			)
			OR scoped_reservation_id IN (
				SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2)
			)
		  )
	`, propertyID, clusterIDs); err != nil {
		return MapErr(err)
	}

	if deleteIdentity {
		if _, err := tx.Exec(ctx, `
			DELETE FROM guest_portal_identities
			WHERE property_id = $1 AND canonical_guest_id = ANY($2)
		`, propertyID, clusterIDs); err != nil {
			return MapErr(err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE guest_portal_identities
			SET status = 'SUSPENDED',
			    pending_email = '',
			    pending_normalized_email = '',
			    pending_email_expires_at = NULL,
			    version = version + 1,
			    updated_at = NOW()
			WHERE property_id = $1 AND canonical_guest_id = ANY($2)
		`, propertyID, clusterIDs); err != nil {
			return MapErr(err)
		}
	}
	return nil
}

// ListActiveGuestsByNormalizedEmail returns all active non-merged, non-anonymized, non-erasure-pending
// guest profiles in propertyID matching normEmail (for Decision D3 duplicate disambiguation).
func (s *Store) ListActiveGuestsByNormalizedEmail(
	ctx context.Context,
	db DBTX,
	propertyID uuid.UUID,
	normEmail string,
) ([]domain.Guest, error) {
	if db == nil {
		db = s.pool
	}
	normEmail = domain.NormalizeEmail(normEmail)
	if normEmail == "" {
		return nil, nil
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, first_name, last_name, full_name, email, normalized_email,
		       phone, normalized_phone, preferred_contact_method, preferred_language, status,
		       address_json, preferences, notes, merged_into_guest_id, merged_at, anonymized_at,
		       erasure_requested_at, created_by, updated_by, version, created_at, updated_at
		FROM guests
		WHERE property_id = $1
		  AND status IN ('ACTIVE', 'VIP', 'DO_NOT_CONTACT')
		  AND anonymized_at IS NULL
		  AND erasure_requested_at IS NULL
		  AND (normalized_email = $2 OR LOWER(email) = $2)
		ORDER BY created_at ASC, id ASC
	`, propertyID, normEmail)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.Guest
	for rows.Next() {
		var g domain.Guest
		var status string
		if err := rows.Scan(
			&g.ID, &g.PropertyID, &g.FirstName, &g.LastName, &g.FullName,
			&g.Email, &g.NormalizedEmail, &g.Phone, &g.NormalizedPhone,
			&g.PreferredContactMethod, &g.PreferredLanguage, &status,
			&g.AddressJSON, &g.Preferences, &g.Notes, &g.MergedIntoGuestID, &g.MergedAt,
			&g.AnonymizedAt, &g.ErasureRequestedAt, &g.CreatedBy, &g.UpdatedBy,
			&g.Version, &g.CreatedAt, &g.UpdatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		g.Status = domain.GuestStatus(status)
		out = append(out, g)
	}
	return out, MapErr(rows.Err())
}

// GetReservationByReference loads a property-scoped reservation by its human-readable reference.
func (s *Store) GetReservationByReference(
	ctx context.Context,
	db DBTX,
	propertyID uuid.UUID,
	reference string,
) (domain.Reservation, error) {
	if db == nil {
		db = s.pool
	}
	ref := strings.ToUpper(strings.TrimSpace(reference))
	if ref == "" {
		return domain.Reservation{}, domain.ErrNotFound
	}
	row := db.QueryRow(ctx, `
		SELECT id, property_id, reference, guest_id, guest_name, guest_email, guest_phone,
		       channel_code, lower(stay_range), upper(stay_range),
		       adults, children, infants, status,
		       payment_status, total_pence, deposit_pence, amount_paid_pence, balance_due_pence, currency,
		       occupant_details, estimated_arrival_time, guest_arrival_notes, internal_notes, cancellation_reason, cancelled_at,
		       cancellation_requested_at, cancellation_request_note,
		       created_by, updated_by, version, created_at, updated_at
		FROM reservations
		WHERE property_id = $1 AND UPPER(reference) = $2
	`, propertyID, ref)
	res, err := scanReservation(row)
	if err != nil {
		return domain.Reservation{}, MapErr(err)
	}
	unitMap, err := s.loadReservationUnitsMap(ctx, db, propertyID, []uuid.UUID{res.ID})
	if err != nil {
		return domain.Reservation{}, err
	}
	res.Units = unitMap[res.ID]
	for _, u := range res.Units {
		res.UnitIDs = append(res.UnitIDs, u.UnitID)
	}
	return res, nil
}

// VerifyConfirmationTokenForPortalTx validates a direct booking's reference + confirmation token
// strictly within propertyID for non-anonymized canonical guests, updates last_used_at, and returns
// the owned reservation and token expiry timestamp.
func (s *Store) VerifyConfirmationTokenForPortalTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	bookingReference, plainToken string,
	nowUTC time.Time,
) (domain.Reservation, time.Time, error) {
	ref := strings.ToUpper(strings.TrimSpace(bookingReference))
	tokHash := HashConfirmationToken(plainToken)
	if ref == "" || strings.TrimSpace(plainToken) == "" {
		return domain.Reservation{}, time.Time{}, domain.ErrUnauthorized
	}

	var tokID, resID, guestID uuid.UUID
	var tokExpiresAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT t.id, t.expires_at, r.id, r.guest_id
		FROM confirmation_access_tokens t
		JOIN reservations r ON r.property_id = t.property_id AND r.id = t.reservation_id
		WHERE t.property_id = $1
		  AND t.token_hash = $2
		  AND t.revoked_at IS NULL
		  AND t.expires_at > $3
		  AND UPPER(r.reference) = $4
		FOR UPDATE OF t
	`, propertyID, tokHash, nowUTC, ref).Scan(&tokID, &tokExpiresAt, &resID, &guestID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.Reservation{}, time.Time{}, domain.ErrUnauthorized
		}
		return domain.Reservation{}, time.Time{}, MapErr(err)
	}

	canonID, _, anonOrPending, err := s.LockCanonicalGuestClusterTx(ctx, tx, propertyID, guestID)
	if err != nil || anonOrPending {
		return domain.Reservation{}, time.Time{}, domain.ErrUnauthorized
	}
	guest, err := s.GetGuest(ctx, tx, propertyID, canonID)
	if err != nil || guest.Status == domain.GuestStatusAnonymized || guest.AnonymizedAt != nil || guest.ErasureRequestedAt != nil {
		return domain.Reservation{}, time.Time{}, domain.ErrUnauthorized
	}

	if _, err := tx.Exec(ctx, `
		UPDATE confirmation_access_tokens
		SET last_used_at = $2
		WHERE id = $1
	`, tokID, nowUTC); err != nil {
		return domain.Reservation{}, time.Time{}, MapErr(err)
	}

	res, err := s.GetReservation(ctx, tx, propertyID, resID)
	if err != nil {
		return domain.Reservation{}, time.Time{}, err
	}
	return res, tokExpiresAt, nil
}

func scanPortalIdentity(row scannable) (domain.GuestPortalIdentity, error) {
	var out domain.GuestPortalIdentity
	var status string
	err := row.Scan(
		&out.ID, &out.PropertyID, &out.CanonicalGuestID,
		&out.VerifiedEmail, &out.NormalizedVerifiedEmail,
		&out.PendingEmail, &out.PendingNormalizedEmail, &out.PendingEmailExpiresAt,
		&out.EmailVerifiedAt, &out.LastLoginAt, &status,
		&out.Version, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return domain.GuestPortalIdentity{}, MapErr(err)
	}
	out.Status = domain.PortalIdentityStatus(status)
	return out, nil
}

func (s *Store) GetPortalIdentityByID(ctx context.Context, db DBTX, propertyID, id uuid.UUID) (domain.GuestPortalIdentity, error) {
	if db == nil {
		db = s.pool
	}
	return scanPortalIdentity(db.QueryRow(ctx, `
		SELECT id, property_id, canonical_guest_id, verified_email, normalized_verified_email,
		       pending_email, pending_normalized_email, pending_email_expires_at,
		       email_verified_at, last_login_at, status, version, created_at, updated_at
		FROM guest_portal_identities
		WHERE property_id = $1 AND id = $2
	`, propertyID, id))
}

func (s *Store) GetPortalIdentityByGuest(ctx context.Context, db DBTX, propertyID, canonicalGuestID uuid.UUID) (domain.GuestPortalIdentity, error) {
	if db == nil {
		db = s.pool
	}
	return scanPortalIdentity(db.QueryRow(ctx, `
		SELECT id, property_id, canonical_guest_id, verified_email, normalized_verified_email,
		       pending_email, pending_normalized_email, pending_email_expires_at,
		       email_verified_at, last_login_at, status, version, created_at, updated_at
		FROM guest_portal_identities
		WHERE property_id = $1 AND canonical_guest_id = $2
	`, propertyID, canonicalGuestID))
}

func (s *Store) GetPortalIdentityByVerifiedEmail(ctx context.Context, db DBTX, propertyID uuid.UUID, normEmail string) (domain.GuestPortalIdentity, error) {
	if db == nil {
		db = s.pool
	}
	return scanPortalIdentity(db.QueryRow(ctx, `
		SELECT id, property_id, canonical_guest_id, verified_email, normalized_verified_email,
		       pending_email, pending_normalized_email, pending_email_expires_at,
		       email_verified_at, last_login_at, status, version, created_at, updated_at
		FROM guest_portal_identities
		WHERE property_id = $1 AND normalized_verified_email = $2
	`, propertyID, domain.NormalizeEmail(normEmail)))
}

// UpsertPortalIdentityOnLoginTx binds or refreshes the per-property GuestPortalIdentity
// for canonicalGuestID and verifiedEmail.
func (s *Store) UpsertPortalIdentityOnLoginTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, canonicalGuestID uuid.UUID,
	verifiedEmail string,
	nowUTC time.Time,
) (domain.GuestPortalIdentity, error) {
	normEmail := domain.NormalizeEmail(verifiedEmail)
	cleanEmail := strings.TrimSpace(strings.ToLower(verifiedEmail))
	if normEmail == "" {
		return domain.GuestPortalIdentity{}, domain.NewValidationError(map[string]string{"email": "valid email required"})
	}
	newID, err := NewUUID()
	if err != nil {
		return domain.GuestPortalIdentity{}, err
	}
	return scanPortalIdentity(tx.QueryRow(ctx, `
		INSERT INTO guest_portal_identities (
			id, property_id, canonical_guest_id, verified_email, normalized_verified_email,
			email_verified_at, last_login_at, status
		) VALUES ($1, $2, $3, $4, $5, $6, $6, 'ACTIVE')
		ON CONFLICT (property_id, canonical_guest_id) DO UPDATE
		SET verified_email = EXCLUDED.verified_email,
		    normalized_verified_email = EXCLUDED.normalized_verified_email,
		    email_verified_at = EXCLUDED.email_verified_at,
		    last_login_at = EXCLUDED.last_login_at,
		    status = 'ACTIVE',
		    version = guest_portal_identities.version + 1,
		    updated_at = NOW()
		RETURNING id, property_id, canonical_guest_id, verified_email, normalized_verified_email,
		          pending_email, pending_normalized_email, pending_email_expires_at,
		          email_verified_at, last_login_at, status, version, created_at, updated_at
	`, newID, propertyID, canonicalGuestID, cleanEmail, normEmail, nowUTC))
}

func scrubSettledChallengeOutboxByEmailTx(ctx context.Context, tx pgx.Tx, propertyID uuid.UUID, normEmail string) error {
	if normEmail == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE email_outbox
		SET body_text = '[redacted security challenge]',
		    body_html = '',
		    updated_at = NOW()
		WHERE property_id = $1
		  AND LOWER(recipient_email) = $2
		  AND template IN ('portal_verification', 'portal_step_up', 'portal_email_change_verify')
		  AND status IN ('SENT', 'CANCELLED', 'FAILED')
		  AND body_text <> '[redacted security challenge]'
	`, propertyID, normEmail)
	return MapErr(err)
}

// InvalidateActivePortalChallengesTx invalidates prior unconsumed challenges for the same tuple.
func (s *Store) InvalidateActivePortalChallengesTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	normEmail string,
	purpose domain.PortalChallengePurpose,
	nowUTC time.Time,
) error {
	norm := domain.NormalizeEmail(normEmail)
	if _, err := tx.Exec(ctx, `
		UPDATE guest_portal_challenges
		SET invalidated_at = $4
		WHERE property_id = $1
		  AND normalized_email = $2
		  AND purpose = $3
		  AND consumed_at IS NULL
		  AND invalidated_at IS NULL
	`, propertyID, norm, string(purpose), nowUTC); err != nil {
		return MapErr(err)
	}
	return scrubSettledChallengeOutboxByEmailTx(ctx, tx, propertyID, norm)
}

func (s *Store) CreatePortalChallengeTx(
	ctx context.Context,
	tx pgx.Tx,
	c domain.GuestPortalChallenge,
) (domain.GuestPortalChallenge, error) {
	if c.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.GuestPortalChallenge{}, err
		}
		c.ID = id
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = domain.PortalChallengeMaxAttempts
	}
	if c.OTPHashAlg == "" {
		c.OTPHashAlg = domain.PortalOTPHashAlgHMACSHA256V1
	}
	if c.OTPHashAlg != domain.PortalOTPHashAlgHMACSHA256V1 {
		return domain.GuestPortalChallenge{}, errors.New("refusing to persist non-HMAC portal OTP challenge")
	}
	if rawMAC, err := hex.DecodeString(strings.TrimSpace(c.OTPHash)); err != nil || len(rawMAC) != sha256.Size {
		return domain.GuestPortalChallenge{}, errors.New("portal challenge otp_hash must be a 32-byte hex HMAC-SHA256 digest")
	}
	var out domain.GuestPortalChallenge
	var purpose string
	err := tx.QueryRow(ctx, `
		INSERT INTO guest_portal_challenges (
			id, property_id, portal_identity_id, guest_id, reservation_id,
			normalized_email, purpose, otp_hash, otp_hash_alg, token_hash, browser_nonce_hash,
			booking_reference, failed_attempts, max_attempts, expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11,
			$12, 0, $13, $14
		)
		RETURNING id, property_id, portal_identity_id, guest_id, reservation_id,
		          normalized_email, purpose, otp_hash, otp_hash_alg, token_hash, browser_nonce_hash,
		          booking_reference, failed_attempts, max_attempts, expires_at,
		          consumed_at, invalidated_at, created_at
	`,
		c.ID, c.PropertyID, c.PortalIdentityID, c.GuestID, c.ReservationID,
		domain.NormalizeEmail(c.NormalizedEmail), string(c.Purpose), strings.ToLower(strings.TrimSpace(c.OTPHash)), c.OTPHashAlg, c.TokenHash, c.BrowserNonceHash,
		strings.ToUpper(strings.TrimSpace(c.BookingReference)), c.MaxAttempts, c.ExpiresAt,
	).Scan(
		&out.ID, &out.PropertyID, &out.PortalIdentityID, &out.GuestID, &out.ReservationID,
		&out.NormalizedEmail, &purpose, &out.OTPHash, &out.OTPHashAlg, &out.TokenHash, &out.BrowserNonceHash,
		&out.BookingReference, &out.FailedAttempts, &out.MaxAttempts, &out.ExpiresAt,
		&out.ConsumedAt, &out.InvalidatedAt, &out.CreatedAt,
	)
	if err != nil {
		return domain.GuestPortalChallenge{}, MapErr(err)
	}
	out.Purpose = domain.PortalChallengePurpose(purpose)
	return out, nil
}

// LockLatestPortalChallengeByEmailTx locks the newest active unconsumed HMAC-protected challenge for (propertyID, normEmail, purpose).
func (s *Store) LockLatestPortalChallengeByEmailTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	normEmail string,
	purpose domain.PortalChallengePurpose,
	nowUTC time.Time,
) (domain.GuestPortalChallenge, error) {
	var out domain.GuestPortalChallenge
	var purp string
	err := tx.QueryRow(ctx, `
		SELECT id, property_id, portal_identity_id, guest_id, reservation_id,
		       normalized_email, purpose, otp_hash, otp_hash_alg, token_hash, browser_nonce_hash,
		       booking_reference, failed_attempts, max_attempts, expires_at,
		       consumed_at, invalidated_at, created_at
		FROM guest_portal_challenges
		WHERE property_id = $1
		  AND normalized_email = $2
		  AND purpose = $3
		  AND otp_hash_alg = $5
		  AND consumed_at IS NULL
		  AND invalidated_at IS NULL
		  AND expires_at > $4
		  AND failed_attempts < max_attempts
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE
	`, propertyID, domain.NormalizeEmail(normEmail), string(purpose), nowUTC, domain.PortalOTPHashAlgHMACSHA256V1).Scan(
		&out.ID, &out.PropertyID, &out.PortalIdentityID, &out.GuestID, &out.ReservationID,
		&out.NormalizedEmail, &purp, &out.OTPHash, &out.OTPHashAlg, &out.TokenHash, &out.BrowserNonceHash,
		&out.BookingReference, &out.FailedAttempts, &out.MaxAttempts, &out.ExpiresAt,
		&out.ConsumedAt, &out.InvalidatedAt, &out.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.GuestPortalChallenge{}, domain.ErrUnauthorized
		}
		return domain.GuestPortalChallenge{}, MapErr(err)
	}
	out.Purpose = domain.PortalChallengePurpose(purp)
	return out, nil
}

// LockPortalChallengeByTokenHashTx locks an unconsumed HMAC-protected challenge by its magic-link token_hash.
func (s *Store) LockPortalChallengeByTokenHashTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	tokenHash string,
	nowUTC time.Time,
) (domain.GuestPortalChallenge, error) {
	var out domain.GuestPortalChallenge
	var purp string
	err := tx.QueryRow(ctx, `
		SELECT id, property_id, portal_identity_id, guest_id, reservation_id,
		       normalized_email, purpose, otp_hash, otp_hash_alg, token_hash, browser_nonce_hash,
		       booking_reference, failed_attempts, max_attempts, expires_at,
		       consumed_at, invalidated_at, created_at
		FROM guest_portal_challenges
		WHERE property_id = $1
		  AND token_hash = $2
		  AND otp_hash_alg = $4
		  AND consumed_at IS NULL
		  AND invalidated_at IS NULL
		  AND expires_at > $3
		  AND failed_attempts < max_attempts
		FOR UPDATE
	`, propertyID, tokenHash, nowUTC, domain.PortalOTPHashAlgHMACSHA256V1).Scan(
		&out.ID, &out.PropertyID, &out.PortalIdentityID, &out.GuestID, &out.ReservationID,
		&out.NormalizedEmail, &purp, &out.OTPHash, &out.OTPHashAlg, &out.TokenHash, &out.BrowserNonceHash,
		&out.BookingReference, &out.FailedAttempts, &out.MaxAttempts, &out.ExpiresAt,
		&out.ConsumedAt, &out.InvalidatedAt, &out.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.GuestPortalChallenge{}, domain.ErrUnauthorized
		}
		return domain.GuestPortalChallenge{}, MapErr(err)
	}
	out.Purpose = domain.PortalChallengePurpose(purp)
	return out, nil
}

// IncrementPortalChallengeFailure bumps failed_attempts on an invalid OTP/nonce attempt in its own query
// so brute-force guesses are bounded even when the caller's transaction rolls back.
func (s *Store) IncrementPortalChallengeFailure(ctx context.Context, propertyID, challengeID uuid.UUID, nowUTC time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE guest_portal_challenges
		SET failed_attempts = failed_attempts + 1,
		    invalidated_at = CASE WHEN failed_attempts + 1 >= max_attempts THEN $3 ELSE invalidated_at END
		WHERE property_id = $1 AND id = $2 AND consumed_at IS NULL
	`, propertyID, challengeID, nowUTC)
	return MapErr(err)
}

func (s *Store) ConsumePortalChallengeTx(ctx context.Context, tx pgx.Tx, propertyID, challengeID uuid.UUID, normEmail string, nowUTC time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE guest_portal_challenges
		SET consumed_at = $3
		WHERE property_id = $1
		  AND id = $2
		  AND consumed_at IS NULL
		  AND invalidated_at IS NULL
		  AND expires_at > $3
		  AND failed_attempts < max_attempts
	`, propertyID, challengeID, nowUTC)
	if err != nil {
		return MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUnauthorized
	}
	return scrubSettledChallengeOutboxByEmailTx(ctx, tx, propertyID, domain.NormalizeEmail(normEmail))
}

// VerifyChallengeProof checks either a 6-digit OTP code (using constant-time HMAC-SHA256
// verification with a server-held key) or a magic-link token paired with a matching same-browser
// nonce cookie hash. Fails closed with ErrPortalOTPKeyInvalid if otpHMACKey is missing/unsafe,
// and never accepts pre-remediation unkeyed challenge rows.
func VerifyChallengeProof(otpHMACKey []byte, ch domain.GuestPortalChallenge, otpCode, magicToken, browserNonce string) (bool, error) {
	if NormalizePortalOTP(otpCode) != "" {
		if !isSafeHMACKey(otpHMACKey) {
			return false, ErrPortalOTPKeyInvalid
		}
		if ch.OTPHashAlg != domain.PortalOTPHashAlgHMACSHA256V1 {
			return false, nil
		}
		storedMAC, decErr := hex.DecodeString(strings.TrimSpace(ch.OTPHash))
		if decErr != nil || len(storedMAC) != sha256.Size {
			return false, nil
		}
		expectedMAC, macErr := computePortalOTPMAC(otpHMACKey, ch.ID, ch.NormalizedEmail, otpCode)
		if macErr != nil {
			return false, nil
		}
		return hmac.Equal(expectedMAC, storedMAC), nil
	}
	if ch.OTPHashAlg != domain.PortalOTPHashAlgHMACSHA256V1 {
		return false, nil
	}
	if strings.TrimSpace(magicToken) != "" && strings.TrimSpace(browserNonce) != "" && ch.BrowserNonceHash != "" {
		tokOK := constantTimeHexEqual(HashPortalValue(magicToken), ch.TokenHash)
		nonceOK := constantTimeHexEqual(HashPortalValue(browserNonce), ch.BrowserNonceHash)
		return tokOK && nonceOK, nil
	}
	return false, nil
}

func (s *Store) CreatePortalSessionTx(
	ctx context.Context,
	tx pgx.Tx,
	sess domain.GuestPortalSession,
) (domain.GuestPortalSession, error) {
	if sess.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.GuestPortalSession{}, err
		}
		sess.ID = id
	}
	var out domain.GuestPortalSession
	var scope string
	err := tx.QueryRow(ctx, `
		INSERT INTO guest_portal_sessions (
			id, property_id, portal_identity_id, scoped_reservation_id, scope_type,
			session_token_hash, csrf_secret_hash, verified_at,
			idle_expires_at, absolute_expires_at, last_seen_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $8
		)
		RETURNING id, property_id, portal_identity_id, scoped_reservation_id, scope_type,
		          session_token_hash, csrf_secret_hash, verified_at,
		          idle_expires_at, absolute_expires_at, revoked_at, last_seen_at, created_at
	`,
		sess.ID, sess.PropertyID, sess.PortalIdentityID, sess.ScopedReservationID, string(sess.ScopeType),
		sess.SessionTokenHash, sess.CSRFSecretHash, sess.VerifiedAt,
		sess.IdleExpiresAt, sess.AbsoluteExpiresAt,
	).Scan(
		&out.ID, &out.PropertyID, &out.PortalIdentityID, &out.ScopedReservationID, &scope,
		&out.SessionTokenHash, &out.CSRFSecretHash, &out.VerifiedAt,
		&out.IdleExpiresAt, &out.AbsoluteExpiresAt, &out.RevokedAt, &out.LastSeenAt, &out.CreatedAt,
	)
	if err != nil {
		return domain.GuestPortalSession{}, MapErr(err)
	}
	out.ScopeType = domain.PortalSessionScope(scope)
	return out, nil
}

// AuthenticateAndSlidePortalSession validates an opaque portal session token strictly within propertyID,
// enforces idle & absolute TTLs, and slides idle_expires_at up to absolute_expires_at.
func (s *Store) AuthenticateAndSlidePortalSession(
	ctx context.Context,
	propertyID uuid.UUID,
	plainSessionToken string,
	nowUTC time.Time,
) (domain.GuestPortalSession, error) {
	tokHash := HashPortalValue(plainSessionToken)
	if strings.TrimSpace(plainSessionToken) == "" {
		return domain.GuestPortalSession{}, domain.ErrUnauthorized
	}

	var sess domain.GuestPortalSession
	var scope string
	err := s.pool.QueryRow(ctx, `
		SELECT id, property_id, portal_identity_id, scoped_reservation_id, scope_type,
		       session_token_hash, csrf_secret_hash, verified_at,
		       idle_expires_at, absolute_expires_at, revoked_at, last_seen_at, created_at
		FROM guest_portal_sessions
		WHERE property_id = $1
		  AND session_token_hash = $2
		  AND revoked_at IS NULL
		  AND idle_expires_at > $3
		  AND absolute_expires_at > $3
	`, propertyID, tokHash, nowUTC).Scan(
		&sess.ID, &sess.PropertyID, &sess.PortalIdentityID, &sess.ScopedReservationID, &scope,
		&sess.SessionTokenHash, &sess.CSRFSecretHash, &sess.VerifiedAt,
		&sess.IdleExpiresAt, &sess.AbsoluteExpiresAt, &sess.RevokedAt, &sess.LastSeenAt, &sess.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.GuestPortalSession{}, domain.ErrUnauthorized
		}
		return domain.GuestPortalSession{}, MapErr(err)
	}
	sess.ScopeType = domain.PortalSessionScope(scope)

	idleTTL := domain.PortalTierBIdleTTL
	if sess.ScopeType == domain.PortalScopeBooking {
		idleTTL = domain.PortalTierAIdleTTL
	}
	nextIdle := nowUTC.Add(idleTTL)
	if nextIdle.After(sess.AbsoluteExpiresAt) {
		nextIdle = sess.AbsoluteExpiresAt
	}

	if _, err := s.pool.Exec(ctx, `
		UPDATE guest_portal_sessions
		SET last_seen_at = $3,
		    idle_expires_at = $4
		WHERE property_id = $1 AND id = $2 AND revoked_at IS NULL
	`, propertyID, sess.ID, nowUTC, nextIdle); err == nil {
		sess.LastSeenAt = nowUTC
		sess.IdleExpiresAt = nextIdle
	}
	return sess, nil
}

func (s *Store) RefreshPortalSessionVerifiedAtTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, sessionID uuid.UUID,
	nowUTC time.Time,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE guest_portal_sessions
		SET verified_at = $3, last_seen_at = $3
		WHERE property_id = $1 AND id = $2 AND revoked_at IS NULL
	`, propertyID, sessionID, nowUTC)
	return MapErr(err)
}

func (s *Store) RevokePortalSessionByTokenHashTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	tokenHash string,
	nowUTC time.Time,
) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		UPDATE guest_portal_sessions
		SET revoked_at = $3
		WHERE property_id = $1 AND session_token_hash = $2 AND revoked_at IS NULL
		RETURNING id
	`, propertyID, tokenHash, nowUTC).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, MapErr(err)
	}
	return id, true, nil
}

// RevokeOtherPortalSessionsTx revokes all active portal sessions for portalIdentityID except keepSessionID.
func (s *Store) RevokeOtherPortalSessionsTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, portalIdentityID, keepSessionID uuid.UUID,
	nowUTC time.Time,
) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE guest_portal_sessions
		SET revoked_at = $4
		WHERE property_id = $1
		  AND portal_identity_id = $2
		  AND id <> $3
		  AND revoked_at IS NULL
	`, propertyID, portalIdentityID, keepSessionID, nowUTC)
	if err != nil {
		return 0, MapErr(err)
	}
	return tag.RowsAffected(), nil
}

// RevokeAllGuestPortalAccessTx revokes all active Tier A & Tier B portal sessions and unconsumed
// challenges for a canonical guest cluster (used for operational support recovery / compromise containment).
func (s *Store) RevokeAllGuestPortalAccessTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	clusterIDs []uuid.UUID,
	nowUTC time.Time,
) (int64, error) {
	if len(clusterIDs) == 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE guest_portal_challenges
		SET invalidated_at = $3
		WHERE property_id = $1
		  AND consumed_at IS NULL
		  AND invalidated_at IS NULL
		  AND (
			guest_id = ANY($2)
			OR portal_identity_id IN (
				SELECT id FROM guest_portal_identities WHERE property_id = $1 AND canonical_guest_id = ANY($2)
			)
			OR reservation_id IN (
				SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2)
			)
		  )
	`, propertyID, clusterIDs, nowUTC); err != nil {
		return 0, MapErr(err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE guest_portal_sessions
		SET revoked_at = $3
		WHERE property_id = $1
		  AND revoked_at IS NULL
		  AND (
			portal_identity_id IN (
				SELECT id FROM guest_portal_identities WHERE property_id = $1 AND canonical_guest_id = ANY($2)
			)
			OR scoped_reservation_id IN (
				SELECT id FROM reservations WHERE property_id = $1 AND guest_id = ANY($2)
			)
		  )
	`, propertyID, clusterIDs, nowUTC)
	if err != nil {
		return 0, MapErr(err)
	}
	return tag.RowsAffected(), nil
}

// UpdateReservationArrivalDetailsTx updates solely estimated_arrival_time and guest_arrival_notes
// without touching stay_range, units, financials, or internal_notes.
func (s *Store) UpdateReservationArrivalDetailsTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, reservationID uuid.UUID,
	eta, arrivalNotes string,
) (domain.Reservation, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE reservations
		SET estimated_arrival_time = $3,
		    guest_arrival_notes = $4,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2
	`, propertyID, reservationID, eta, arrivalNotes)
	if err != nil {
		return domain.Reservation{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Reservation{}, domain.ErrNotFound
	}
	return s.GetReservation(ctx, tx, propertyID, reservationID)
}

// CountOpenPortalChangeRequestsTx counts open portal booking-change enquiries for a reservation
// to enforce the Section 6.1 cap (max 3 open change requests per reservation).
func (s *Store) CountOpenPortalChangeRequestsTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, reservationID uuid.UUID,
) (int, error) {
	var count int
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM guest_enquiries
		WHERE property_id = $1
		  AND reservation_id = $2
		  AND source = 'PORTAL'
		  AND category IN ('BOOKING', 'AVAILABILITY')
		  AND status NOT IN ('RESOLVED', 'CLOSED')
	`, propertyID, reservationID).Scan(&count)
	return count, MapErr(err)
}

// HasOpenPrivacyRightsEnquiryTx reports whether an open SAR or Erasure request already exists
// for the canonical guest cluster so duplicate submissions are silently deduplicated without
// leaking prior request status (Section 6.1 & Section 7.2).
func (s *Store) HasOpenPrivacyRightsEnquiryTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	clusterIDs []uuid.UUID,
	subjectPrefix string,
) (bool, error) {
	if len(clusterIDs) == 0 {
		return false, nil
	}
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM guest_enquiries
			WHERE property_id = $1
			  AND guest_id = ANY($2)
			  AND source = 'PORTAL'
			  AND subject LIKE $3 || '%'
			  AND status NOT IN ('RESOLVED', 'CLOSED')
		)
	`, propertyID, clusterIDs, subjectPrefix).Scan(&exists)
	return exists, MapErr(err)
}

// UpdateGuestPortalContactTx updates self-service phone, preferred_contact_method, and preferred_language
// on the canonical guest without altering email, status, or internal notes.
func (s *Store) UpdateGuestPortalContactTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, canonicalGuestID uuid.UUID,
	phone, contactMethod, preferredLang string,
) (domain.Guest, error) {
	normPhone := domain.NormalizePhone(phone)
	tag, err := tx.Exec(ctx, `
		UPDATE guests
		SET phone = $3,
		    normalized_phone = $4,
		    preferred_contact_method = $5,
		    preferred_language = $6,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1
		  AND id = $2
		  AND status IN ('ACTIVE', 'VIP')
		  AND anonymized_at IS NULL
		  AND erasure_requested_at IS NULL
	`, propertyID, canonicalGuestID, phone, normPhone, contactMethod, preferredLang)
	if err != nil {
		return domain.Guest{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Guest{}, domain.ErrConflict
	}
	return s.GetGuest(ctx, tx, propertyID, canonicalGuestID)
}

// ReplacePortalGuestPreferencesTx replaces guest-editable preference categories (ARRIVAL,
// ACCESSIBILITY, COMMUNICATION, PET) without exposing or overwriting internal staff notes
// on existing rows or other staff-only categories.
func (s *Store) ReplacePortalGuestPreferencesTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, canonicalGuestID uuid.UUID,
	items []domain.PortalGuestPreferenceView,
) error {
	// Remove portal-managed categories that have no internal staff notes, then update any
	// remaining matching row (preserving staff-authored notes) or insert a fresh row.
	if _, err := tx.Exec(ctx, `
		DELETE FROM guest_preferences
		WHERE property_id = $1
		  AND guest_id = $2
		  AND category IN ('ARRIVAL', 'ACCESSIBILITY', 'COMMUNICATION', 'PET')
		  AND TRIM(notes) = ''
	`, propertyID, canonicalGuestID); err != nil {
		return MapErr(err)
	}
	for _, it := range items {
		updTag, err := tx.Exec(ctx, `
			UPDATE guest_preferences
			SET preference_value = $5,
			    updated_at = NOW()
			WHERE property_id = $1
			  AND guest_id = $2
			  AND category = $3
			  AND preference_key = $4
		`, propertyID, canonicalGuestID, string(it.Category), it.PreferenceKey, it.PreferenceValue)
		if err != nil {
			return MapErr(err)
		}
		if updTag.RowsAffected() > 0 {
			continue
		}
		id, err := NewUUID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO guest_preferences (
				id, property_id, guest_id, category, preference_key, preference_value, notes
			) VALUES ($1, $2, $3, $4, $5, $6, '')
		`, id, propertyID, canonicalGuestID, string(it.Category), it.PreferenceKey, it.PreferenceValue); err != nil {
			return MapErr(err)
		}
	}
	return nil
}

func (s *Store) SetPortalPendingEmailTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, portalIdentityID uuid.UUID,
	pendingEmail string,
	expiresAt time.Time,
) error {
	clean := strings.TrimSpace(strings.ToLower(pendingEmail))
	norm := domain.NormalizeEmail(pendingEmail)
	tag, err := tx.Exec(ctx, `
		UPDATE guest_portal_identities
		SET pending_email = $3,
		    pending_normalized_email = $4,
		    pending_email_expires_at = $5,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND status = 'ACTIVE'
	`, propertyID, portalIdentityID, clean, norm, expiresAt)
	if err != nil {
		return MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUnauthorized
	}
	return nil
}

// ConfirmPortalEmailChangeTx atomically updates guest_portal_identities and canonical guests.email
// after the guest proves possession of pending_email.
func (s *Store) ConfirmPortalEmailChangeTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, portalIdentityID, canonicalGuestID uuid.UUID,
	newEmail string,
	nowUTC time.Time,
) (domain.GuestPortalIdentity, domain.Guest, error) {
	clean := strings.TrimSpace(strings.ToLower(newEmail))
	norm := domain.NormalizeEmail(newEmail)
	ident, err := scanPortalIdentity(tx.QueryRow(ctx, `
		UPDATE guest_portal_identities
		SET verified_email = $3,
		    normalized_verified_email = $4,
		    pending_email = '',
		    pending_normalized_email = '',
		    pending_email_expires_at = NULL,
		    email_verified_at = $5,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND status = 'ACTIVE'
		RETURNING id, property_id, canonical_guest_id, verified_email, normalized_verified_email,
		          pending_email, pending_normalized_email, pending_email_expires_at,
		          email_verified_at, last_login_at, status, version, created_at, updated_at
	`, propertyID, portalIdentityID, clean, norm, nowUTC))
	if err != nil {
		return domain.GuestPortalIdentity{}, domain.Guest{}, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE guests
		SET email = $3,
		    normalized_email = $4,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1
		  AND id = $2
		  AND status IN ('ACTIVE', 'VIP', 'DO_NOT_CONTACT')
		  AND anonymized_at IS NULL
		  AND erasure_requested_at IS NULL
	`, propertyID, canonicalGuestID, clean, norm); err != nil {
		return domain.GuestPortalIdentity{}, domain.Guest{}, MapErr(err)
	}
	g, err := s.GetGuest(ctx, tx, propertyID, canonicalGuestID)
	if err != nil {
		return domain.GuestPortalIdentity{}, domain.Guest{}, err
	}
	return ident, g, nil
}

// ReopenEnquiryOnPortalReplyTx transitions a non-CLOSED enquiry to WAITING_FOR_PROPERTY
// (preserving NEW if still NEW, and rejecting CLOSED enquiries with 409 Conflict per Section 6.1).
func (s *Store) ReopenEnquiryOnPortalReplyTx(ctx context.Context, tx pgx.Tx, propertyID, enquiryID uuid.UUID) (domain.GuestEnquiry, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE guest_enquiries
		SET status = CASE WHEN status = 'NEW' THEN 'NEW' ELSE 'WAITING_FOR_PROPERTY' END,
		    resolved_at = NULL,
		    resolved_by = NULL,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND status <> 'CLOSED'
	`, propertyID, enquiryID)
	if err != nil {
		return domain.GuestEnquiry{}, MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.GuestEnquiry{}, domain.ErrConflict
	}
	return s.GetGuestEnquiry(ctx, tx, propertyID, enquiryID)
}

// PruneExpiredPortalState implements the shortest technically safe cleanup for expired/consumed/revoked
// portal credentials while preserving audit_logs evidence (Section 7.2 #3), and scrubs any settled or
// expired security challenge bodies in email_outbox.
func (s *Store) PruneExpiredPortalState(ctx context.Context, nowUTC time.Time) error {
	skewCutoff := nowUTC.Add(-5 * time.Minute)
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM guest_portal_challenges
		WHERE expires_at <= $1
		   OR (consumed_at IS NOT NULL AND consumed_at <= $1)
		   OR (invalidated_at IS NOT NULL AND invalidated_at <= $1)
	`, skewCutoff); err != nil {
		return MapErr(err)
	}
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM guest_portal_sessions
		WHERE absolute_expires_at <= $1
		   OR idle_expires_at <= $1
		   OR (revoked_at IS NOT NULL AND revoked_at <= $1)
	`, skewCutoff); err != nil {
		return MapErr(err)
	}
	outboxCutoff := nowUTC.Add(-domain.PortalChallengeTTL)
	if _, err := s.pool.Exec(ctx, `
		UPDATE email_outbox
		SET body_text = '[redacted security challenge]',
		    body_html = '',
		    updated_at = NOW()
		WHERE template IN ('portal_verification', 'portal_step_up', 'portal_email_change_verify')
		  AND body_text <> '[redacted security challenge]'
		  AND (
			status IN ('SENT', 'CANCELLED', 'FAILED')
			OR created_at <= $1
		  )
	`, outboxCutoff); err != nil {
		return MapErr(err)
	}
	return nil
}
