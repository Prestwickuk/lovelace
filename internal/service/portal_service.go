package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/mailer"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

var (
	ErrPortalDisabled                = errors.New("guest portal is not enabled for this property")
	ErrPortalStepUpRequired          = errors.New("recent email verification is required for this action")
	ErrPortalDuplicateDisambiguation = errors.New("corroborating booking reference required to verify your profile")
	ErrPortalOTAManaged              = errors.New("booking is managed through an external channel")
	ErrPortalDoNotContactRestricted  = errors.New("profile updates are restricted while do-not-contact status is active")
	ErrPortalCSRFInvalid             = errors.New("invalid or missing portal CSRF token")
	ErrPortalKeyUnavailable          = errors.New("portal cryptographic key is unavailable or unsafe")
)

const portalPrivacyReceiptMessage = "Your request has been recorded for review by the property team."

func validatePortalMasterKey(seed []byte) error {
	if len(seed) != sha256.Size {
		return ErrPortalKeyUnavailable
	}
	var seen [256]bool
	distinct := 0
	for _, b := range seed {
		if !seen[b] {
			seen[b] = true
			distinct++
		}
	}
	if distinct < 8 {
		return ErrPortalKeyUnavailable
	}
	if subtle.ConstantTimeCompare(seed, []byte("tirbach-dev-only-32byte-key-0001")) == 1 {
		return ErrPortalKeyUnavailable
	}
	if insecureHex, err := hex.DecodeString("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"); err == nil && subtle.ConstantTimeCompare(seed, insecureHex) == 1 {
		return ErrPortalKeyUnavailable
	}
	return nil
}

func derivePortalHMACSubkey(seed []byte, subkeyPurpose string) ([]byte, error) {
	if err := validatePortalMasterKey(seed); err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, seed)
	_, _ = mac.Write([]byte(subkeyPurpose))
	return mac.Sum(nil), nil
}

func (s *Service) portalCSRFHMACKey() ([]byte, error) {
	return derivePortalHMACSubkey(s.cfg.EncryptionKey, "lovelace-portal-subkey-csrf-v1")
}

func (s *Service) portalOTPHMACKey() ([]byte, error) {
	return derivePortalHMACSubkey(s.cfg.EncryptionKey, "lovelace-portal-subkey-otp-hmac-v2")
}

// portalActor constructs the pseudonymous public guest audit actor without recording
// IP or user-agent telemetry hashes while Section 7.2 #3 retention remains open.
func portalActor(meta Actor) Actor {
	return Actor{
		UserID:    uuid.Nil,
		Email:     "guest@public.lovelace",
		Role:      "public_guest",
		RequestID: meta.RequestID,
	}
}

func (s *Service) portalPublicBaseURL(prop domain.PublicPropertyView) string {
	if dom := strings.TrimSpace(prop.CustomDomain); dom != "" {
		if strings.HasPrefix(dom, "http://") || strings.HasPrefix(dom, "https://") {
			return strings.TrimRight(dom, "/")
		}
		return "https://" + strings.TrimRight(dom, "/")
	}
	if orig := strings.TrimSpace(s.cfg.PublicOrigin); orig != "" {
		parts := strings.Split(orig, ",")
		return strings.TrimRight(strings.TrimSpace(parts[0]), "/")
	}
	return "http://127.0.0.1:3000"
}

func (s *Service) resolvePortalProperty(ctx context.Context, db postgres.DBTX, slug string) (domain.PublicPropertyView, error) {
	slug = strings.TrimSpace(strings.ToLower(slug))
	if slug == "" {
		return domain.PublicPropertyView{}, domain.ErrNotFound
	}
	if !s.cfg.GuestPortalEnabled {
		return domain.PublicPropertyView{}, ErrPortalDisabled
	}
	prop, err := s.store.GetPublicPropertyBySlug(ctx, db, slug)
	if err != nil {
		return domain.PublicPropertyView{}, err
	}
	if !prop.Policy.GuestPortalEnabled {
		return domain.PublicPropertyView{}, ErrPortalDisabled
	}
	s.maskPublicPropertyPortalFlag(&prop)
	return prop, nil
}

// PruneExpiredPortalState runs safe credential cleanup for expired/consumed/revoked challenges and
// sessions while preserving audit_logs evidence (Section 7.2 #3).
func (s *Service) PruneExpiredPortalState(ctx context.Context) error {
	return s.store.PruneExpiredPortalState(ctx, s.nowUTC())
}

// queuePortalSecurityChallengeEmailTx queues a single-use OTP / magic-link challenge directly
// into email_outbox with communication_id = NULL and reservation_id = NULL so raw credentials
// never enter staff-readable guest_communications and never collide with uq_email_outbox_reservation_template.
func (s *Service) queuePortalSecurityChallengeEmailTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	guestID *uuid.UUID,
	recipient, templateCode, subject, bodyText, bodyHTML string,
	nowUTC time.Time,
) error {
	cleanRecip, err := domain.ValidateEmailRecipient(recipient)
	if err != nil {
		return err
	}
	cleanSubj, err := domain.SanitizeEmailHeaderValue(subject)
	if err != nil || cleanSubj == "" {
		return fmt.Errorf("invalid email subject")
	}
	senderFrom := strings.TrimSpace(s.cfg.EmailFrom)
	if senderFrom == "" {
		senderFrom = "bookings@tirbach.wales"
	}
	if _, err := mailer.ValidateMessage(mailer.Message{
		From:     senderFrom,
		To:       cleanRecip,
		Subject:  cleanSubj,
		BodyText: bodyText,
		BodyHTML: bodyHTML,
	}); err != nil {
		return err
	}
	outboxID, err := postgres.NewUUID()
	if err != nil {
		return err
	}
	_, err = s.store.QueueEmail(ctx, tx, domain.EmailOutboxItem{
		ID:              outboxID,
		PropertyID:      propertyID,
		ReservationID:   nil,
		GuestID:         guestID,
		CommunicationID: nil,
		RecipientEmail:  cleanRecip,
		Template:        templateCode,
		Subject:         cleanSubj,
		BodyText:        bodyText,
		BodyHTML:        bodyHTML,
		NextAttemptAt:   nowUTC,
		CreatedAt:       nowUTC,
	})
	return err
}

// queuePortalOperationalNotificationTx logs a non-secret operational notification in guest_communications
// (with reservation_id/enquiry_id linked on guest_communications) and queues email_outbox with
// reservation_id = NULL + communication_id = &commID (Section 7.1 #2).
func (s *Service) queuePortalOperationalNotificationTx(
	ctx context.Context,
	tx pgx.Tx,
	propertyID, canonicalGuestID uuid.UUID,
	reservationID, enquiryID *uuid.UUID,
	recipient, templateCode, subject, summary, bodyText, bodyHTML string,
	nowUTC time.Time,
) error {
	cleanRecip, err := domain.ValidateEmailRecipient(recipient)
	if err != nil || domain.IsOTAProxyEmail(cleanRecip) {
		return nil
	}
	cleanSubj, err := domain.SanitizeEmailHeaderValue(subject)
	if err != nil || cleanSubj == "" {
		return fmt.Errorf("invalid email subject")
	}
	senderFrom := strings.TrimSpace(s.cfg.EmailFrom)
	if senderFrom == "" {
		senderFrom = "bookings@tirbach.wales"
	}
	if _, err := mailer.ValidateMessage(mailer.Message{
		From:     senderFrom,
		To:       cleanRecip,
		Subject:  cleanSubj,
		BodyText: bodyText,
		BodyHTML: bodyHTML,
	}); err != nil {
		return err
	}
	commID, err := postgres.NewUUID()
	if err != nil {
		return err
	}
	outboxID, err := postgres.NewUUID()
	if err != nil {
		return err
	}
	if _, err := s.store.CreateGuestCommunication(ctx, tx, domain.GuestCommunication{
		ID:              commID,
		PropertyID:      propertyID,
		GuestID:         &canonicalGuestID,
		ReservationID:   reservationID,
		EnquiryID:       enquiryID,
		InteractionType: domain.InteractionEmailOutbound,
		Direction:       domain.CommDirOutbound,
		Subject:         cleanSubj,
		BodyText:        bodyText,
		BodyHTML:        bodyHTML,
		Summary:         summary,
		RecipientEmail:  cleanRecip,
		SenderEmail:     senderFrom,
		DeliveryStatus:  domain.DeliveryQueued,
		StaffUserID:     nil,
		OccurredAt:      nowUTC,
	}); err != nil {
		return err
	}
	if _, err := s.store.QueueEmail(ctx, tx, domain.EmailOutboxItem{
		ID:              outboxID,
		PropertyID:      propertyID,
		ReservationID:   nil,
		GuestID:         &canonicalGuestID,
		CommunicationID: &commID,
		RecipientEmail:  cleanRecip,
		Template:        templateCode,
		Subject:         cleanSubj,
		BodyText:        bodyText,
		BodyHTML:        bodyHTML,
		NextAttemptAt:   nowUTC,
		CreatedAt:       nowUTC,
	}); err != nil {
		return err
	}
	return s.store.LinkCommunicationOutbox(ctx, tx, commID, outboxID)
}

// --- Portal Auth Challenge & Session Issuance (Owner-Confirmed D1, D2, D3, D8, D9) ---

type RequestPortalChallengeInput struct {
	PropertySlug     string
	Email            string
	Purpose          domain.PortalChallengePurpose
	BookingReference string
}

type PortalChallengeInitiated struct {
	Accepted         bool   `json:"accepted"`
	BrowserNonce     string `json:"browser_nonce,omitempty"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

// RequestPortalAuthChallenge initiates a uniform non-enumerating Tier B passwordless OTP /
// browser-bound magic-link challenge (D1).
func (s *Service) RequestPortalAuthChallenge(
	ctx context.Context,
	meta Actor,
	in RequestPortalChallengeInput,
) (PortalChallengeInitiated, error) {
	prop, err := s.resolvePortalProperty(ctx, nil, in.PropertySlug)
	if err != nil {
		return PortalChallengeInitiated{}, err
	}

	purpose := in.Purpose
	if purpose == "" {
		purpose = domain.PortalChallengeLogin
	}
	if purpose != domain.PortalChallengeLogin {
		return PortalChallengeInitiated{}, domain.NewValidationError(map[string]string{
			"purpose": "must be LOGIN",
		})
	}
	cleanEmail, err := domain.ValidateEmailRecipient(in.Email)
	if err != nil {
		return PortalChallengeInitiated{}, domain.NewValidationError(map[string]string{
			"email": "must be a valid email address",
		})
	}
	normEmail := domain.NormalizeEmail(cleanEmail)
	bookingRef := strings.ToUpper(strings.TrimSpace(in.BookingReference))
	otpKey, err := s.portalOTPHMACKey()
	if err != nil {
		return PortalChallengeInitiated{}, err
	}

	nowUTC := s.nowUTC()
	_ = s.store.PruneExpiredPortalState(ctx, nowUTC)

	// Always generate browserNonce so response shape is identical regardless of match state.
	browserNonce, err := postgres.GeneratePortalRandomHex(32)
	if err != nil {
		return PortalChallengeInitiated{}, err
	}
	resp := PortalChallengeInitiated{
		Accepted:         true,
		BrowserNonce:     browserNonce,
		ExpiresInSeconds: int(domain.PortalChallengeTTL.Seconds()),
	}

	if domain.IsOTAProxyEmail(normEmail) {
		return resp, nil
	}

	if err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var targetIdentityID, targetGuestID *uuid.UUID
		var sendChallenge bool

		if ident, iErr := s.store.GetPortalIdentityByVerifiedEmail(ctx, tx, prop.ID, normEmail); iErr == nil {
			if ident.Status != domain.PortalIdentityActive {
				return nil
			}
			canonID, _, anonOrPending, gErr := s.store.LockCanonicalGuestClusterTx(ctx, tx, prop.ID, ident.CanonicalGuestID)
			if gErr == nil && !anonOrPending {
				guest, gErr2 := s.store.GetGuest(ctx, tx, prop.ID, canonID)
				if gErr2 == nil && guest.Status != domain.GuestStatusAnonymized && guest.AnonymizedAt == nil && guest.ErasureRequestedAt == nil {
					idCopy := ident.ID
					targetIdentityID = &idCopy
					targetGuestID = &canonID
					sendChallenge = true
				}
			}
		} else {
			matches, mErr := s.store.ListActiveGuestsByNormalizedEmail(ctx, tx, prop.ID, normEmail)
			if mErr == nil && len(matches) > 0 {
				if len(matches) == 1 {
					gid := matches[0].ID
					targetGuestID = &gid
				}
				sendChallenge = true
			}
		}

		if !sendChallenge {
			return nil
		}

		if err := s.store.InvalidateActivePortalChallengesTx(ctx, tx, prop.ID, normEmail, purpose, nowUTC); err != nil {
			return err
		}
		chID, err := postgres.NewUUID()
		if err != nil {
			return err
		}
		otpPlain, err := postgres.GeneratePortalOTPCode()
		if err != nil {
			return err
		}
		magicToken, err := postgres.GeneratePortalRandomHex(32)
		if err != nil {
			return err
		}
		otpHash, err := postgres.HashPortalOTP(otpKey, chID, normEmail, otpPlain)
		if err != nil {
			return err
		}
		if _, err := s.store.CreatePortalChallengeTx(ctx, tx, domain.GuestPortalChallenge{
			ID:               chID,
			PropertyID:       prop.ID,
			PortalIdentityID: targetIdentityID,
			GuestID:          targetGuestID,
			NormalizedEmail:  normEmail,
			Purpose:          purpose,
			OTPHash:          otpHash,
			OTPHashAlg:       domain.PortalOTPHashAlgHMACSHA256V1,
			TokenHash:        postgres.HashPortalValue(magicToken),
			BrowserNonceHash: postgres.HashPortalValue(browserNonce),
			BookingReference: bookingRef,
			MaxAttempts:      domain.PortalChallengeMaxAttempts,
			ExpiresAt:        nowUTC.Add(domain.PortalChallengeTTL),
		}); err != nil {
			return err
		}

		if targetGuestID != nil {
			auditResID := *targetGuestID
			if targetIdentityID != nil {
				auditResID = *targetIdentityID
			}
			if err := s.recordAudit(ctx, tx, portalActor(meta), &prop.ID, "guest_portal_identity.challenge_issued", "guest_portal_identity", auditResID.String(), nil, map[string]any{
				"id":           auditResID,
				"property_id":  prop.ID,
				"guest_id":     *targetGuestID,
				"status":       string(domain.PortalIdentityActive),
				"purpose":      string(purpose),
				"challenge_id": chID,
				"created_at":   nowUTC,
				"updated_at":   nowUTC,
			}); err != nil {
				return err
			}
		}

		verifyURL := fmt.Sprintf("%s/portal/verify#t=%s", s.portalPublicBaseURL(prop), magicToken)
		subj := fmt.Sprintf("%s — Your Guest Portal Verification Code", prop.Name)
		bodyText := fmt.Sprintf(
			"Hello,\n\nYour %s Guest Portal verification code is: %s\n\nThis single-use code expires in 15 minutes.\n\nIf you requested sign-in from this browser, you can also complete verification here:\n%s\n\nIf you did not request this code, you can safely ignore this email.\n",
			prop.Name, otpPlain, verifyURL,
		)
		bodyHTML := fmt.Sprintf(
			"<p>Hello,</p><p>Your <strong>%s</strong> Guest Portal verification code is:</p><p style=\"font-size:24px;font-family:monospace;letter-spacing:4px;\"><strong>%s</strong></p><p>This single-use code expires in 15 minutes.</p><p>If you requested sign-in from the same browser, you may also <a href=\"%s\">complete sign-in here</a>.</p><p>If you did not request this code, you can safely ignore this email.</p>",
			html.EscapeString(prop.Name), html.EscapeString(otpPlain), html.EscapeString(verifyURL),
		)
		return s.queuePortalSecurityChallengeEmailTx(ctx, tx, prop.ID, targetGuestID, cleanEmail, "portal_verification", subj, bodyText, bodyHTML, nowUTC)
	}); err != nil {
		return PortalChallengeInitiated{}, err
	}

	return resp, nil
}

type RedeemPortalChallengeInput struct {
	PropertySlug     string
	Email            string
	OTPCode          string
	MagicToken       string
	BrowserNonce     string
	BookingReference string
}

type PortalLoginResult struct {
	SessionToken string                   `json:"session_token"`
	Session      domain.PortalSessionView `json:"session"`
}

// RedeemPortalChallenge verifies a single-use OTP or browser-bound magic link and mints an
// isolated Tier B (ACCOUNT) GuestPortalSession (D1, D2, D3, D8, D9).
func (s *Service) RedeemPortalChallenge(
	ctx context.Context,
	meta Actor,
	in RedeemPortalChallengeInput,
) (PortalLoginResult, error) {
	prop, err := s.resolvePortalProperty(ctx, nil, in.PropertySlug)
	if err != nil {
		return PortalLoginResult{}, err
	}
	otpCode := strings.TrimSpace(in.OTPCode)
	magicToken := strings.TrimSpace(in.MagicToken)
	if otpCode == "" && magicToken == "" {
		return PortalLoginResult{}, domain.NewValidationError(map[string]string{
			"code": "verification code or sign-in link token is required",
		})
	}
	normEmail := domain.NormalizeEmail(in.Email)
	if magicToken == "" && normEmail == "" {
		return PortalLoginResult{}, domain.NewValidationError(map[string]string{
			"email": "email is required when verifying with a 6-digit code",
		})
	}
	otpKey, err := s.portalOTPHMACKey()
	if err != nil {
		return PortalLoginResult{}, err
	}
	csrfKey, err := s.portalCSRFHMACKey()
	if err != nil {
		return PortalLoginResult{}, err
	}

	nowUTC := s.nowUTC()
	var failedChallengeID uuid.UUID
	var out PortalLoginResult

	txErr := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ch domain.GuestPortalChallenge
		var lErr error
		if magicToken != "" && otpCode == "" {
			ch, lErr = s.store.LockPortalChallengeByTokenHashTx(ctx, tx, prop.ID, postgres.HashPortalValue(magicToken), nowUTC)
		} else {
			ch, lErr = s.store.LockLatestPortalChallengeByEmailTx(ctx, tx, prop.ID, normEmail, domain.PortalChallengeLogin, nowUTC)
		}
		if lErr != nil {
			return domain.ErrUnauthorized
		}
		if ch.Purpose != domain.PortalChallengeLogin {
			return domain.ErrUnauthorized
		}
		proofOK, pErr := postgres.VerifyChallengeProof(otpKey, ch, otpCode, magicToken, in.BrowserNonce)
		if pErr != nil {
			return pErr
		}
		if !proofOK {
			failedChallengeID = ch.ID
			return domain.ErrUnauthorized
		}

		corroboratingRef := strings.ToUpper(strings.TrimSpace(in.BookingReference))
		if corroboratingRef == "" {
			corroboratingRef = strings.ToUpper(strings.TrimSpace(ch.BookingReference))
		}

		activeMatches, mErr := s.store.ListActiveGuestsByNormalizedEmail(ctx, tx, prop.ID, ch.NormalizedEmail)
		if mErr != nil {
			return domain.ErrUnauthorized
		}

		var canonicalGuestID uuid.UUID
		if ident, iErr := s.store.GetPortalIdentityByVerifiedEmail(ctx, tx, prop.ID, ch.NormalizedEmail); iErr == nil {
			if ident.Status != domain.PortalIdentityActive {
				return domain.ErrUnauthorized
			}
			canonID, _, anonOrPending, gErr := s.store.LockCanonicalGuestClusterTx(ctx, tx, prop.ID, ident.CanonicalGuestID)
			if gErr != nil || anonOrPending {
				return domain.ErrUnauthorized
			}
			// Decision D3: If >1 distinct active unmerged guest rows share this email, require
			// a corroborating booking reference belonging to canonID until CRM merges duplicates.
			distinctOther := false
			for _, mg := range activeMatches {
				if mg.ID != canonID {
					distinctOther = true
					break
				}
			}
			if distinctOther {
				if corroboratingRef == "" {
					return ErrPortalDuplicateDisambiguation
				}
				res, rErr := s.store.GetReservationByReference(ctx, tx, prop.ID, corroboratingRef)
				if rErr != nil {
					failedChallengeID = ch.ID
					return domain.ErrUnauthorized
				}
				resCanon, cErr := s.store.ResolveCanonicalGuestID(ctx, tx, prop.ID, res.GuestID)
				if cErr != nil {
					return domain.ErrUnauthorized
				}
				if resCanon != canonID {
					// Another distinct unmerged profile shares this email; refuse auto-link until CRM merge.
					return ErrPortalDuplicateDisambiguation
				}
			} else if corroboratingRef != "" {
				res, rErr := s.store.GetReservationByReference(ctx, tx, prop.ID, corroboratingRef)
				if rErr != nil {
					failedChallengeID = ch.ID
					return domain.ErrUnauthorized
				}
				resCanon, cErr := s.store.ResolveCanonicalGuestID(ctx, tx, prop.ID, res.GuestID)
				if cErr != nil || resCanon != canonID {
					failedChallengeID = ch.ID
					return domain.ErrUnauthorized
				}
			}
			canonicalGuestID = canonID
		} else {
			if len(activeMatches) == 0 {
				return domain.ErrUnauthorized
			}
			if len(activeMatches) == 1 {
				if corroboratingRef != "" {
					res, rErr := s.store.GetReservationByReference(ctx, tx, prop.ID, corroboratingRef)
					if rErr != nil {
						failedChallengeID = ch.ID
						return domain.ErrUnauthorized
					}
					resCanon, cErr := s.store.ResolveCanonicalGuestID(ctx, tx, prop.ID, res.GuestID)
					if cErr != nil || resCanon != activeMatches[0].ID {
						failedChallengeID = ch.ID
						return domain.ErrUnauthorized
					}
				}
				canonicalGuestID = activeMatches[0].ID
			} else {
				// Decision D3: Multiple unmerged active guest rows share this email.
				if corroboratingRef == "" {
					return ErrPortalDuplicateDisambiguation
				}
				res, rErr := s.store.GetReservationByReference(ctx, tx, prop.ID, corroboratingRef)
				if rErr != nil {
					failedChallengeID = ch.ID
					return domain.ErrUnauthorized
				}
				resCanon, cErr := s.store.ResolveCanonicalGuestID(ctx, tx, prop.ID, res.GuestID)
				if cErr != nil {
					return domain.ErrUnauthorized
				}
				matched := false
				for _, mg := range activeMatches {
					if mg.ID == resCanon {
						matched = true
						break
					}
				}
				if !matched {
					failedChallengeID = ch.ID
					return domain.ErrUnauthorized
				}
				canonicalGuestID = resCanon
			}
		}

		canonID, _, anonOrPending, gErr := s.store.LockCanonicalGuestClusterTx(ctx, tx, prop.ID, canonicalGuestID)
		if gErr != nil || anonOrPending {
			return domain.ErrUnauthorized
		}
		guest, gErr := s.store.GetGuest(ctx, tx, prop.ID, canonID)
		if gErr != nil || guest.Status == domain.GuestStatusAnonymized || guest.AnonymizedAt != nil || guest.ErasureRequestedAt != nil {
			return domain.ErrUnauthorized
		}

		if err := s.store.ConsumePortalChallengeTx(ctx, tx, prop.ID, ch.ID, ch.NormalizedEmail, nowUTC); err != nil {
			return err
		}
		ident, err := s.store.UpsertPortalIdentityOnLoginTx(ctx, tx, prop.ID, canonID, ch.NormalizedEmail, nowUTC)
		if err != nil {
			return err
		}

		sessionID, err := postgres.NewUUID()
		if err != nil {
			return err
		}
		plainSession, err := postgres.GeneratePortalRandomHex(32)
		if err != nil {
			return err
		}
		sessHash := postgres.HashPortalValue(plainSession)
		csrfPlain, csrfHash, err := postgres.DerivePortalCSRFToken(csrfKey, sessionID, sessHash)
		if err != nil {
			return err
		}

		sess, err := s.store.CreatePortalSessionTx(ctx, tx, domain.GuestPortalSession{
			ID:                sessionID,
			PropertyID:        prop.ID,
			PortalIdentityID:  &ident.ID,
			ScopeType:         domain.PortalScopeAccount,
			SessionTokenHash:  sessHash,
			CSRFSecretHash:    csrfHash,
			VerifiedAt:        nowUTC,
			IdleExpiresAt:     nowUTC.Add(domain.PortalTierBIdleTTL),
			AbsoluteExpiresAt: nowUTC.Add(domain.PortalTierBAbsoluteTTL),
		})
		if err != nil {
			return err
		}
		if err := s.recordAudit(ctx, tx, portalActor(meta), &prop.ID, "guest_portal_identity.verified", "guest_portal_identity", ident.ID.String(), nil, map[string]any{
			"id":                ident.ID,
			"property_id":       prop.ID,
			"guest_id":          canonID,
			"status":            string(ident.Status),
			"purpose":           string(ch.Purpose),
			"challenge_id":      ch.ID,
			"version":           ident.Version,
			"email_verified_at": ident.EmailVerifiedAt,
			"created_at":        ident.CreatedAt,
			"updated_at":        ident.UpdatedAt,
		}); err != nil {
			return err
		}
		if err := s.recordAudit(ctx, tx, portalActor(meta), &prop.ID, "guest_portal_session.created", "guest_portal_session", sess.ID.String(), nil, map[string]any{
			"id":                  sess.ID,
			"session_id":          sess.ID,
			"property_id":         prop.ID,
			"portal_identity_id":  ident.ID,
			"scope_type":          string(sess.ScopeType),
			"idle_expires_at":     sess.IdleExpiresAt,
			"absolute_expires_at": sess.AbsoluteExpiresAt,
			"created_at":          sess.CreatedAt,
		}); err != nil {
			return err
		}

		consents, _ := s.store.ListGuestConsentRecords(ctx, tx, prop.ID, canonID)
		consentState := domain.ConsentNotSubscribed
		if len(consents) > 0 {
			consentState = consents[0].Status
		}
		if guest.Status == domain.GuestStatusDoNotContact {
			consentState = domain.ConsentWithdrawn
		}
		lang := guest.PreferredLanguage
		if lang == "" {
			lang = "en"
		}
		out = PortalLoginResult{
			SessionToken: plainSession,
			Session: domain.PortalSessionView{
				Authenticated:     true,
				PropertySlug:      prop.Slug,
				PropertyName:      prop.Name,
				ScopeType:         domain.PortalScopeAccount,
				CSRFToken:         csrfPlain,
				VerifiedAt:        sess.VerifiedAt,
				StepUpFresh:       true,
				IdleExpiresAt:     sess.IdleExpiresAt,
				AbsoluteExpiresAt: sess.AbsoluteExpiresAt,
				Guest: &domain.PortalSessionGuestSummary{
					FullName:              guest.FullName,
					VerifiedEmail:         ident.VerifiedEmail,
					PendingEmail:          activePendingEmail(ident, nowUTC),
					PreferredLanguage:     lang,
					IsDoNotContact:        guest.Status == domain.GuestStatusDoNotContact,
					MarketingConsentState: string(consentState),
				},
			},
		}
		return nil
	})

	if failedChallengeID != uuid.Nil {
		_ = s.store.IncrementPortalChallengeFailure(ctx, prop.ID, failedChallengeID, nowUTC)
	}
	if txErr != nil {
		return PortalLoginResult{}, txErr
	}
	return out, nil
}

// CreatePortalBookingTokenSession exchanges a direct booking's reference + confirmation token
// for a Tier A (BOOKING-scoped) portal session limited to that exact booking without elevating
// to Tier B account access (D1, D8).
func (s *Service) CreatePortalBookingTokenSession(
	ctx context.Context,
	meta Actor,
	propertySlug, bookingRef, confirmationToken string,
) (PortalLoginResult, error) {
	prop, err := s.resolvePortalProperty(ctx, nil, propertySlug)
	if err != nil {
		return PortalLoginResult{}, err
	}
	if strings.TrimSpace(bookingRef) == "" || strings.TrimSpace(confirmationToken) == "" {
		return PortalLoginResult{}, domain.NewValidationError(map[string]string{
			"booking_reference": "booking_reference and confirmation_token are required",
		})
	}
	csrfKey, err := s.portalCSRFHMACKey()
	if err != nil {
		return PortalLoginResult{}, err
	}
	nowUTC := s.nowUTC()
	var out PortalLoginResult
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		res, tokExpiresAt, vErr := s.store.VerifyConfirmationTokenForPortalTx(ctx, tx, prop.ID, bookingRef, confirmationToken, nowUTC)
		if vErr != nil {
			return domain.ErrUnauthorized
		}
		sessionID, err := postgres.NewUUID()
		if err != nil {
			return err
		}
		plainSession, err := postgres.GeneratePortalRandomHex(32)
		if err != nil {
			return err
		}
		sessHash := postgres.HashPortalValue(plainSession)
		csrfPlain, csrfHash, err := postgres.DerivePortalCSRFToken(csrfKey, sessionID, sessHash)
		if err != nil {
			return err
		}

		absExp := nowUTC.Add(domain.PortalTierAAbsoluteTTL)
		if tokExpiresAt.Before(absExp) {
			absExp = tokExpiresAt
		}
		idleExp := nowUTC.Add(domain.PortalTierAIdleTTL)
		if absExp.Before(idleExp) {
			idleExp = absExp
		}

		sess, err := s.store.CreatePortalSessionTx(ctx, tx, domain.GuestPortalSession{
			ID:                  sessionID,
			PropertyID:          prop.ID,
			ScopedReservationID: &res.ID,
			ScopeType:           domain.PortalScopeBooking,
			SessionTokenHash:    sessHash,
			CSRFSecretHash:      csrfHash,
			VerifiedAt:          nowUTC,
			IdleExpiresAt:       idleExp,
			AbsoluteExpiresAt:   absExp,
		})
		if err != nil {
			return err
		}
		if err := s.recordAudit(ctx, tx, portalActor(meta), &prop.ID, "guest_portal_session.created", "guest_portal_session", sess.ID.String(), nil, map[string]any{
			"id":                    sess.ID,
			"session_id":            sess.ID,
			"property_id":           prop.ID,
			"scoped_reservation_id": res.ID,
			"scope_type":            string(sess.ScopeType),
			"idle_expires_at":       sess.IdleExpiresAt,
			"absolute_expires_at":   sess.AbsoluteExpiresAt,
			"created_at":            sess.CreatedAt,
		}); err != nil {
			return err
		}
		out = PortalLoginResult{
			SessionToken: plainSession,
			Session: domain.PortalSessionView{
				Authenticated:          true,
				PropertySlug:           prop.Slug,
				PropertyName:           prop.Name,
				ScopeType:              domain.PortalScopeBooking,
				ScopedBookingReference: res.Reference,
				CSRFToken:              csrfPlain,
				VerifiedAt:             sess.VerifiedAt,
				StepUpFresh:            false,
				IdleExpiresAt:          sess.IdleExpiresAt,
				AbsoluteExpiresAt:      sess.AbsoluteExpiresAt,
			},
		}
		return nil
	})
	return out, err
}

type resolvedPortalAuth struct {
	Property       domain.PublicPropertyView
	Session        domain.GuestPortalSession
	CSRFToken      string
	Identity       *domain.GuestPortalIdentity
	CanonicalGuest domain.Guest
	ClusterIDs     []uuid.UUID
	ScopedRes      *domain.Reservation
}

func activePendingEmail(ident domain.GuestPortalIdentity, nowUTC time.Time) string {
	if ident.PendingEmail != "" && ident.PendingEmailExpiresAt != nil && ident.PendingEmailExpiresAt.After(nowUTC) {
		return ident.PendingEmail
	}
	return ""
}

func (s *Service) authenticatePortalSession(
	ctx context.Context,
	propertySlug, plainSessionToken, csrfHeader string,
	requireCSRF bool,
) (resolvedPortalAuth, error) {
	prop, err := s.resolvePortalProperty(ctx, nil, propertySlug)
	if err != nil {
		return resolvedPortalAuth{}, err
	}
	csrfKey, err := s.portalCSRFHMACKey()
	if err != nil {
		return resolvedPortalAuth{}, err
	}
	nowUTC := s.nowUTC()
	sess, err := s.store.AuthenticateAndSlidePortalSession(ctx, prop.ID, plainSessionToken, nowUTC)
	if err != nil {
		return resolvedPortalAuth{}, domain.ErrUnauthorized
	}

	csrfPlain, csrfExpectedHash, err := postgres.DerivePortalCSRFToken(csrfKey, sess.ID, sess.SessionTokenHash)
	if err != nil {
		return resolvedPortalAuth{}, err
	}
	if !auth.ConstantTimeEqual(csrfExpectedHash, sess.CSRFSecretHash) {
		return resolvedPortalAuth{}, domain.ErrUnauthorized
	}
	if requireCSRF {
		providedHash := postgres.HashPortalValue(csrfHeader)
		if strings.TrimSpace(csrfHeader) == "" || !auth.ConstantTimeEqual(providedHash, sess.CSRFSecretHash) {
			return resolvedPortalAuth{}, ErrPortalCSRFInvalid
		}
	}

	revokeInvalid := func() {
		_ = s.store.WithTx(ctx, func(tx pgx.Tx) error {
			_, _, _ = s.store.RevokePortalSessionByTokenHashTx(ctx, tx, prop.ID, sess.SessionTokenHash, nowUTC)
			return nil
		})
	}

	switch sess.ScopeType {
	case domain.PortalScopeBooking:
		if sess.ScopedReservationID == nil {
			revokeInvalid()
			return resolvedPortalAuth{}, domain.ErrUnauthorized
		}
		res, err := s.store.GetReservation(ctx, nil, prop.ID, *sess.ScopedReservationID)
		if err != nil {
			revokeInvalid()
			return resolvedPortalAuth{}, domain.ErrUnauthorized
		}
		canonID, clusterIDs, err := s.store.GetGuestClusterIDs(ctx, nil, prop.ID, res.GuestID)
		if err != nil {
			revokeInvalid()
			return resolvedPortalAuth{}, domain.ErrUnauthorized
		}
		guest, err := s.store.GetGuest(ctx, nil, prop.ID, canonID)
		if err != nil || guest.Status == domain.GuestStatusAnonymized || guest.AnonymizedAt != nil || guest.ErasureRequestedAt != nil {
			revokeInvalid()
			return resolvedPortalAuth{}, domain.ErrUnauthorized
		}
		return resolvedPortalAuth{
			Property:       prop,
			Session:        sess,
			CSRFToken:      csrfPlain,
			CanonicalGuest: guest,
			ClusterIDs:     clusterIDs,
			ScopedRes:      &res,
		}, nil

	case domain.PortalScopeAccount:
		if sess.PortalIdentityID == nil {
			revokeInvalid()
			return resolvedPortalAuth{}, domain.ErrUnauthorized
		}
		ident, err := s.store.GetPortalIdentityByID(ctx, nil, prop.ID, *sess.PortalIdentityID)
		if err != nil || ident.Status != domain.PortalIdentityActive {
			revokeInvalid()
			return resolvedPortalAuth{}, domain.ErrUnauthorized
		}
		canonID, clusterIDs, err := s.store.GetGuestClusterIDs(ctx, nil, prop.ID, ident.CanonicalGuestID)
		if err != nil {
			revokeInvalid()
			return resolvedPortalAuth{}, domain.ErrUnauthorized
		}
		guest, err := s.store.GetGuest(ctx, nil, prop.ID, canonID)
		if err != nil || guest.Status == domain.GuestStatusAnonymized || guest.AnonymizedAt != nil || guest.ErasureRequestedAt != nil {
			revokeInvalid()
			return resolvedPortalAuth{}, domain.ErrUnauthorized
		}
		return resolvedPortalAuth{
			Property:       prop,
			Session:        sess,
			CSRFToken:      csrfPlain,
			Identity:       &ident,
			CanonicalGuest: guest,
			ClusterIDs:     clusterIDs,
		}, nil
	}

	revokeInvalid()
	return resolvedPortalAuth{}, domain.ErrUnauthorized
}

func (s *Service) GetPortalSessionView(ctx context.Context, propertySlug, plainSessionToken string) (domain.PortalSessionView, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, "", false)
	if err != nil {
		return domain.PortalSessionView{}, err
	}
	nowUTC := s.nowUTC()
	stepUpFresh := authCtx.Session.ScopeType == domain.PortalScopeAccount &&
		nowUTC.Sub(authCtx.Session.VerifiedAt) <= domain.PortalStepUpFreshnessWindow
	view := domain.PortalSessionView{
		Authenticated:     true,
		PropertySlug:      authCtx.Property.Slug,
		PropertyName:      authCtx.Property.Name,
		ScopeType:         authCtx.Session.ScopeType,
		CSRFToken:         authCtx.CSRFToken,
		VerifiedAt:        authCtx.Session.VerifiedAt,
		StepUpFresh:       stepUpFresh,
		IdleExpiresAt:     authCtx.Session.IdleExpiresAt,
		AbsoluteExpiresAt: authCtx.Session.AbsoluteExpiresAt,
	}
	if authCtx.ScopedRes != nil {
		view.ScopedBookingReference = authCtx.ScopedRes.Reference
	}
	if authCtx.Identity != nil {
		consents, _ := s.store.ListGuestConsentRecords(ctx, nil, authCtx.Property.ID, authCtx.CanonicalGuest.ID)
		consentState := domain.ConsentNotSubscribed
		if len(consents) > 0 {
			consentState = consents[0].Status
		}
		if authCtx.CanonicalGuest.Status == domain.GuestStatusDoNotContact {
			consentState = domain.ConsentWithdrawn
		}
		lang := authCtx.CanonicalGuest.PreferredLanguage
		if lang == "" {
			lang = "en"
		}
		view.Guest = &domain.PortalSessionGuestSummary{
			FullName:              authCtx.CanonicalGuest.FullName,
			VerifiedEmail:         authCtx.Identity.VerifiedEmail,
			PendingEmail:          activePendingEmail(*authCtx.Identity, nowUTC),
			PreferredLanguage:     lang,
			IsDoNotContact:        authCtx.CanonicalGuest.Status == domain.GuestStatusDoNotContact,
			MarketingConsentState: string(consentState),
		}
	}
	return view, nil
}

func (s *Service) LogoutPortalSession(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader string,
) error {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			return nil
		}
		return err
	}
	nowUTC := s.nowUTC()
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		sessID, revoked, err := s.store.RevokePortalSessionByTokenHashTx(ctx, tx, authCtx.Property.ID, authCtx.Session.SessionTokenHash, nowUTC)
		if err != nil || !revoked {
			return err
		}
		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest_portal_session.revoked", "guest_portal_session", sessID.String(), nil, map[string]any{
			"id":                    sessID,
			"session_id":            sessID,
			"property_id":           authCtx.Property.ID,
			"portal_identity_id":    authCtx.Session.PortalIdentityID,
			"scoped_reservation_id": authCtx.Session.ScopedReservationID,
			"scope_type":            string(authCtx.Session.ScopeType),
			"idle_expires_at":       authCtx.Session.IdleExpiresAt,
			"absolute_expires_at":   authCtx.Session.AbsoluteExpiresAt,
			"revoked_at":            nowUTC,
			"created_at":            authCtx.Session.CreatedAt,
		})
	})
	_ = s.store.PruneExpiredPortalState(ctx, nowUTC)
	return err
}

// RequestPortalStepUpChallenge sends a fresh 6-digit STEP_UP verification code to the
// authenticated Tier B guest's verified email so high-sensitivity actions (email change,
// SAR, erasure) verify inbox possession within <= 15 minutes (Decision D8).
func (s *Service) RequestPortalStepUpChallenge(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader string,
) error {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return err
	}
	if authCtx.Session.ScopeType != domain.PortalScopeAccount || authCtx.Identity == nil {
		return domain.ErrForbidden
	}
	otpKey, err := s.portalOTPHMACKey()
	if err != nil {
		return err
	}
	nowUTC := s.nowUTC()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.InvalidateActivePortalChallengesTx(ctx, tx, authCtx.Property.ID, authCtx.Identity.NormalizedVerifiedEmail, domain.PortalChallengeStepUp, nowUTC); err != nil {
			return err
		}
		chID, err := postgres.NewUUID()
		if err != nil {
			return err
		}
		otpPlain, err := postgres.GeneratePortalOTPCode()
		if err != nil {
			return err
		}
		tokDummy, err := postgres.GeneratePortalRandomHex(32)
		if err != nil {
			return err
		}
		otpHash, err := postgres.HashPortalOTP(otpKey, chID, authCtx.Identity.NormalizedVerifiedEmail, otpPlain)
		if err != nil {
			return err
		}
		gid := authCtx.CanonicalGuest.ID
		identID := authCtx.Identity.ID
		if _, err := s.store.CreatePortalChallengeTx(ctx, tx, domain.GuestPortalChallenge{
			ID:               chID,
			PropertyID:       authCtx.Property.ID,
			PortalIdentityID: &identID,
			GuestID:          &gid,
			NormalizedEmail:  authCtx.Identity.NormalizedVerifiedEmail,
			Purpose:          domain.PortalChallengeStepUp,
			OTPHash:          otpHash,
			OTPHashAlg:       domain.PortalOTPHashAlgHMACSHA256V1,
			TokenHash:        postgres.HashPortalValue(tokDummy),
			MaxAttempts:      domain.PortalChallengeMaxAttempts,
			ExpiresAt:        nowUTC.Add(domain.PortalChallengeTTL),
		}); err != nil {
			return err
		}
		if err := s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest_portal_identity.step_up_issued", "guest_portal_identity", identID.String(), nil, map[string]any{
			"id":           identID,
			"property_id":  authCtx.Property.ID,
			"guest_id":     gid,
			"status":       string(authCtx.Identity.Status),
			"purpose":      string(domain.PortalChallengeStepUp),
			"challenge_id": chID,
			"version":      authCtx.Identity.Version,
			"created_at":   authCtx.Identity.CreatedAt,
			"updated_at":   nowUTC,
		}); err != nil {
			return err
		}
		subj := fmt.Sprintf("%s — Security Confirmation Code", authCtx.Property.Name)
		txt := fmt.Sprintf("Hello %s,\n\nYour security confirmation code is: %s\n\nThis single-use code expires in 15 minutes.\n", authCtx.CanonicalGuest.FullName, otpPlain)
		htm := fmt.Sprintf("<p>Hello %s,</p><p>Your security confirmation code is: <strong style=\"font-family:monospace;font-size:20px;\">%s</strong></p><p>This single-use code expires in 15 minutes.</p>", html.EscapeString(authCtx.CanonicalGuest.FullName), html.EscapeString(otpPlain))
		return s.queuePortalSecurityChallengeEmailTx(ctx, tx, authCtx.Property.ID, &gid, authCtx.Identity.VerifiedEmail, "portal_step_up", subj, txt, htm, nowUTC)
	})
}

func (s *Service) VerifyPortalStepUpChallenge(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader, otpCode string,
) (domain.PortalSessionView, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return domain.PortalSessionView{}, err
	}
	if authCtx.Session.ScopeType != domain.PortalScopeAccount || authCtx.Identity == nil {
		return domain.PortalSessionView{}, domain.ErrForbidden
	}
	if strings.TrimSpace(otpCode) == "" {
		return domain.PortalSessionView{}, domain.NewValidationError(map[string]string{"code": "verification code is required"})
	}
	otpKey, err := s.portalOTPHMACKey()
	if err != nil {
		return domain.PortalSessionView{}, err
	}
	nowUTC := s.nowUTC()
	var failedID uuid.UUID
	txErr := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		ch, err := s.store.LockLatestPortalChallengeByEmailTx(ctx, tx, authCtx.Property.ID, authCtx.Identity.NormalizedVerifiedEmail, domain.PortalChallengeStepUp, nowUTC)
		if err != nil {
			return domain.ErrUnauthorized
		}
		proofOK, pErr := postgres.VerifyChallengeProof(otpKey, ch, otpCode, "", "")
		if pErr != nil {
			return pErr
		}
		if !proofOK {
			failedID = ch.ID
			return domain.ErrUnauthorized
		}
		if err := s.store.ConsumePortalChallengeTx(ctx, tx, authCtx.Property.ID, ch.ID, ch.NormalizedEmail, nowUTC); err != nil {
			return err
		}
		if err := s.store.RefreshPortalSessionVerifiedAtTx(ctx, tx, authCtx.Property.ID, authCtx.Session.ID, nowUTC); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest_portal_identity.step_up_verified", "guest_portal_identity", authCtx.Identity.ID.String(), nil, map[string]any{
			"id":                authCtx.Identity.ID,
			"property_id":       authCtx.Property.ID,
			"guest_id":          authCtx.CanonicalGuest.ID,
			"status":            string(authCtx.Identity.Status),
			"purpose":           string(domain.PortalChallengeStepUp),
			"challenge_id":      ch.ID,
			"version":           authCtx.Identity.Version,
			"email_verified_at": nowUTC,
			"created_at":        authCtx.Identity.CreatedAt,
			"updated_at":        nowUTC,
		})
	})
	if failedID != uuid.Nil {
		_ = s.store.IncrementPortalChallengeFailure(ctx, authCtx.Property.ID, failedID, nowUTC)
	}
	if txErr != nil {
		return domain.PortalSessionView{}, txErr
	}
	return s.GetPortalSessionView(ctx, propertySlug, plainSessionToken)
}

// --- Portal Bookings & Self-Service Stay Management (D4, D5, D9, Section 6.1) ---

func buildPortalBookingSummary(prop domain.PublicPropertyView, r domain.Reservation) domain.PortalBookingSummary {
	isOTA := !strings.EqualFold(strings.TrimSpace(r.ChannelCode), "DIRECT")
	nights := int(r.DepartureDate.Sub(r.ArrivalDate).Hours() / 24)
	units := make([]domain.PortalBookingUnitView, 0, len(r.Units))
	for _, u := range r.Units {
		units = append(units, domain.PortalBookingUnitView{
			UnitName:     u.UnitName,
			UnitTypeName: u.UnitTypeName,
			Adults:       u.Adults,
			Children:     u.Children,
			Infants:      u.Infants,
		})
	}
	activePreStay := r.Status == domain.ResStatusPending || r.Status == domain.ResStatusConfirmed
	canArrival := !isOTA && (activePreStay || r.Status == domain.ResStatusCheckedIn)
	canChange := !isOTA && activePreStay
	canCancel := !isOTA && activePreStay && r.CancellationRequestedAt == nil

	sum := domain.PortalBookingSummary{
		Reference:             r.Reference,
		Status:                r.Status,
		ChannelCode:           r.ChannelCode,
		IsOTAManaged:          isOTA,
		ArrivalDate:           domain.FormatDate(r.ArrivalDate),
		DepartureDate:         domain.FormatDate(r.DepartureDate),
		Nights:                nights,
		CheckInTime:           prop.CheckInTime,
		CheckOutTime:          prop.CheckOutTime,
		Adults:                r.Adults,
		Children:              r.Children,
		Infants:               r.Infants,
		Units:                 units,
		EstimatedArrivalTime:  r.EstimatedArrivalTime,
		GuestArrivalNotes:     r.GuestArrivalNotes,
		CancellationRequested: r.CancellationRequestedAt != nil,
		CanUpdateArrival:      canArrival,
		CanRequestChange:      canChange,
		CanRequestCancel:      canCancel,
	}
	if isOTA {
		sum.BillingNotice = fmt.Sprintf("Managed & billed via %s. For billing receipts, date changes, or cancellations, please use your %s account.", r.ChannelCode, r.ChannelCode)
	} else {
		sum.PaymentStatus = r.PaymentStatus
		sum.TotalPence = r.TotalPence
		sum.DepositPence = r.DepositPence
		sum.AmountPaidPence = r.AmountPaidPence
		sum.BalanceDuePence = r.BalanceDuePence
		sum.Currency = r.Currency
		if sum.Currency == "" {
			sum.Currency = prop.Currency
		}
	}
	return sum
}

func buildPortalQuoteSummary(q *domain.BookingQuote) *domain.PortalQuoteSummaryView {
	if q == nil {
		return nil
	}
	var items []domain.PortalQuoteLineItemView
	accTotal := q.AccommodationSubtotalPence + q.OccupancySurchargePence
	desc := q.UnitTypeName
	if strings.TrimSpace(desc) == "" {
		desc = "Accommodation"
	}
	if q.Nights > 0 {
		unitNightly := accTotal / int64(q.Nights)
		items = append(items, domain.PortalQuoteLineItemView{
			Description: fmt.Sprintf("%s (%d night(s))", desc, q.Nights),
			Quantity:    q.Nights,
			UnitPence:   unitNightly,
			TotalPence:  accTotal,
		})
	}
	for _, ex := range q.ExtrasBreakdown {
		items = append(items, domain.PortalQuoteLineItemView{
			Description: ex.Name,
			Quantity:    ex.Quantity,
			UnitPence:   ex.UnitPence,
			TotalPence:  ex.TotalPence,
		})
	}
	return &domain.PortalQuoteSummaryView{
		Currency:      q.Currency,
		Nights:        q.Nights,
		SubtotalPence: accTotal,
		ExtrasPence:   q.ExtrasSubtotalPence,
		TaxPence:      q.TaxPence,
		TotalPence:    q.TotalPence,
		DepositPence:  q.DepositDuePence,
		BalancePence:  q.BalanceDuePence,
		LineItems:     items,
	}
}

func buildPortalPaymentSummaries(records []domain.PaymentRecord) []domain.PortalPaymentSummaryView {
	if len(records) == 0 {
		return nil
	}
	out := make([]domain.PortalPaymentSummaryView, 0, len(records))
	for _, p := range records {
		var paidPtr *time.Time
		if !p.PaidAt.IsZero() {
			t := p.PaidAt
			paidPtr = &t
		}
		out = append(out, domain.PortalPaymentSummaryView{
			AmountPence: p.AmountPence,
			Currency:    p.Currency,
			Status:      p.Status,
			PaidAt:      paidPtr,
		})
	}
	return out
}

func (s *Service) ListPortalBookings(ctx context.Context, propertySlug, plainSessionToken string) ([]domain.PortalBookingSummary, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, "", false)
	if err != nil {
		return nil, err
	}
	if authCtx.Session.ScopeType == domain.PortalScopeBooking {
		if authCtx.ScopedRes == nil || authCtx.ScopedRes.Status == domain.ResStatusInquiry {
			return []domain.PortalBookingSummary{}, nil
		}
		if !strings.EqualFold(authCtx.ScopedRes.ChannelCode, "DIRECT") && domain.IsOTAProxyEmail(authCtx.ScopedRes.GuestEmail) {
			return []domain.PortalBookingSummary{}, nil
		}
		return []domain.PortalBookingSummary{buildPortalBookingSummary(authCtx.Property, *authCtx.ScopedRes)}, nil
	}

	reservations, err := s.store.ListGuestReservationsByCluster(ctx, nil, authCtx.Property.ID, authCtx.ClusterIDs)
	if err != nil {
		return nil, err
	}
	allowOTA := authCtx.Identity != nil && !domain.IsOTAProxyEmail(authCtx.Identity.VerifiedEmail)
	out := make([]domain.PortalBookingSummary, 0, len(reservations))
	for _, r := range reservations {
		if r.Status == domain.ResStatusInquiry {
			continue
		}
		if !strings.EqualFold(r.ChannelCode, "DIRECT") && !allowOTA {
			continue
		}
		out = append(out, buildPortalBookingSummary(authCtx.Property, r))
		if len(out) >= 50 {
			break
		}
	}
	return out, nil
}

func (s *Service) authorizePortalReservation(authCtx resolvedPortalAuth, res domain.Reservation) error {
	if res.Status == domain.ResStatusInquiry {
		return domain.ErrNotFound
	}
	if authCtx.Session.ScopeType == domain.PortalScopeBooking {
		if authCtx.ScopedRes == nil || authCtx.ScopedRes.ID != res.ID {
			return domain.ErrNotFound
		}
		if !strings.EqualFold(res.ChannelCode, "DIRECT") && domain.IsOTAProxyEmail(res.GuestEmail) {
			return domain.ErrNotFound
		}
		return nil
	}
	owned := false
	for _, cid := range authCtx.ClusterIDs {
		if res.GuestID == cid {
			owned = true
			break
		}
	}
	if !owned {
		return domain.ErrNotFound
	}
	if !strings.EqualFold(res.ChannelCode, "DIRECT") {
		if authCtx.Identity == nil || domain.IsOTAProxyEmail(authCtx.Identity.VerifiedEmail) {
			return domain.ErrNotFound
		}
	}
	return nil
}

func (s *Service) GetPortalBookingDetail(
	ctx context.Context,
	propertySlug, plainSessionToken, bookingRef string,
) (domain.PortalBookingDetail, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, "", false)
	if err != nil {
		return domain.PortalBookingDetail{}, err
	}
	res, err := s.store.GetReservationByReference(ctx, nil, authCtx.Property.ID, bookingRef)
	if err != nil {
		return domain.PortalBookingDetail{}, domain.ErrNotFound
	}
	if err := s.authorizePortalReservation(authCtx, res); err != nil {
		return domain.PortalBookingDetail{}, err
	}
	sum := buildPortalBookingSummary(authCtx.Property, res)
	detail := domain.PortalBookingDetail{
		PortalBookingSummary: sum,
		PropertyName:         authCtx.Property.Name,
		PropertySlug:         authCtx.Property.Slug,
		ContactEmail:         authCtx.Property.ContactEmail,
		ContactPhone:         authCtx.Property.ContactPhone,
		CancellationPolicy:   authCtx.Property.Policy.CancellationPolicy,
		QuietHours:           authCtx.Property.Policy.QuietHours,
		FirePolicy:           authCtx.Property.Policy.FirePolicy,
		PetPolicy:            authCtx.Property.Policy.PetPolicy,
	}
	if !sum.IsOTAManaged {
		rawQuote, _ := s.store.GetLatestQuoteForHoldOrReservation(ctx, nil, authCtx.Property.ID, nil, &res.ID)
		rawPayments, _ := s.store.ListPaymentsForReservation(ctx, nil, authCtx.Property.ID, res.ID)
		detail.Quote = buildPortalQuoteSummary(rawQuote)
		detail.Payments = buildPortalPaymentSummaries(rawPayments)
	}
	return detail, nil
}

func (s *Service) UpdatePortalArrivalDetails(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader, bookingRef, eta, arrivalNotes string,
) (domain.PortalBookingDetail, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return domain.PortalBookingDetail{}, err
	}
	cleanETA, cleanNotes, err := domain.ValidatePortalArrivalDetails(eta, arrivalNotes)
	if err != nil {
		return domain.PortalBookingDetail{}, err
	}
	nowUTC := s.nowUTC()
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		res, err := s.store.GetReservationByReference(ctx, tx, authCtx.Property.ID, bookingRef)
		if err != nil {
			return domain.ErrNotFound
		}
		if err := s.authorizePortalReservation(authCtx, res); err != nil {
			return err
		}
		if !strings.EqualFold(res.ChannelCode, "DIRECT") {
			return ErrPortalOTAManaged
		}
		if res.Status != domain.ResStatusPending && res.Status != domain.ResStatusConfirmed && res.Status != domain.ResStatusCheckedIn {
			return fmt.Errorf("%w: arrival details can only be updated on active stays", domain.ErrConflict)
		}
		canonID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, authCtx.Property.ID, authCtx.CanonicalGuest.ID)
		if err != nil || anonOrPending {
			return domain.ErrUnauthorized
		}
		updated, err := s.store.UpdateReservationArrivalDetailsTx(ctx, tx, authCtx.Property.ID, res.ID, cleanETA, cleanNotes)
		if err != nil {
			return err
		}
		reqEmail := authCtx.CanonicalGuest.Email
		if authCtx.Identity != nil && authCtx.Identity.VerifiedEmail != "" {
			reqEmail = authCtx.Identity.VerifiedEmail
		} else if res.GuestEmail != "" {
			reqEmail = res.GuestEmail
		}
		resID := res.ID
		subj := fmt.Sprintf("%s — Arrival Details Updated (%s)", authCtx.Property.Name, res.Reference)
		txt := fmt.Sprintf("Hello %s,\n\nYour arrival details for booking %s have been updated.\n", authCtx.CanonicalGuest.FullName, res.Reference)
		htm := fmt.Sprintf("<p>Hello %s,</p><p>Your arrival details for booking <strong>%s</strong> have been updated.</p>", html.EscapeString(authCtx.CanonicalGuest.FullName), html.EscapeString(res.Reference))
		_ = s.queuePortalOperationalNotificationTx(ctx, tx, authCtx.Property.ID, canonID, &resID, nil, reqEmail, "portal_arrival_updated", subj, "Arrival details updated via Guest Portal", txt, htm, nowUTC)

		snap := auditReservationSnapshot(updated)
		snap["changed_fields"] = []string{"arrival_details"}
		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "reservation.arrival_updated", "reservation", updated.ID.String(), auditReservationSnapshot(res), snap)
	})
	if err != nil {
		return domain.PortalBookingDetail{}, err
	}
	return s.GetPortalBookingDetail(ctx, propertySlug, plainSessionToken, bookingRef)
}

type PortalBookingChangeRequestInput struct {
	RequestedArrival   string
	RequestedDeparture string
	RequestedAdults    int
	RequestedChildren  int
	RequestedInfants   int
	Notes              string
}

func (s *Service) RequestPortalBookingChange(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader, bookingRef string,
	in PortalBookingChangeRequestInput,
) (domain.PortalEnquiryView, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return domain.PortalEnquiryView{}, err
	}
	cleanNotes, err := domain.ValidatePortalMessageText("notes", in.Notes, domain.PortalMaxChangeNotesRunes, true)
	if err != nil {
		return domain.PortalEnquiryView{}, err
	}
	var summaryParts []string
	if strings.TrimSpace(in.RequestedArrival) != "" || strings.TrimSpace(in.RequestedDeparture) != "" {
		arr, aErr := domain.ParseDate(in.RequestedArrival)
		dep, dErr := domain.ParseDate(in.RequestedDeparture)
		if aErr != nil || dErr != nil || !dep.After(arr) {
			return domain.PortalEnquiryView{}, domain.NewValidationError(map[string]string{
				"dates": "requested_departure must be YYYY-MM-DD strictly after requested_arrival",
			})
		}
		summaryParts = append(summaryParts, fmt.Sprintf("Requested Dates: %s to %s", domain.FormatDate(arr), domain.FormatDate(dep)))
	}
	if in.RequestedAdults > 0 || in.RequestedChildren > 0 || in.RequestedInfants > 0 {
		if in.RequestedAdults < 1 || in.RequestedAdults > 30 || in.RequestedChildren < 0 || in.RequestedChildren > 30 || in.RequestedInfants < 0 || in.RequestedInfants > 30 {
			return domain.PortalEnquiryView{}, domain.NewValidationError(map[string]string{
				"party": "requested party counts are out of range",
			})
		}
		summaryParts = append(summaryParts, fmt.Sprintf("Requested Party: %d adults, %d children, %d infants", in.RequestedAdults, in.RequestedChildren, in.RequestedInfants))
	}
	summaryParts = append(summaryParts, "Guest Notes: "+cleanNotes)
	fullBody := strings.Join(summaryParts, "\n")

	nowUTC := s.nowUTC()
	var createdEnq domain.GuestEnquiry
	var createdMsg domain.GuestEnquiryMessage
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		res, err := s.store.GetReservationByReference(ctx, tx, authCtx.Property.ID, bookingRef)
		if err != nil {
			return domain.ErrNotFound
		}
		if err := s.authorizePortalReservation(authCtx, res); err != nil {
			return err
		}
		if !strings.EqualFold(res.ChannelCode, "DIRECT") {
			return ErrPortalOTAManaged
		}
		if res.Status != domain.ResStatusPending && res.Status != domain.ResStatusConfirmed {
			return fmt.Errorf("%w: change requests are only available for upcoming pending or confirmed direct bookings", domain.ErrConflict)
		}
		canonID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, authCtx.Property.ID, authCtx.CanonicalGuest.ID)
		if err != nil || anonOrPending {
			return domain.ErrUnauthorized
		}
		openCount, err := s.store.CountOpenPortalChangeRequestsTx(ctx, tx, authCtx.Property.ID, res.ID)
		if err != nil {
			return err
		}
		if openCount >= domain.PortalMaxOpenChangeRequests {
			return fmt.Errorf("%w: at most %d open change requests are permitted per booking", domain.ErrConflict, domain.PortalMaxOpenChangeRequests)
		}

		resID := res.ID
		reqEmail := authCtx.CanonicalGuest.Email
		if authCtx.Identity != nil && authCtx.Identity.VerifiedEmail != "" {
			reqEmail = authCtx.Identity.VerifiedEmail
		} else if res.GuestEmail != "" {
			reqEmail = res.GuestEmail
		}
		createdEnq, err = s.store.CreateGuestEnquiry(ctx, tx, domain.GuestEnquiry{
			PropertyID:     authCtx.Property.ID,
			GuestID:        &canonID,
			ReservationID:  &resID,
			Category:       domain.EnqCatBooking,
			Status:         domain.EnqStatusNew,
			Priority:       domain.EnqPriorityNormal,
			Source:         domain.EnqSourcePortal,
			Subject:        fmt.Sprintf("Booking Change Request — %s", res.Reference),
			Description:    fullBody,
			RequesterName:  authCtx.CanonicalGuest.FullName,
			RequesterEmail: reqEmail,
			RequesterPhone: authCtx.CanonicalGuest.Phone,
		})
		if err != nil {
			return err
		}
		createdEnq.ReservationReference = res.Reference
		createdMsg, err = s.store.AddGuestEnquiryMessage(ctx, tx, domain.GuestEnquiryMessage{
			PropertyID:  authCtx.Property.ID,
			EnquiryID:   createdEnq.ID,
			Direction:   domain.CommDirInbound,
			SenderName:  authCtx.CanonicalGuest.FullName,
			SenderEmail: reqEmail,
			Body:        fullBody,
			IsInternal:  false,
		})
		if err != nil {
			return err
		}
		if _, err := s.store.CreateGuestCommunication(ctx, tx, domain.GuestCommunication{
			PropertyID:      authCtx.Property.ID,
			GuestID:         &canonID,
			ReservationID:   &resID,
			EnquiryID:       &createdEnq.ID,
			InteractionType: domain.InteractionWebsiteEnquiry,
			Direction:       domain.CommDirInbound,
			Subject:         fmt.Sprintf("[%s] %s", createdEnq.Reference, createdEnq.Subject),
			BodyText:        fullBody,
			Summary:         "Booking change request submitted via Guest Portal",
			DeliveryStatus:  domain.DeliveryLogged,
			OccurredAt:      nowUTC,
		}); err != nil {
			return err
		}
		subj := fmt.Sprintf("%s — Change Request Received (%s)", authCtx.Property.Name, res.Reference)
		txt := fmt.Sprintf("Hello %s,\n\nWe have received your booking change request (%s) for booking %s.\nOur reservations team will review availability and pricing and reply shortly.\n", authCtx.CanonicalGuest.FullName, createdEnq.Reference, res.Reference)
		htm := fmt.Sprintf("<p>Hello %s,</p><p>We have received your booking change request (<strong>%s</strong>) for booking <strong>%s</strong>.</p><p>Our reservations team will review availability and pricing and reply shortly.</p>", html.EscapeString(authCtx.CanonicalGuest.FullName), html.EscapeString(createdEnq.Reference), html.EscapeString(res.Reference))
		_ = s.queuePortalOperationalNotificationTx(ctx, tx, authCtx.Property.ID, canonID, &resID, &createdEnq.ID, reqEmail, "portal_change_request_ack", subj, "Booking change request acknowledgment queued", txt, htm, nowUTC)

		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest_enquiry.created", "guest_enquiry", createdEnq.ID.String(), nil, auditEnquirySnapshot(createdEnq))
	})
	if err != nil {
		return domain.PortalEnquiryView{}, err
	}
	return toPortalEnquiryView(createdEnq, []domain.GuestEnquiryMessage{createdMsg}), nil
}

func (s *Service) RequestPortalBookingCancellation(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader, bookingRef, note string,
) (domain.PortalBookingDetail, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return domain.PortalBookingDetail{}, err
	}
	cleanNote, err := domain.ValidatePortalMessageText("note", note, domain.PortalMaxArrivalNotesRunes, false)
	if err != nil {
		return domain.PortalBookingDetail{}, err
	}
	nowUTC := s.nowUTC()
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		res, err := s.store.GetReservationByReference(ctx, tx, authCtx.Property.ID, bookingRef)
		if err != nil {
			return domain.ErrNotFound
		}
		if err := s.authorizePortalReservation(authCtx, res); err != nil {
			return err
		}
		if !strings.EqualFold(res.ChannelCode, "DIRECT") {
			return ErrPortalOTAManaged
		}
		if res.Status != domain.ResStatusPending && res.Status != domain.ResStatusConfirmed {
			return fmt.Errorf("%w: cancellation cannot be requested for %s bookings", domain.ErrConflict, res.Status)
		}
		if res.CancellationRequestedAt != nil {
			return fmt.Errorf("%w: a cancellation request has already been recorded for this booking", domain.ErrConflict)
		}
		canonID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, authCtx.Property.ID, authCtx.CanonicalGuest.ID)
		if err != nil || anonOrPending {
			return domain.ErrUnauthorized
		}
		updated, err := s.store.RecordCustomerCancellationRequest(ctx, tx, authCtx.Property.ID, res.ID, cleanNote)
		if err != nil {
			return err
		}
		reqEmail := authCtx.CanonicalGuest.Email
		if authCtx.Identity != nil && authCtx.Identity.VerifiedEmail != "" {
			reqEmail = authCtx.Identity.VerifiedEmail
		} else if res.GuestEmail != "" {
			reqEmail = res.GuestEmail
		}
		resID := res.ID
		subj := fmt.Sprintf("%s — Cancellation Request Received (%s)", authCtx.Property.Name, res.Reference)
		txt := fmt.Sprintf("Hello %s,\n\nWe have recorded your cancellation request for booking %s.\nOur team will review your booking under our cancellation policy and confirm next steps.\n", authCtx.CanonicalGuest.FullName, res.Reference)
		htm := fmt.Sprintf("<p>Hello %s,</p><p>We have recorded your cancellation request for booking <strong>%s</strong>.</p><p>Our team will review your booking under our cancellation policy and confirm next steps.</p>", html.EscapeString(authCtx.CanonicalGuest.FullName), html.EscapeString(res.Reference))
		_ = s.queuePortalOperationalNotificationTx(ctx, tx, authCtx.Property.ID, canonID, &resID, nil, reqEmail, "portal_cancel_request_ack", subj, "Cancellation request acknowledgment queued", txt, htm, nowUTC)

		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "reservation.cancellation_requested", "reservation", res.ID.String(), nil, auditReservationSnapshot(updated))
	})
	if err != nil {
		return domain.PortalBookingDetail{}, err
	}
	return s.GetPortalBookingDetail(ctx, propertySlug, plainSessionToken, bookingRef)
}

// --- Portal Tier B Profile, Email Change, Consent, Enquiries & Privacy Requests ---

func (s *Service) GetPortalProfile(ctx context.Context, propertySlug, plainSessionToken string) (domain.PortalProfileView, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, "", false)
	if err != nil {
		return domain.PortalProfileView{}, err
	}
	if authCtx.Session.ScopeType != domain.PortalScopeAccount || authCtx.Identity == nil {
		return domain.PortalProfileView{}, domain.ErrForbidden
	}
	nowUTC := s.nowUTC()
	g := authCtx.CanonicalGuest
	consents, err := s.store.ListGuestConsentRecords(ctx, nil, authCtx.Property.ID, g.ID)
	if err != nil {
		return domain.PortalProfileView{}, err
	}
	consentState := domain.ConsentNotSubscribed
	if len(consents) > 0 {
		consentState = consents[0].Status
	}
	if g.Status == domain.GuestStatusDoNotContact {
		consentState = domain.ConsentWithdrawn
	}
	consentViews := make([]domain.PortalConsentEntryView, 0, len(consents))
	for _, c := range consents {
		consentViews = append(consentViews, domain.PortalConsentEntryView{
			ConsentType: c.ConsentType,
			Status:      c.Status,
			Method:      c.Method,
			OccurredAt:  c.OccurredAt,
		})
	}

	rawPrefs, err := s.store.ListGuestPreferences(ctx, nil, authCtx.Property.ID, g.ID)
	if err != nil {
		return domain.PortalProfileView{}, err
	}
	prefViews := make([]domain.PortalGuestPreferenceView, 0, len(rawPrefs))
	for _, p := range rawPrefs {
		switch p.Category {
		case domain.PrefCatArrival, domain.PrefCatAccessibility, domain.PrefCatCommunication, domain.PrefCatPet:
			prefViews = append(prefViews, domain.PortalGuestPreferenceView{
				Category:        p.Category,
				PreferenceKey:   p.PreferenceKey,
				PreferenceValue: p.PreferenceValue,
			})
		}
	}

	var pendExp *time.Time
	pendEmail := activePendingEmail(*authCtx.Identity, nowUTC)
	if pendEmail != "" {
		pendExp = authCtx.Identity.PendingEmailExpiresAt
	}
	lang := g.PreferredLanguage
	if lang == "" {
		lang = "en"
	}
	method := g.PreferredContactMethod
	if method == "" {
		method = string(domain.ContactMethodEmail)
	}

	return domain.PortalProfileView{
		FirstName:              g.FirstName,
		LastName:               g.LastName,
		FullName:               g.FullName,
		VerifiedEmail:          authCtx.Identity.VerifiedEmail,
		PendingEmail:           pendEmail,
		PendingEmailExpiresAt:  pendExp,
		Phone:                  g.Phone,
		PreferredContactMethod: method,
		PreferredLanguage:      lang,
		IsDoNotContact:         g.Status == domain.GuestStatusDoNotContact,
		MarketingConsentState:  consentState,
		ConsentHistory:         consentViews,
		Preferences:            prefViews,
	}, nil
}

type UpdatePortalProfileInput struct {
	Phone                  string
	PreferredContactMethod string
	PreferredLanguage      string
	Preferences            *[]domain.PortalGuestPreferenceView
}

func (s *Service) UpdatePortalProfile(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader string,
	in UpdatePortalProfileInput,
) (domain.PortalProfileView, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return domain.PortalProfileView{}, err
	}
	if authCtx.Session.ScopeType != domain.PortalScopeAccount || authCtx.Identity == nil {
		return domain.PortalProfileView{}, domain.ErrForbidden
	}
	if authCtx.CanonicalGuest.Status == domain.GuestStatusDoNotContact {
		return domain.PortalProfileView{}, ErrPortalDoNotContactRestricted
	}
	cleanPhone, cleanMethod, cleanLang, err := domain.ValidatePortalProfileUpdate(in.Phone, in.PreferredContactMethod, in.PreferredLanguage)
	if err != nil {
		return domain.PortalProfileView{}, err
	}
	var validatedPrefs []domain.PortalGuestPreferenceView
	if in.Preferences != nil {
		validatedPrefs, err = domain.ValidatePortalPreferences(*in.Preferences)
		if err != nil {
			return domain.PortalProfileView{}, err
		}
	}

	nowUTC := s.nowUTC()
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		canonID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, authCtx.Property.ID, authCtx.CanonicalGuest.ID)
		if err != nil {
			return err
		}
		if anonOrPending {
			return domain.ErrUnauthorized
		}
		before, err := s.store.GetGuest(ctx, tx, authCtx.Property.ID, canonID)
		if err != nil {
			return err
		}
		if before.Status == domain.GuestStatusDoNotContact {
			return ErrPortalDoNotContactRestricted
		}
		updated, err := s.store.UpdateGuestPortalContactTx(ctx, tx, authCtx.Property.ID, canonID, cleanPhone, cleanMethod, cleanLang)
		if err != nil {
			return err
		}
		changedFields := auditGuestChangedFields(before, updated)
		if in.Preferences != nil {
			if err := s.store.ReplacePortalGuestPreferencesTx(ctx, tx, authCtx.Property.ID, canonID, validatedPrefs); err != nil {
				return err
			}
			changedFields = append(changedFields, "preferences")
		}
		subj := fmt.Sprintf("%s — Contact Preferences Updated", authCtx.Property.Name)
		txt := fmt.Sprintf("Hello %s,\n\nYour Guest Portal contact details and communication preferences were updated.\nIf you did not make this change, please contact us immediately.\n", updated.FullName)
		htm := fmt.Sprintf("<p>Hello %s,</p><p>Your Guest Portal contact details and communication preferences were updated.</p><p>If you did not make this change, please contact us immediately.</p>", html.EscapeString(updated.FullName))
		_ = s.queuePortalOperationalNotificationTx(ctx, tx, authCtx.Property.ID, canonID, nil, nil, authCtx.Identity.VerifiedEmail, "portal_profile_updated", subj, "Contact preferences updated via Guest Portal", txt, htm, nowUTC)

		afterSnap := auditGuestSnapshot(updated)
		afterSnap["changed_fields"] = changedFields
		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest.updated", "guest", updated.ID.String(), auditGuestSnapshot(before), afterSnap)
	})
	if err != nil {
		return domain.PortalProfileView{}, err
	}
	return s.GetPortalProfile(ctx, propertySlug, plainSessionToken)
}

// InitiatePortalEmailChange sends a single-use verification OTP to newEmail (requiring fresh
// step-up session verification <= 15m) without disclosing whether another guest owns newEmail.
func (s *Service) InitiatePortalEmailChange(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader, newEmail string,
) error {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return err
	}
	if authCtx.Session.ScopeType != domain.PortalScopeAccount || authCtx.Identity == nil {
		return domain.ErrForbidden
	}
	if authCtx.CanonicalGuest.Status == domain.GuestStatusDoNotContact {
		return ErrPortalDoNotContactRestricted
	}
	nowUTC := s.nowUTC()
	if nowUTC.Sub(authCtx.Session.VerifiedAt) > domain.PortalStepUpFreshnessWindow {
		return ErrPortalStepUpRequired
	}
	cleanNew, err := domain.ValidateEmailRecipient(newEmail)
	if err != nil {
		return domain.NewValidationError(map[string]string{"new_email": "must be a valid email address"})
	}
	normNew := domain.NormalizeEmail(cleanNew)
	if normNew == authCtx.Identity.NormalizedVerifiedEmail {
		return domain.NewValidationError(map[string]string{"new_email": "must be different from your current verified email"})
	}
	otpKey, err := s.portalOTPHMACKey()
	if err != nil {
		return err
	}
	if domain.IsOTAProxyEmail(normNew) {
		return nil
	}

	if err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if existingIdent, iErr := s.store.GetPortalIdentityByVerifiedEmail(ctx, tx, authCtx.Property.ID, normNew); iErr == nil && existingIdent.ID != authCtx.Identity.ID {
			return nil
		}
		existingGuests, gErr := s.store.ListActiveGuestsByNormalizedEmail(ctx, tx, authCtx.Property.ID, normNew)
		if gErr != nil {
			return nil
		}
		for _, eg := range existingGuests {
			if eg.ID != authCtx.CanonicalGuest.ID {
				return nil
			}
		}

		exp := nowUTC.Add(domain.PortalChallengeTTL)
		if err := s.store.SetPortalPendingEmailTx(ctx, tx, authCtx.Property.ID, authCtx.Identity.ID, cleanNew, exp); err != nil {
			return err
		}
		if err := s.store.InvalidateActivePortalChallengesTx(ctx, tx, authCtx.Property.ID, normNew, domain.PortalChallengeVerifyEmail, nowUTC); err != nil {
			return err
		}
		chID, err := postgres.NewUUID()
		if err != nil {
			return err
		}
		otpPlain, err := postgres.GeneratePortalOTPCode()
		if err != nil {
			return err
		}
		tokDummy, err := postgres.GeneratePortalRandomHex(32)
		if err != nil {
			return err
		}
		otpHash, err := postgres.HashPortalOTP(otpKey, chID, normNew, otpPlain)
		if err != nil {
			return err
		}
		identID := authCtx.Identity.ID
		gid := authCtx.CanonicalGuest.ID
		if _, err := s.store.CreatePortalChallengeTx(ctx, tx, domain.GuestPortalChallenge{
			ID:               chID,
			PropertyID:       authCtx.Property.ID,
			PortalIdentityID: &identID,
			GuestID:          &gid,
			NormalizedEmail:  normNew,
			Purpose:          domain.PortalChallengeVerifyEmail,
			OTPHash:          otpHash,
			OTPHashAlg:       domain.PortalOTPHashAlgHMACSHA256V1,
			TokenHash:        postgres.HashPortalValue(tokDummy),
			MaxAttempts:      domain.PortalChallengeMaxAttempts,
			ExpiresAt:        exp,
		}); err != nil {
			return err
		}
		if err := s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest_portal_identity.email_change_requested", "guest_portal_identity", identID.String(), nil, map[string]any{
			"id":           identID,
			"property_id":  authCtx.Property.ID,
			"guest_id":     gid,
			"status":       string(authCtx.Identity.Status),
			"purpose":      string(domain.PortalChallengeVerifyEmail),
			"challenge_id": chID,
			"version":      authCtx.Identity.Version + 1,
			"created_at":   authCtx.Identity.CreatedAt,
			"updated_at":   nowUTC,
		}); err != nil {
			return err
		}

		subj := fmt.Sprintf("%s — Verify Your New Email Address", authCtx.Property.Name)
		txt := fmt.Sprintf("Hello %s,\n\nUse verification code %s within 15 minutes to confirm changing your Guest Portal email address to %s.\n", authCtx.CanonicalGuest.FullName, otpPlain, cleanNew)
		htm := fmt.Sprintf("<p>Hello %s,</p><p>Use verification code <strong style=\"font-family:monospace;font-size:20px;\">%s</strong> within 15 minutes to confirm changing your Guest Portal email address.</p>", html.EscapeString(authCtx.CanonicalGuest.FullName), html.EscapeString(otpPlain))
		return s.queuePortalSecurityChallengeEmailTx(ctx, tx, authCtx.Property.ID, &gid, cleanNew, "portal_email_change_verify", subj, txt, htm, nowUTC)
	}); err != nil {
		return err
	}
	return nil
}

// ConfirmPortalEmailChange verifies the OTP sent to pending_email, atomically updates
// guest_portal_identities + guests.email, revokes all other portal sessions, and alerts the old email.
func (s *Service) ConfirmPortalEmailChange(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader, otpCode string,
) (domain.PortalProfileView, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return domain.PortalProfileView{}, err
	}
	if authCtx.Session.ScopeType != domain.PortalScopeAccount || authCtx.Identity == nil {
		return domain.PortalProfileView{}, domain.ErrForbidden
	}
	nowUTC := s.nowUTC()
	pendEmail := activePendingEmail(*authCtx.Identity, nowUTC)
	if pendEmail == "" {
		return domain.PortalProfileView{}, fmt.Errorf("%w: no pending email verification is active", domain.ErrConflict)
	}
	if strings.TrimSpace(otpCode) == "" {
		return domain.PortalProfileView{}, domain.NewValidationError(map[string]string{"code": "verification code is required"})
	}
	otpKey, err := s.portalOTPHMACKey()
	if err != nil {
		return domain.PortalProfileView{}, err
	}

	oldVerifiedEmail := authCtx.Identity.VerifiedEmail
	var failedID uuid.UUID
	txErr := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		ch, err := s.store.LockLatestPortalChallengeByEmailTx(ctx, tx, authCtx.Property.ID, domain.NormalizeEmail(pendEmail), domain.PortalChallengeVerifyEmail, nowUTC)
		if err != nil {
			return domain.ErrUnauthorized
		}
		if ch.PortalIdentityID == nil || *ch.PortalIdentityID != authCtx.Identity.ID {
			return domain.ErrUnauthorized
		}
		proofOK, pErr := postgres.VerifyChallengeProof(otpKey, ch, otpCode, "", "")
		if pErr != nil {
			return pErr
		}
		if !proofOK {
			failedID = ch.ID
			return domain.ErrUnauthorized
		}
		if err := s.store.ConsumePortalChallengeTx(ctx, tx, authCtx.Property.ID, ch.ID, ch.NormalizedEmail, nowUTC); err != nil {
			return err
		}
		canonID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, authCtx.Property.ID, authCtx.CanonicalGuest.ID)
		if err != nil || anonOrPending {
			return domain.ErrUnauthorized
		}
		updatedIdent, _, err := s.store.ConfirmPortalEmailChangeTx(ctx, tx, authCtx.Property.ID, authCtx.Identity.ID, canonID, pendEmail, nowUTC)
		if err != nil {
			return err
		}
		revokedCount, err := s.store.RevokeOtherPortalSessionsTx(ctx, tx, authCtx.Property.ID, authCtx.Identity.ID, authCtx.Session.ID, nowUTC)
		if err != nil {
			return err
		}
		if err := s.store.RefreshPortalSessionVerifiedAtTx(ctx, tx, authCtx.Property.ID, authCtx.Session.ID, nowUTC); err != nil {
			return err
		}

		subj := fmt.Sprintf("%s — Security Notice: Guest Portal Email Changed", authCtx.Property.Name)
		txt := fmt.Sprintf("Hello %s,\n\nYour %s Guest Portal email address was just updated, and all other signed-in sessions were revoked.\nIf you did not authorise this change, please contact %s immediately.\n", authCtx.CanonicalGuest.FullName, authCtx.Property.Name, authCtx.Property.ContactEmail)
		htm := fmt.Sprintf("<p>Hello %s,</p><p>Your <strong>%s</strong> Guest Portal email address was just updated, and all other signed-in sessions were revoked.</p><p>If you did not authorise this change, please contact us immediately.</p>", html.EscapeString(authCtx.CanonicalGuest.FullName), html.EscapeString(authCtx.Property.Name))
		_ = s.queuePortalOperationalNotificationTx(ctx, tx, authCtx.Property.ID, canonID, nil, nil, oldVerifiedEmail, "portal_email_change_alert", subj, "Security alert sent to previous verified email", txt, htm, nowUTC)

		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest_portal_identity.email_changed", "guest_portal_identity", updatedIdent.ID.String(), nil, map[string]any{
			"id":                updatedIdent.ID,
			"property_id":       authCtx.Property.ID,
			"guest_id":          canonID,
			"status":            string(updatedIdent.Status),
			"purpose":           string(domain.PortalChallengeVerifyEmail),
			"challenge_id":      ch.ID,
			"sessions_revoked":  revokedCount,
			"version":           updatedIdent.Version,
			"email_verified_at": updatedIdent.EmailVerifiedAt,
			"created_at":        updatedIdent.CreatedAt,
			"updated_at":        updatedIdent.UpdatedAt,
		})
	})
	if failedID != uuid.Nil {
		_ = s.store.IncrementPortalChallengeFailure(ctx, authCtx.Property.ID, failedID, nowUTC)
	}
	if txErr != nil {
		return domain.PortalProfileView{}, txErr
	}
	return s.GetPortalProfile(ctx, propertySlug, plainSessionToken)
}

func (s *Service) UpdatePortalMarketingConsent(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader string,
	status domain.ConsentStatus,
) (domain.PortalProfileView, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return domain.PortalProfileView{}, err
	}
	if authCtx.Session.ScopeType != domain.PortalScopeAccount || authCtx.Identity == nil {
		return domain.PortalProfileView{}, domain.ErrForbidden
	}
	if status != domain.ConsentGranted && status != domain.ConsentWithdrawn {
		return domain.PortalProfileView{}, domain.NewValidationError(map[string]string{
			"status": "must be GRANTED or WITHDRAWN",
		})
	}
	if authCtx.CanonicalGuest.Status == domain.GuestStatusDoNotContact && status == domain.ConsentGranted {
		return domain.PortalProfileView{}, ErrPortalDoNotContactRestricted
	}
	nowUTC := s.nowUTC()
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		canonID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, authCtx.Property.ID, authCtx.CanonicalGuest.ID)
		if err != nil || anonOrPending {
			return domain.ErrUnauthorized
		}
		rec, err := s.store.RecordGuestConsent(ctx, tx, domain.GuestConsentRecord{
			PropertyID:    authCtx.Property.ID,
			GuestID:       canonID,
			ConsentType:   "MARKETING_EMAIL",
			Status:        status,
			Method:        "PORTAL",
			Source:        "apps/public-web",
			PolicyVersion: authCtx.Property.Policy.BookingTermsVersion,
			OccurredAt:    nowUTC,
		})
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest_consent.recorded", "guest", canonID.String(), nil, auditConsentSnapshot(rec))
	})
	if err != nil {
		return domain.PortalProfileView{}, err
	}
	return s.GetPortalProfile(ctx, propertySlug, plainSessionToken)
}

func toPortalEnquiryView(e domain.GuestEnquiry, msgs []domain.GuestEnquiryMessage) domain.PortalEnquiryView {
	visible := make([]domain.PortalEnquiryMessageView, 0, len(msgs))
	for _, m := range msgs {
		if m.IsInternal || m.Direction == domain.CommDirInternal {
			continue
		}
		role := "GUEST"
		sender := m.SenderName
		if m.Direction == domain.CommDirOutbound || m.StaffUserID != nil {
			role = "PROPERTY"
			sender = "Reservations Team"
		} else if strings.TrimSpace(sender) == "" {
			sender = "Guest"
		}
		visible = append(visible, domain.PortalEnquiryMessageView{
			ID:         m.ID,
			Direction:  m.Direction,
			SenderRole: role,
			SenderName: sender,
			Body:       m.Body,
			CreatedAt:  m.CreatedAt,
		})
	}
	return domain.PortalEnquiryView{
		ID:                   e.ID,
		Reference:            e.Reference,
		ReservationReference: e.ReservationReference,
		Category:             e.Category,
		Status:               e.Status,
		Subject:              e.Subject,
		Description:          e.Description,
		CreatedAt:            e.CreatedAt,
		UpdatedAt:            e.UpdatedAt,
		Messages:             visible,
	}
}

// ListPortalEnquiries lists guest-visible support threads while strictly excluding SAR/Erasure
// rights requests (Section 7.2) and stripping all INTERNAL messages and staff identities.
func (s *Service) ListPortalEnquiries(ctx context.Context, propertySlug, plainSessionToken string) ([]domain.PortalEnquiryView, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, "", false)
	if err != nil {
		return nil, err
	}
	var enquiries []domain.GuestEnquiry
	if authCtx.Session.ScopeType == domain.PortalScopeBooking {
		if authCtx.ScopedRes == nil {
			return []domain.PortalEnquiryView{}, nil
		}
		enquiries, err = s.store.ListGuestEnquiriesByClusterOrReservations(ctx, nil, authCtx.Property.ID, nil, []uuid.UUID{authCtx.ScopedRes.ID})
		if err != nil {
			return nil, err
		}
	} else {
		reservations, err := s.store.ListGuestReservationsByCluster(ctx, nil, authCtx.Property.ID, authCtx.ClusterIDs)
		if err != nil {
			return nil, err
		}
		resIDs := make([]uuid.UUID, 0, len(reservations))
		for _, r := range reservations {
			resIDs = append(resIDs, r.ID)
		}
		enquiries, err = s.store.ListGuestEnquiriesByClusterOrReservations(ctx, nil, authCtx.Property.ID, authCtx.ClusterIDs, resIDs)
		if err != nil {
			return nil, err
		}
	}

	filtered := make([]domain.GuestEnquiry, 0, len(enquiries))
	enqIDs := make([]uuid.UUID, 0, len(enquiries))
	for _, e := range enquiries {
		if domain.IsPrivacyRightsEnquiry(e) {
			continue
		}
		if authCtx.Session.ScopeType == domain.PortalScopeBooking {
			if e.ReservationID == nil || authCtx.ScopedRes == nil || *e.ReservationID != authCtx.ScopedRes.ID {
				continue
			}
		}
		filtered = append(filtered, e)
		enqIDs = append(enqIDs, e.ID)
	}
	if len(enqIDs) == 0 {
		return []domain.PortalEnquiryView{}, nil
	}
	allMsgs, err := s.store.ListGuestEnquiryMessagesByEnquiryIDs(ctx, nil, authCtx.Property.ID, enqIDs)
	if err != nil {
		return nil, err
	}
	msgByEnq := make(map[uuid.UUID][]domain.GuestEnquiryMessage, len(filtered))
	for _, m := range allMsgs {
		if m.IsInternal || m.Direction == domain.CommDirInternal {
			continue
		}
		msgByEnq[m.EnquiryID] = append(msgByEnq[m.EnquiryID], m)
	}
	out := make([]domain.PortalEnquiryView, 0, len(filtered))
	for _, e := range filtered {
		out = append(out, toPortalEnquiryView(e, msgByEnq[e.ID]))
	}
	return out, nil
}

func (s *Service) CreatePortalEnquiry(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader string,
	category domain.EnquiryCategory,
	bookingRef, subject, message string,
) (domain.PortalEnquiryView, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return domain.PortalEnquiryView{}, err
	}
	if authCtx.Session.ScopeType != domain.PortalScopeAccount || authCtx.Identity == nil {
		return domain.PortalEnquiryView{}, domain.ErrForbidden
	}
	cleanSubj, err := domain.ValidatePortalMessageText("subject", subject, domain.PortalMaxEnquirySubjectRunes, true)
	if err != nil {
		return domain.PortalEnquiryView{}, err
	}
	if strings.HasPrefix(cleanSubj, domain.PortalSARSubjectPrefix) || strings.HasPrefix(cleanSubj, domain.PortalErasureSubjectPrefix) || strings.HasPrefix(cleanSubj, "[GDPR Art.") {
		return domain.PortalEnquiryView{}, domain.NewValidationError(map[string]string{"subject": "reserved subject prefix"})
	}
	cleanBody, err := domain.ValidatePortalMessageText("message", message, domain.PortalMaxEnquiryMessageRunes, true)
	if err != nil {
		return domain.PortalEnquiryView{}, err
	}
	if category == "" {
		category = domain.EnqCatGeneral
	}
	switch category {
	case domain.EnqCatGeneral, domain.EnqCatBooking, domain.EnqCatArrivalDeparture,
		domain.EnqCatSpecialRequest, domain.EnqCatFacilities, domain.EnqCatComplaint,
		domain.EnqCatLostProperty, domain.EnqCatOther:
	default:
		return domain.PortalEnquiryView{}, domain.NewValidationError(map[string]string{"category": "unsupported enquiry category"})
	}

	nowUTC := s.nowUTC()
	var createdEnq domain.GuestEnquiry
	var createdMsg domain.GuestEnquiryMessage
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		canonID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, authCtx.Property.ID, authCtx.CanonicalGuest.ID)
		if err != nil || anonOrPending {
			return domain.ErrUnauthorized
		}
		var resIDPtr *uuid.UUID
		var resRef string
		if strings.TrimSpace(bookingRef) != "" {
			res, rErr := s.store.GetReservationByReference(ctx, tx, authCtx.Property.ID, bookingRef)
			if rErr != nil {
				return domain.NewValidationError(map[string]string{"booking_reference": "booking reference not found on your account"})
			}
			if err := s.authorizePortalReservation(authCtx, res); err != nil {
				return domain.NewValidationError(map[string]string{"booking_reference": "booking reference not found on your account"})
			}
			rid := res.ID
			resIDPtr = &rid
			resRef = res.Reference
		}

		createdEnq, err = s.store.CreateGuestEnquiry(ctx, tx, domain.GuestEnquiry{
			PropertyID:     authCtx.Property.ID,
			GuestID:        &canonID,
			ReservationID:  resIDPtr,
			Category:       category,
			Status:         domain.EnqStatusNew,
			Priority:       domain.EnqPriorityNormal,
			Source:         domain.EnqSourcePortal,
			Subject:        cleanSubj,
			Description:    cleanBody,
			RequesterName:  authCtx.CanonicalGuest.FullName,
			RequesterEmail: authCtx.Identity.VerifiedEmail,
			RequesterPhone: authCtx.CanonicalGuest.Phone,
		})
		if err != nil {
			return err
		}
		createdEnq.ReservationReference = resRef
		createdMsg, err = s.store.AddGuestEnquiryMessage(ctx, tx, domain.GuestEnquiryMessage{
			PropertyID:  authCtx.Property.ID,
			EnquiryID:   createdEnq.ID,
			Direction:   domain.CommDirInbound,
			SenderName:  authCtx.CanonicalGuest.FullName,
			SenderEmail: authCtx.Identity.VerifiedEmail,
			Body:        cleanBody,
			IsInternal:  false,
		})
		if err != nil {
			return err
		}
		intType := domain.InteractionWebsiteEnquiry
		if category == domain.EnqCatComplaint {
			intType = domain.InteractionComplaint
		}
		if _, err := s.store.CreateGuestCommunication(ctx, tx, domain.GuestCommunication{
			PropertyID:      authCtx.Property.ID,
			GuestID:         &canonID,
			ReservationID:   resIDPtr,
			EnquiryID:       &createdEnq.ID,
			InteractionType: intType,
			Direction:       domain.CommDirInbound,
			Subject:         fmt.Sprintf("[%s] %s", createdEnq.Reference, createdEnq.Subject),
			BodyText:        cleanBody,
			Summary:         fmt.Sprintf("%s submitted via Guest Portal", category),
			DeliveryStatus:  domain.DeliveryLogged,
			OccurredAt:      nowUTC,
		}); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest_enquiry.created", "guest_enquiry", createdEnq.ID.String(), nil, auditEnquirySnapshot(createdEnq))
	})
	if err != nil {
		return domain.PortalEnquiryView{}, err
	}
	return toPortalEnquiryView(createdEnq, []domain.GuestEnquiryMessage{createdMsg}), nil
}

func (s *Service) ReplyPortalEnquiry(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader string,
	enquiryID uuid.UUID,
	message string,
) (domain.PortalEnquiryView, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return domain.PortalEnquiryView{}, err
	}
	cleanBody, err := domain.ValidatePortalMessageText("message", message, domain.PortalMaxEnquiryMessageRunes, true)
	if err != nil {
		return domain.PortalEnquiryView{}, err
	}

	var updatedEnq domain.GuestEnquiry
	var allMsgs []domain.GuestEnquiryMessage
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		enq, err := s.store.GetGuestEnquiry(ctx, tx, authCtx.Property.ID, enquiryID)
		if err != nil {
			return domain.ErrNotFound
		}
		if domain.IsPrivacyRightsEnquiry(enq) {
			return domain.ErrNotFound
		}
		if authCtx.Session.ScopeType == domain.PortalScopeBooking {
			if authCtx.ScopedRes == nil || enq.ReservationID == nil || *enq.ReservationID != authCtx.ScopedRes.ID {
				return domain.ErrNotFound
			}
		} else {
			owned := false
			if enq.GuestID != nil {
				for _, cid := range authCtx.ClusterIDs {
					if *enq.GuestID == cid {
						owned = true
						break
					}
				}
			}
			if !owned && enq.ReservationID != nil {
				res, rErr := s.store.GetReservation(ctx, tx, authCtx.Property.ID, *enq.ReservationID)
				if rErr == nil && s.authorizePortalReservation(authCtx, res) == nil {
					owned = true
				}
			}
			if !owned {
				return domain.ErrNotFound
			}
		}
		if enq.Status == domain.EnqStatusClosed {
			return fmt.Errorf("%w: closed enquiries cannot accept new replies", domain.ErrConflict)
		}

		_, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, authCtx.Property.ID, authCtx.CanonicalGuest.ID)
		if err != nil || anonOrPending {
			return domain.ErrUnauthorized
		}

		senderEmail := authCtx.CanonicalGuest.Email
		if authCtx.Identity != nil && authCtx.Identity.VerifiedEmail != "" {
			senderEmail = authCtx.Identity.VerifiedEmail
		}
		msg, err := s.store.AddGuestEnquiryMessage(ctx, tx, domain.GuestEnquiryMessage{
			PropertyID:  authCtx.Property.ID,
			EnquiryID:   enq.ID,
			Direction:   domain.CommDirInbound,
			SenderName:  authCtx.CanonicalGuest.FullName,
			SenderEmail: senderEmail,
			Body:        cleanBody,
			IsInternal:  false,
		})
		if err != nil {
			return err
		}
		updatedEnq, err = s.store.ReopenEnquiryOnPortalReplyTx(ctx, tx, authCtx.Property.ID, enq.ID)
		if err != nil {
			return err
		}
		allMsgs, err = s.store.ListGuestEnquiryMessages(ctx, tx, authCtx.Property.ID, enq.ID)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest_enquiry_message.created", "guest_enquiry", enq.ID.String(), nil, auditEnquiryMessageSnapshot(msg))
	})
	if err != nil {
		return domain.PortalEnquiryView{}, err
	}
	return toPortalEnquiryView(updatedEnq, allMsgs), nil
}

// RequestPortalSAR records an authenticated personal data access request for staff review
// (Owner-Confirmed D6 & Section 7.2). It never calls ExportGuestData and returns a uniform
// receipt without exposing request status or details.
func (s *Service) RequestPortalSAR(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader, notes string,
) (domain.PortalPrivacyRequestReceipt, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return domain.PortalPrivacyRequestReceipt{}, err
	}
	if authCtx.Session.ScopeType != domain.PortalScopeAccount || authCtx.Identity == nil {
		return domain.PortalPrivacyRequestReceipt{}, domain.ErrForbidden
	}
	nowUTC := s.nowUTC()
	if nowUTC.Sub(authCtx.Session.VerifiedAt) > domain.PortalStepUpFreshnessWindow {
		return domain.PortalPrivacyRequestReceipt{}, ErrPortalStepUpRequired
	}
	cleanNotes, err := domain.ValidatePortalMessageText("notes", notes, domain.PortalMaxArrivalNotesRunes, false)
	if err != nil {
		return domain.PortalPrivacyRequestReceipt{}, err
	}
	body := "Authenticated Guest Portal personal data access request queued for staff review."
	if cleanNotes != "" {
		body += "\n\nGuest Details: " + cleanNotes
	}

	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		canonID, clusterIDs, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, authCtx.Property.ID, authCtx.CanonicalGuest.ID)
		if err != nil || anonOrPending {
			return domain.ErrUnauthorized
		}
		alreadyOpen, err := s.store.HasOpenPrivacyRightsEnquiryTx(ctx, tx, authCtx.Property.ID, clusterIDs, domain.PortalSARSubjectPrefix)
		if err != nil {
			return err
		}
		if alreadyOpen {
			// Enforce 1-open-request cap silently without revealing prior request existence/status.
			return nil
		}
		createdEnq, err := s.store.CreateGuestEnquiry(ctx, tx, domain.GuestEnquiry{
			PropertyID:     authCtx.Property.ID,
			GuestID:        &canonID,
			Category:       domain.EnqCatOther,
			Status:         domain.EnqStatusNew,
			Priority:       domain.EnqPriorityHigh,
			Source:         domain.EnqSourcePortal,
			Subject:        domain.PortalSARSubjectPrefix + " Personal Data Access Request",
			Description:    body,
			RequesterName:  authCtx.CanonicalGuest.FullName,
			RequesterEmail: authCtx.Identity.VerifiedEmail,
			RequesterPhone: authCtx.CanonicalGuest.Phone,
		})
		if err != nil {
			return err
		}
		if _, err := s.store.AddGuestEnquiryMessage(ctx, tx, domain.GuestEnquiryMessage{
			PropertyID:  authCtx.Property.ID,
			EnquiryID:   createdEnq.ID,
			Direction:   domain.CommDirInbound,
			SenderName:  authCtx.CanonicalGuest.FullName,
			SenderEmail: authCtx.Identity.VerifiedEmail,
			Body:        body,
			IsInternal:  false,
		}); err != nil {
			return err
		}
		subj := fmt.Sprintf("%s — Personal Data Request Recorded", authCtx.Property.Name)
		txt := fmt.Sprintf("Hello %s,\n\nWe have recorded your personal data access request for review by our team.\n", authCtx.CanonicalGuest.FullName)
		htm := fmt.Sprintf("<p>Hello %s,</p><p>We have recorded your personal data access request for review by our team.</p>", html.EscapeString(authCtx.CanonicalGuest.FullName))
		_ = s.queuePortalOperationalNotificationTx(ctx, tx, authCtx.Property.ID, canonID, nil, &createdEnq.ID, authCtx.Identity.VerifiedEmail, "portal_sar_ack", subj, "Personal data access request receipt queued", txt, htm, nowUTC)

		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest.sar_requested", "guest", canonID.String(), nil, map[string]any{
			"guest_id":    canonID,
			"property_id": authCtx.Property.ID,
			"enquiry_id":  createdEnq.ID,
		})
	})
	if err != nil {
		return domain.PortalPrivacyRequestReceipt{}, err
	}
	return domain.PortalPrivacyRequestReceipt{
		Accepted: true,
		Message:  portalPrivacyReceiptMessage,
	}, nil
}

// RequestPortalErasure records an authenticated personal data erasure request for staff review
// (Owner-Confirmed D7 & Section 7.2) before staff AnonymizeGuest sets erasure_requested_at,
// withdrawing marketing consent if currently granted and returning a uniform receipt.
func (s *Service) RequestPortalErasure(
	ctx context.Context,
	meta Actor,
	propertySlug, plainSessionToken, csrfHeader, reason string,
) (domain.PortalPrivacyRequestReceipt, error) {
	authCtx, err := s.authenticatePortalSession(ctx, propertySlug, plainSessionToken, csrfHeader, true)
	if err != nil {
		return domain.PortalPrivacyRequestReceipt{}, err
	}
	if authCtx.Session.ScopeType != domain.PortalScopeAccount || authCtx.Identity == nil {
		return domain.PortalPrivacyRequestReceipt{}, domain.ErrForbidden
	}
	nowUTC := s.nowUTC()
	if nowUTC.Sub(authCtx.Session.VerifiedAt) > domain.PortalStepUpFreshnessWindow {
		return domain.PortalPrivacyRequestReceipt{}, ErrPortalStepUpRequired
	}
	cleanReason, err := domain.ValidatePortalMessageText("reason", reason, domain.PortalMaxArrivalNotesRunes, false)
	if err != nil {
		return domain.PortalPrivacyRequestReceipt{}, err
	}
	body := "Authenticated Guest Portal personal data erasure request queued for staff review."
	if cleanReason != "" {
		body += "\n\nReason: " + cleanReason
	}

	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		canonID, clusterIDs, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, authCtx.Property.ID, authCtx.CanonicalGuest.ID)
		if err != nil || anonOrPending {
			return domain.ErrUnauthorized
		}
		// Always withdraw marketing consent if currently granted (D7/D9).
		consents, err := s.store.ListGuestConsentRecords(ctx, tx, authCtx.Property.ID, canonID)
		if err != nil {
			return err
		}
		if len(consents) > 0 && consents[0].Status == domain.ConsentGranted {
			rec, err := s.store.RecordGuestConsent(ctx, tx, domain.GuestConsentRecord{
				PropertyID:    authCtx.Property.ID,
				GuestID:       canonID,
				ConsentType:   "MARKETING_EMAIL",
				Status:        domain.ConsentWithdrawn,
				Method:        "PORTAL",
				Source:        "apps/public-web",
				PolicyVersion: authCtx.Property.Policy.BookingTermsVersion,
				OccurredAt:    nowUTC,
			})
			if err != nil {
				return err
			}
			if err := s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest_consent.recorded", "guest", canonID.String(), nil, auditConsentSnapshot(rec)); err != nil {
				return err
			}
		}

		alreadyOpen, err := s.store.HasOpenPrivacyRightsEnquiryTx(ctx, tx, authCtx.Property.ID, clusterIDs, domain.PortalErasureSubjectPrefix)
		if err != nil {
			return err
		}
		if alreadyOpen {
			return nil
		}

		createdEnq, err := s.store.CreateGuestEnquiry(ctx, tx, domain.GuestEnquiry{
			PropertyID:     authCtx.Property.ID,
			GuestID:        &canonID,
			Category:       domain.EnqCatOther,
			Status:         domain.EnqStatusNew,
			Priority:       domain.EnqPriorityHigh,
			Source:         domain.EnqSourcePortal,
			Subject:        domain.PortalErasureSubjectPrefix + " Personal Data Erasure Request",
			Description:    body,
			RequesterName:  authCtx.CanonicalGuest.FullName,
			RequesterEmail: authCtx.Identity.VerifiedEmail,
			RequesterPhone: authCtx.CanonicalGuest.Phone,
		})
		if err != nil {
			return err
		}
		if _, err := s.store.AddGuestEnquiryMessage(ctx, tx, domain.GuestEnquiryMessage{
			PropertyID:  authCtx.Property.ID,
			EnquiryID:   createdEnq.ID,
			Direction:   domain.CommDirInbound,
			SenderName:  authCtx.CanonicalGuest.FullName,
			SenderEmail: authCtx.Identity.VerifiedEmail,
			Body:        body,
			IsInternal:  false,
		}); err != nil {
			return err
		}

		subj := fmt.Sprintf("%s — Personal Data Erasure Request Recorded", authCtx.Property.Name)
		txt := fmt.Sprintf("Hello %s,\n\nWe have recorded your personal data erasure request for review by our team.\n", authCtx.CanonicalGuest.FullName)
		htm := fmt.Sprintf("<p>Hello %s,</p><p>We have recorded your personal data erasure request for review by our team.</p>", html.EscapeString(authCtx.CanonicalGuest.FullName))
		_ = s.queuePortalOperationalNotificationTx(ctx, tx, authCtx.Property.ID, canonID, nil, &createdEnq.ID, authCtx.Identity.VerifiedEmail, "portal_erasure_ack", subj, "Personal data erasure request receipt queued", txt, htm, nowUTC)

		return s.recordAudit(ctx, tx, portalActor(meta), &authCtx.Property.ID, "guest.erasure_requested", "guest", canonID.String(), nil, map[string]any{
			"guest_id":    canonID,
			"property_id": authCtx.Property.ID,
			"enquiry_id":  createdEnq.ID,
		})
	})
	if err != nil {
		return domain.PortalPrivacyRequestReceipt{}, err
	}
	return domain.PortalPrivacyRequestReceipt{
		Accepted: true,
		Message:  portalPrivacyReceiptMessage,
	}, nil
}

// --- Operational Support Recovery Methods (Section 9.2; Never Mint Sessions or Reveal Codes) ---

func (s *Service) SendGuestPortalVerification(
	ctx context.Context,
	actor Actor,
	propertyID, guestID uuid.UUID,
) error {
	if err := s.requirePerm(actor, auth.PermCommsWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	otpKey, err := s.portalOTPHMACKey()
	if err != nil {
		return err
	}
	nowUTC := s.nowUTC()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		prop, err := s.store.GetPropertyByID(ctx, tx, propertyID)
		if err != nil {
			return err
		}
		pubProp, err := s.store.GetPublicPropertyBySlug(ctx, tx, prop.Slug)
		if err != nil || !s.cfg.GuestPortalEnabled || !pubProp.Policy.GuestPortalEnabled {
			return fmt.Errorf("%w: public guest portal is disabled for this property", domain.ErrConflict)
		}
		canonID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, propertyID, guestID)
		if err != nil {
			return err
		}
		if anonOrPending {
			return fmt.Errorf("%w: cannot send portal verification to an anonymized or erasure-pending guest", domain.ErrConflict)
		}
		guest, err := s.store.GetGuest(ctx, tx, propertyID, canonID)
		if err != nil {
			return err
		}
		cleanEmail, err := domain.ValidateEmailRecipient(guest.Email)
		if err != nil || domain.IsOTAProxyEmail(cleanEmail) {
			return domain.NewValidationError(map[string]string{
				"email": "guest must have a valid personal (non-OTA-proxy) email address before sending portal verification",
			})
		}
		normEmail := domain.NormalizeEmail(cleanEmail)
		if err := s.store.InvalidateActivePortalChallengesTx(ctx, tx, propertyID, normEmail, domain.PortalChallengeLogin, nowUTC); err != nil {
			return err
		}
		chID, err := postgres.NewUUID()
		if err != nil {
			return err
		}
		otpPlain, err := postgres.GeneratePortalOTPCode()
		if err != nil {
			return err
		}
		tokDummy, err := postgres.GeneratePortalRandomHex(32)
		if err != nil {
			return err
		}
		otpHash, err := postgres.HashPortalOTP(otpKey, chID, normEmail, otpPlain)
		if err != nil {
			return err
		}
		var identID *uuid.UUID
		if ident, iErr := s.store.GetPortalIdentityByGuest(ctx, tx, propertyID, canonID); iErr == nil {
			idCopy := ident.ID
			identID = &idCopy
		}
		if _, err := s.store.CreatePortalChallengeTx(ctx, tx, domain.GuestPortalChallenge{
			ID:               chID,
			PropertyID:       propertyID,
			PortalIdentityID: identID,
			GuestID:          &canonID,
			NormalizedEmail:  normEmail,
			Purpose:          domain.PortalChallengeLogin,
			OTPHash:          otpHash,
			OTPHashAlg:       domain.PortalOTPHashAlgHMACSHA256V1,
			TokenHash:        postgres.HashPortalValue(tokDummy),
			MaxAttempts:      domain.PortalChallengeMaxAttempts,
			ExpiresAt:        nowUTC.Add(domain.PortalChallengeTTL),
		}); err != nil {
			return err
		}

		portalURL := s.portalPublicBaseURL(pubProp)
		subj := fmt.Sprintf("%s — Guest Portal Verification Code", prop.Name)
		txt := fmt.Sprintf("Hello %s,\n\nYour %s Guest Portal verification code is: %s\n\nEnter this code at %s/portal within 15 minutes to access your bookings.\n", guest.FullName, prop.Name, otpPlain, portalURL)
		htm := fmt.Sprintf("<p>Hello %s,</p><p>Your <strong>%s</strong> Guest Portal verification code is: <strong style=\"font-family:monospace;font-size:20px;\">%s</strong></p><p>Enter this code at <a href=\"%s/portal\">%s/portal</a> within 15 minutes.</p>", html.EscapeString(guest.FullName), html.EscapeString(prop.Name), html.EscapeString(otpPlain), html.EscapeString(portalURL), html.EscapeString(portalURL))
		if err := s.queuePortalSecurityChallengeEmailTx(ctx, tx, propertyID, &canonID, cleanEmail, "portal_verification", subj, txt, htm, nowUTC); err != nil {
			return err
		}

		uid := actor.UserID
		if _, err := s.store.CreateGuestCommunication(ctx, tx, domain.GuestCommunication{
			PropertyID:      propertyID,
			GuestID:         &canonID,
			InteractionType: domain.InteractionSystemNotification,
			Direction:       domain.CommDirOutbound,
			Subject:         subj,
			BodyText:        "Portal verification code dispatched to guest email (code withheld from staff logs).",
			Summary:         "Portal verification sent by " + actor.FullName,
			RecipientEmail:  cleanEmail,
			SenderEmail:     s.cfg.EmailFrom,
			DeliveryStatus:  domain.DeliveryLogged,
			StaffUserID:     &uid,
			OccurredAt:      nowUTC,
		}); err != nil {
			return err
		}

		return s.recordAudit(ctx, tx, actor, &propertyID, "guest_portal_identity.staff_verification_sent", "guest_portal_identity", canonID.String(), nil, map[string]any{
			"id":           chID,
			"property_id":  propertyID,
			"guest_id":     canonID,
			"status":       string(domain.PortalIdentityActive),
			"purpose":      string(domain.PortalChallengeLogin),
			"challenge_id": chID,
			"created_at":   nowUTC,
			"updated_at":   nowUTC,
		})
	})
}

func (s *Service) RevokeGuestPortalSessions(
	ctx context.Context,
	actor Actor,
	propertyID, guestID uuid.UUID,
) (int64, error) {
	if err := s.requirePerm(actor, auth.PermGuestsWrite); err != nil {
		return 0, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return 0, err
	}
	nowUTC := s.nowUTC()
	var revoked int64
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		canonID, clusterIDs, _, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, propertyID, guestID)
		if err != nil {
			return err
		}
		revoked, err = s.store.RevokeAllGuestPortalAccessTx(ctx, tx, propertyID, clusterIDs, nowUTC)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "guest_portal_session.revoked_by_staff", "guest_portal_session", canonID.String(), nil, map[string]any{
			"id":                  canonID,
			"session_id":          canonID,
			"property_id":         propertyID,
			"scope_type":          string(domain.PortalScopeAccount),
			"revoked_count":       revoked,
			"idle_expires_at":     nowUTC,
			"absolute_expires_at": nowUTC,
			"revoked_at":          nowUTC,
			"created_at":          nowUTC,
		})
	})
	return revoked, err
}
