package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/mailer"
	"github.com/tirbach/tir-bach/internal/payments"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

type PublicAccommodationCatalog struct {
	Property  domain.PublicPropertyView   `json:"property"`
	UnitTypes []domain.PublicUnitTypeView `json:"unit_types"`
	Extras    []domain.PublicExtraView    `json:"extras"`
}

type PublicAccommodationDetail struct {
	Property domain.PublicPropertyView `json:"property"`
	UnitType domain.PublicUnitTypeView `json:"unit_type"`
	Extras   []domain.PublicExtraView  `json:"extras"`
}

type PublicAvailabilityResponse struct {
	Property    domain.PublicPropertyView       `json:"property"`
	ArrivalDate string                          `json:"arrival_date"`
	Departure   string                          `json:"departure_date"`
	Nights      int                             `json:"nights"`
	Adults      int                             `json:"adults"`
	Children    int                             `json:"children"`
	Infants     int                             `json:"infants"`
	Items       []domain.AvailabilitySearchItem `json:"items"`
}

type CreateHoldRequest struct {
	PropertySlug    string                       `json:"property_slug"`
	UnitTypeCode    string                       `json:"unit_type_code"`
	ArrivalDate     time.Time                    `json:"-"`
	DepartureDate   time.Time                    `json:"-"`
	Adults          int                          `json:"adults"`
	Children        int                          `json:"children"`
	Infants         int                          `json:"infants"`
	Extras          []domain.QuoteExtraSelection `json:"extras"`
	GuestName       string                       `json:"guest_name"`
	GuestEmail      string                       `json:"guest_email"`
	GuestPhone      string                       `json:"guest_phone"`
	SpecialRequests string                       `json:"special_requests"`
	AcceptTerms     bool                         `json:"accept_terms"`
	SessionToken    string                       `json:"session_token"`
}

type CreateHoldResult struct {
	Hold              domain.BookingHold  `json:"hold"`
	Quote             domain.BookingQuote `json:"quote"`
	ConfirmationToken string              `json:"confirmation_token"`
}

type CreateCheckoutRequest struct {
	HoldID         uuid.UUID `json:"hold_id"`
	IdempotencyKey string    `json:"idempotency_key"`
}

type CreateCheckoutResponse struct {
	Hold              domain.BookingHold    `json:"hold"`
	Attempt           domain.PaymentAttempt `json:"payment_attempt"`
	CheckoutURL       string                `json:"checkout_url"`
	PaymentAttemptID  uuid.UUID             `json:"payment_attempt_id"`
	HoldExpiresAt     time.Time             `json:"hold_expires_at"`
	ConfirmationToken string                `json:"confirmation_token,omitempty"`
}

type ReservationFinancials struct {
	Reservation domain.Reservation     `json:"reservation"`
	Quote       *domain.BookingQuote   `json:"quote,omitempty"`
	Payments    []domain.PaymentRecord `json:"payments"`
}

func (s *Service) GetPublicProperty(ctx context.Context, slug string) (domain.PublicPropertyView, error) {
	return s.store.GetPublicPropertyBySlug(ctx, nil, slug)
}

func (s *Service) ListPublicAccommodation(ctx context.Context, slug string) (PublicAccommodationCatalog, error) {
	prop, err := s.store.GetPublicPropertyBySlug(ctx, nil, slug)
	if err != nil {
		return PublicAccommodationCatalog{}, err
	}
	uts, err := s.store.ListPublicUnitTypes(ctx, nil, prop.ID)
	if err != nil {
		return PublicAccommodationCatalog{}, err
	}
	extras, err := s.store.ListPublicExtras(ctx, nil, prop.ID)
	if err != nil {
		return PublicAccommodationCatalog{}, err
	}
	return PublicAccommodationCatalog{
		Property:  prop,
		UnitTypes: uts,
		Extras:    extras,
	}, nil
}

func (s *Service) GetPublicAccommodationDetail(ctx context.Context, slug, code string) (PublicAccommodationDetail, error) {
	prop, err := s.store.GetPublicPropertyBySlug(ctx, nil, slug)
	if err != nil {
		return PublicAccommodationDetail{}, err
	}
	ut, err := s.store.GetPublicUnitTypeByCode(ctx, nil, prop.ID, code)
	if err != nil {
		return PublicAccommodationDetail{}, err
	}
	extras, err := s.store.ListPublicExtras(ctx, nil, prop.ID)
	if err != nil {
		return PublicAccommodationDetail{}, err
	}
	return PublicAccommodationDetail{
		Property: prop,
		UnitType: ut,
		Extras:   extras,
	}, nil
}

func (s *Service) validatePublicSearchDates(prop domain.PublicPropertyView, startDate, endDate time.Time) (int, error) {
	loc, err := time.LoadLocation(prop.Timezone)
	if err != nil {
		loc = time.UTC
	}
	nowLocal := s.nowUTC().In(loc)
	todayLocal := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, time.UTC)

	sNorm := time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, time.UTC)
	eNorm := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 0, 0, 0, 0, time.UTC)

	if sNorm.Before(todayLocal) {
		return 0, domain.NewValidationError(map[string]string{
			"arrival_date": "arrival date must be today or in the future",
		})
	}
	if !eNorm.After(sNorm) {
		return 0, domain.NewValidationError(map[string]string{
			"departure_date": "departure date must be after arrival date",
		})
	}
	nights := int(eNorm.Sub(sNorm).Hours() / 24)
	if nights > domain.MaxRatePreviewNights {
		return 0, domain.NewValidationError(map[string]string{
			"date_range": fmt.Sprintf("stay cannot exceed %d nights", domain.MaxRatePreviewNights),
		})
	}
	return nights, nil
}

// SearchPublicAvailability queries PostgreSQL for genuinely available accommodation categories
// for DIRECT online bookings across claims, holds, closures, channel allocations, release rules,
// occupancy capacities, and min/max stay requirements.
func (s *Service) SearchPublicAvailability(ctx context.Context, in domain.AvailabilitySearchInput) (PublicAvailabilityResponse, error) {
	prop, err := s.store.GetPublicPropertyBySlug(ctx, nil, in.PropertySlug)
	if err != nil {
		return PublicAvailabilityResponse{}, err
	}
	if in.Adults < 1 || in.Children < 0 || in.Infants < 0 {
		return PublicAvailabilityResponse{}, domain.NewValidationError(map[string]string{
			"party": "at least 1 adult is required and children/infants cannot be negative",
		})
	}
	nights, err := s.validatePublicSearchDates(prop, in.ArrivalDate, in.Departure)
	if err != nil {
		return PublicAvailabilityResponse{}, err
	}

	// Best-effort cleanup of expired holds before calculating live availability.
	nowUTC := s.nowUTC()
	_, _ = s.store.ExpireStaleHolds(ctx, nowUTC)

	publicUTs, err := s.store.ListPublicUnitTypes(ctx, nil, prop.ID)
	if err != nil {
		return PublicAvailabilityResponse{}, err
	}
	allUTs, err := s.store.ListUnitTypes(ctx, prop.ID, false)
	if err != nil {
		return PublicAvailabilityResponse{}, err
	}
	fullUTByID := make(map[uuid.UUID]domain.UnitType, len(allUTs))
	for _, ut := range allUTs {
		fullUTByID[ut.ID] = ut
	}

	plan, err := s.store.GetDefaultRatePlan(ctx, nil, prop.ID)
	if err != nil {
		return PublicAvailabilityResponse{}, err
	}
	rules, err := s.store.ListRateRules(ctx, prop.ID, &plan.ID, nil)
	if err != nil {
		return PublicAvailabilityResponse{}, err
	}
	units, err := s.store.ListActiveUnitsForPublicBooking(ctx, nil, prop.ID, nil)
	if err != nil {
		return PublicAvailabilityResponse{}, err
	}
	releaseRules, err := s.store.ListActiveReleaseRulesInRange(ctx, nil, prop.ID, in.ArrivalDate, in.Departure)
	if err != nil {
		return PublicAvailabilityResponse{}, err
	}
	claims, err := s.store.ListActiveClaimsInRange(ctx, prop.ID, in.ArrivalDate, in.Departure)
	if err != nil {
		return PublicAvailabilityResponse{}, err
	}

	// Index blocking claims by unit for O(1) availability evaluation per physical unit.
	blockedUnit := make(map[uuid.UUID]bool)
	for _, c := range claims {
		if c.ClaimType == domain.ClaimChannelRestriction {
			if c.ChannelScope == "DIRECT" {
				blockedUnit[c.UnitID] = true
			}
			continue
		}
		if c.ClaimType == domain.ClaimChannelAllocation && c.ChannelScope == "DIRECT" {
			// Direct allocation is explicitly open to DIRECT bookings.
			continue
		}
		blockedUnit[c.UnitID] = true
	}

	availableCountByType := make(map[uuid.UUID]int)
	for _, u := range units {
		if blockedUnit[u.ID] {
			continue
		}
		if allowed, _, _ := domain.EvaluateReleaseForStay(releaseRules, u, "DIRECT", in.ArrivalDate, in.Departure, nowUTC); !allowed {
			continue
		}
		availableCountByType[u.UnitTypeID]++
	}

	domainProp := domain.Property{
		ID:            prop.ID,
		Currency:      prop.Currency,
		Timezone:      prop.Timezone,
		VATRegistered: prop.VATRegistered,
	}

	catFilter := strings.TrimSpace(strings.ToLower(in.Category))
	items := make([]domain.AvailabilitySearchItem, 0, len(publicUTs))
	totalHeadcount := in.Adults + in.Children + in.Infants

	for _, put := range publicUTs {
		if catFilter != "" && string(put.Category) != catFilter {
			continue
		}
		cnt := availableCountByType[put.ID]
		if cnt <= 0 {
			continue
		}
		if in.Adults > put.MaxAdults || in.Children > put.MaxChildren || in.Infants > put.MaxInfants || totalHeadcount > put.MaxGuests {
			continue
		}
		fullUT, ok := fullUTByID[put.ID]
		if !ok {
			continue
		}
		fullUT.IsPublicBookable = true
		quote, err := domain.ComputePublicQuote(domainProp, prop.Policy, fullUT, plan, rules, nil, nil, in.ArrivalDate, in.Departure, in.Adults, in.Children, in.Infants, nowUTC)
		if err != nil {
			// Stay rule / min-nights mismatch skips unit type from bookable list cleanly.
			continue
		}
		items = append(items, domain.AvailabilitySearchItem{
			UnitType:           put,
			AvailableUnits:     cnt,
			RatePlanID:         plan.ID,
			RatePlanName:       plan.Name,
			Nights:             nights,
			EffectiveMinStay:   put.MinNights,
			EffectiveMaxStay:   put.MaxNights,
			MeetsStayRules:     true,
			AccommodationPence: quote.AccommodationSubtotalPence,
			SurchargePence:     quote.OccupancySurchargePence,
			TaxPence:           quote.TaxPence,
			TotalPence:         quote.TotalPence,
			DepositDuePence:    quote.DepositDuePence,
			BalanceDuePence:    quote.BalanceDuePence,
			Currency:           prop.Currency,
		})
	}

	return PublicAvailabilityResponse{
		Property:    prop,
		ArrivalDate: domain.FormatDate(in.ArrivalDate),
		Departure:   domain.FormatDate(in.Departure),
		Nights:      nights,
		Adults:      in.Adults,
		Children:    in.Children,
		Infants:     in.Infants,
		Items:       items,
	}, nil
}

// ComputePublicPricing builds and persists an authoritative server-computed price quote snapshot.
func (s *Service) ComputePublicPricing(
	ctx context.Context,
	propertySlug, unitTypeCode string,
	startDate, endDate time.Time,
	adults, children, infants int,
	extras []domain.QuoteExtraSelection,
) (domain.BookingQuote, error) {
	prop, err := s.store.GetPublicPropertyBySlug(ctx, nil, propertySlug)
	if err != nil {
		return domain.BookingQuote{}, err
	}
	if _, err := s.validatePublicSearchDates(prop, startDate, endDate); err != nil {
		return domain.BookingQuote{}, err
	}
	utView, err := s.store.GetPublicUnitTypeByCode(ctx, nil, prop.ID, unitTypeCode)
	if err != nil {
		return domain.BookingQuote{}, err
	}
	fullUT, err := s.store.GetUnitType(ctx, nil, prop.ID, utView.ID)
	if err != nil {
		return domain.BookingQuote{}, err
	}
	fullUT.IsPublicBookable = true

	plan, err := s.store.GetDefaultRatePlan(ctx, nil, prop.ID)
	if err != nil {
		return domain.BookingQuote{}, err
	}
	rules, err := s.store.ListRateRules(ctx, prop.ID, &plan.ID, &fullUT.ID)
	if err != nil {
		return domain.BookingQuote{}, err
	}
	allExtras, err := s.store.ListExtras(ctx, prop.ID, false)
	if err != nil {
		return domain.BookingQuote{}, err
	}
	extrasByID := make(map[uuid.UUID]domain.Extra, len(allExtras))
	for _, ex := range allExtras {
		extrasByID[ex.ID] = ex
	}

	domainProp := domain.Property{ID: prop.ID, Currency: prop.Currency, Timezone: prop.Timezone, VATRegistered: prop.VATRegistered}
	quote, err := domain.ComputePublicQuote(domainProp, prop.Policy, fullUT, plan, rules, extrasByID, extras, startDate, endDate, adults, children, infants, s.nowUTC())
	if err != nil {
		return domain.BookingQuote{}, err
	}
	qID, err := postgres.NewUUID()
	if err != nil {
		return domain.BookingQuote{}, err
	}
	quote.ID = qID

	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		saved, txErr := s.store.InsertBookingQuote(ctx, tx, quote)
		if txErr != nil {
			return txErr
		}
		quote = saved
		return nil
	})
	return quote, err
}

// CreatePublicHold atomically validates customer inputs, recalculates pricing server-side,
// and secures an exclusive temporary HOLD claim in PostgreSQL.
func (s *Service) CreatePublicHold(ctx context.Context, meta Actor, req CreateHoldRequest) (CreateHoldResult, error) {
	if err := domain.ValidateCustomerInfo(req.GuestName, req.GuestEmail, req.GuestPhone, req.AcceptTerms); err != nil {
		return CreateHoldResult{}, err
	}
	prop, err := s.store.GetPublicPropertyBySlug(ctx, nil, req.PropertySlug)
	if err != nil {
		return CreateHoldResult{}, err
	}
	if _, err := s.validatePublicSearchDates(prop, req.ArrivalDate, req.DepartureDate); err != nil {
		return CreateHoldResult{}, err
	}
	utView, err := s.store.GetPublicUnitTypeByCode(ctx, nil, prop.ID, req.UnitTypeCode)
	if err != nil {
		return CreateHoldResult{}, err
	}

	fullUT, err := s.store.GetUnitType(ctx, nil, prop.ID, utView.ID)
	if err != nil {
		return CreateHoldResult{}, err
	}
	fullUT.IsPublicBookable = true

	plan, err := s.store.GetDefaultRatePlan(ctx, nil, prop.ID)
	if err != nil {
		return CreateHoldResult{}, err
	}
	rules, err := s.store.ListRateRules(ctx, prop.ID, &plan.ID, &fullUT.ID)
	if err != nil {
		return CreateHoldResult{}, err
	}
	allExtras, err := s.store.ListExtras(ctx, prop.ID, false)
	if err != nil {
		return CreateHoldResult{}, err
	}
	extrasByID := make(map[uuid.UUID]domain.Extra, len(allExtras))
	for _, ex := range allExtras {
		extrasByID[ex.ID] = ex
	}

	nowUTC := s.nowUTC()
	domainProp := domain.Property{ID: prop.ID, Currency: prop.Currency, Timezone: prop.Timezone, VATRegistered: prop.VATRegistered}
	quote, err := domain.ComputePublicQuote(domainProp, prop.Policy, fullUT, plan, rules, extrasByID, req.Extras, req.ArrivalDate, req.DepartureDate, req.Adults, req.Children, req.Infants, nowUTC)
	if err != nil {
		return CreateHoldResult{}, err
	}
	qID, err := postgres.NewUUID()
	if err != nil {
		return CreateHoldResult{}, err
	}
	quote.ID = qID

	holdID, err := postgres.NewUUID()
	if err != nil {
		return CreateHoldResult{}, err
	}
	sessionHash := ""
	if strings.TrimSpace(req.SessionToken) != "" {
		sum := sha256.Sum256([]byte(req.SessionToken))
		sessionHash = hex.EncodeToString(sum[:])
	}

	holdDur := time.Duration(prop.Policy.HoldDurationMinutes) * time.Minute
	if holdDur <= 0 {
		holdDur = 15 * time.Minute
	}
	termsAcceptedAt := nowUTC
	hold := domain.BookingHold{
		ID:                  holdID,
		PropertyID:          prop.ID,
		UnitTypeID:          fullUT.ID,
		RatePlanID:          plan.ID,
		ArrivalDate:         req.ArrivalDate,
		DepartureDate:       req.DepartureDate,
		Adults:              req.Adults,
		Children:            req.Children,
		Infants:             req.Infants,
		GuestName:           strings.TrimSpace(req.GuestName),
		GuestEmail:          strings.TrimSpace(req.GuestEmail),
		GuestPhone:          strings.TrimSpace(req.GuestPhone),
		SpecialRequests:     strings.TrimSpace(req.SpecialRequests),
		TermsVersion:        prop.Policy.BookingTermsVersion,
		TermsAcceptedAt:     &termsAcceptedAt,
		CustomerSessionHash: sessionHash,
		ExpiresAt:           nowUTC.Add(holdDur),
	}

	var out CreateHoldResult
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		savedHold, savedQuote, txErr := s.store.CreateHoldWithClaim(ctx, tx, hold, quote, nowUTC)
		if txErr != nil {
			return txErr
		}
		initialTokenExpiry := savedHold.ExpiresAt.Add(2 * time.Hour)
		tok, txErr := s.store.CreateConfirmationToken(ctx, tx, prop.ID, &savedHold.ID, nil, initialTokenExpiry)
		if txErr != nil {
			return txErr
		}
		metaActor := Actor{
			Email:     "guest@public.lovelace",
			Role:      "public_guest",
			RequestID: meta.RequestID,
			IPAddress: meta.IPAddress,
			UserAgent: meta.UserAgent,
		}
		if err := s.recordAudit(ctx, tx, metaActor, &prop.ID, "booking_hold.created", "booking_hold", savedHold.ID.String(), nil, auditBookingHoldSnapshot(savedHold)); err != nil {
			return err
		}
		out = CreateHoldResult{
			Hold:              savedHold,
			Quote:             savedQuote,
			ConfirmationToken: tok,
		}
		return nil
	})
	return out, err
}

func (s *Service) CancelPublicHold(ctx context.Context, meta Actor, holdID uuid.UUID) (domain.BookingHold, error) {
	var cancelled domain.BookingHold
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetBookingHoldForUpdate(ctx, tx, holdID)
		if err != nil {
			return err
		}
		if before.Status != domain.HoldStatusActive {
			cancelled = before
			return nil
		}
		cancelled, err = s.store.CancelBookingHold(ctx, tx, holdID)
		if err != nil {
			return err
		}
		metaActor := Actor{
			Email:     "guest@public.lovelace",
			Role:      "public_guest",
			RequestID: meta.RequestID,
			IPAddress: meta.IPAddress,
			UserAgent: meta.UserAgent,
		}
		return s.recordAudit(ctx, tx, metaActor, &before.PropertyID, "booking_hold.cancelled", "booking_hold", holdID.String(), auditBookingHoldSnapshot(before), auditBookingHoldSnapshot(cancelled))
	})
	return cancelled, err
}

func (s *Service) InitiatePublicCheckout(ctx context.Context, meta Actor, req CreateCheckoutRequest) (CreateCheckoutResponse, error) {
	if s.cfg.IsProduction() {
		if s.cfg.StripeSandbox {
			return CreateCheckoutResponse{}, fmt.Errorf("%w: sandbox checkout is disabled in production", domain.ErrConflict)
		}
		if !s.cfg.IsStripeConfigured() {
			return CreateCheckoutResponse{}, fmt.Errorf("%w: live card checkout is not validly configured on the server", domain.ErrStripeDisabled)
		}
		if !s.HasEmailProvider() {
			return CreateCheckoutResponse{}, fmt.Errorf("%w: confirmation email delivery is not configured on the server", domain.ErrStripeDisabled)
		}
		if len(s.cfg.AllowedPublicOrigins()) == 0 {
			return CreateCheckoutResponse{}, domain.NewValidationError(map[string]string{"checkout": "production public origin is not configured"})
		}
	} else if s.payProvider == nil || strings.TrimSpace(s.cfg.StripeWebhookSecret) == "" {
		return CreateCheckoutResponse{}, fmt.Errorf("%w: online card checkout is not configured on the server", domain.ErrStripeDisabled)
	}
	nowUTC := s.nowUTC()

	var hold domain.BookingHold
	var quote *domain.BookingQuote
	var prop domain.Property
	var customDomain string
	var policy domain.PropertyBookingPolicy
	var attempt domain.PaymentAttempt
	var alreadyExisted bool

	idemKey := strings.TrimSpace(req.IdempotencyKey)
	if idemKey == "" {
		idemKey = "checkout-" + req.HoldID.String()
	}

	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		hold, txErr = s.store.GetBookingHoldForUpdate(ctx, tx, req.HoldID)
		if txErr != nil {
			return txErr
		}
		if hold.Status != domain.HoldStatusActive || !hold.ExpiresAt.After(nowUTC) {
			if hold.Status == domain.HoldStatusActive {
				_, _ = tx.Exec(ctx, `UPDATE booking_holds SET status = 'EXPIRED', version = version + 1, updated_at = NOW() WHERE id = $1`, hold.ID)
				_, _ = tx.Exec(ctx, `UPDATE inventory_claims SET status = 'RELEASED', released_at = NOW(), updated_at = NOW() WHERE hold_id = $1 AND status = 'ACTIVE'`, hold.ID)
				_, _ = tx.Exec(ctx, `UPDATE payment_attempts SET status = 'EXPIRED', failure_reason = 'booking hold expired', updated_at = NOW() WHERE hold_id = $1 AND status IN ('CREATED', 'REQUIRES_ACTION')`, hold.ID)
			}
			return fmt.Errorf("%w: booking hold has expired, please search and select again", domain.ErrConflict)
		}
		prop, txErr = s.store.GetPropertyByID(ctx, tx, hold.PropertyID)
		if txErr != nil {
			return txErr
		}
		if pubProp, pubErr := s.store.GetPublicPropertyBySlug(ctx, tx, prop.Slug); pubErr == nil {
			customDomain = strings.TrimSpace(pubProp.CustomDomain)
		}
		policy, txErr = s.store.GetBookingPolicy(ctx, tx, hold.PropertyID)
		if txErr != nil {
			return txErr
		}
		if !policy.StripeEnabled {
			return domain.ErrStripeDisabled
		}
		quote, txErr = s.store.GetLatestQuoteForHoldOrReservation(ctx, tx, hold.PropertyID, &hold.ID, nil)
		if txErr != nil || quote == nil {
			if txErr == nil {
				txErr = domain.ErrNotFound
			}
			return txErr
		}

		payType := "FULL"
		if quote.BalanceDuePence > 0 {
			payType = "DEPOSIT"
		}
		attID, txErr := postgres.NewUUID()
		if txErr != nil {
			return txErr
		}
		attempt, alreadyExisted, txErr = s.store.CreateOrGetPaymentAttempt(ctx, tx, domain.PaymentAttempt{
			ID:             attID,
			PropertyID:     hold.PropertyID,
			HoldID:         &hold.ID,
			Provider:       "stripe",
			Mode:           "checkout_session",
			PaymentType:    payType,
			AmountPence:    quote.DepositDuePence,
			Currency:       quote.Currency,
			Status:         domain.AttemptStatusCreated,
			IdempotencyKey: idemKey,
		})
		return txErr
	})
	if err != nil {
		return CreateCheckoutResponse{}, err
	}

	if alreadyExisted {
		if attempt.CheckoutURL == "" && attempt.Status == domain.AttemptStatusCreated {
			// Wait briefly for an identical in-flight attempt to finish creating its checkout session.
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
				refreshed, rErr := s.store.GetPaymentAttemptByIDOrSession(ctx, nil, &attempt.ID, "")
				if rErr == nil {
					attempt = refreshed
					if attempt.CheckoutURL != "" || attempt.Status != domain.AttemptStatusCreated {
						break
					}
				}
			}
		}
		if attempt.Status == domain.AttemptStatusFailed || attempt.Status == domain.AttemptStatusExpired {
			return CreateCheckoutResponse{}, fmt.Errorf("%w: previous checkout attempt ended (%s); please retry checkout", domain.ErrConflict, strings.ToLower(attempt.Status))
		}
		if attempt.CheckoutURL == "" {
			return CreateCheckoutResponse{}, domain.ErrActiveCheckoutExists
		}
		return CreateCheckoutResponse{
			Hold:             hold,
			Attempt:          attempt,
			CheckoutURL:      attempt.CheckoutURL,
			PaymentAttemptID: attempt.ID,
			HoldExpiresAt:    hold.ExpiresAt,
		}, nil
	}

	allowedOrigins := s.cfg.AllowedPublicOrigins()
	if !s.cfg.IsProduction() && len(allowedOrigins) == 0 && customDomain != "" {
		allowedOrigins = append(allowedOrigins, "https://"+customDomain)
	}
	var approvedDomains []string
	if customDomain != "" {
		approvedDomains = append(approvedDomains, customDomain)
	}
	primaryOrigin := ""
	if len(allowedOrigins) > 0 {
		primaryOrigin = allowedOrigins[0]
	}

	rawSuccess := strings.TrimSpace(s.cfg.StripeSuccessURL)
	if rawSuccess == "" && primaryOrigin != "" {
		rawSuccess = primaryOrigin + "/booking/confirmation?ref=" + hold.HoldReference + "&session_id={CHECKOUT_SESSION_ID}"
	}
	rawCancel := strings.TrimSpace(s.cfg.StripeCancelURL)
	if rawCancel == "" && primaryOrigin != "" {
		rawCancel = primaryOrigin + "/book?cancelled=1"
	}

	successURL, sErr := payments.ValidateCheckoutReturnURL(rawSuccess, allowedOrigins, approvedDomains, s.cfg.IsProduction())
	if sErr != nil {
		_ = s.store.MarkPaymentAttemptFailed(ctx, nil, attempt.ID, "invalid server checkout success URL: "+sErr.Error())
		return CreateCheckoutResponse{}, domain.NewValidationError(map[string]string{"checkout": "invalid server checkout success URL: " + sErr.Error()})
	}
	cancelURL, cErr := payments.ValidateCheckoutReturnURL(rawCancel, allowedOrigins, approvedDomains, s.cfg.IsProduction())
	if cErr != nil {
		_ = s.store.MarkPaymentAttemptFailed(ctx, nil, attempt.ID, "invalid server checkout cancel URL: "+cErr.Error())
		return CreateCheckoutResponse{}, domain.NewValidationError(map[string]string{"checkout": "invalid server checkout cancel URL: " + cErr.Error()})
	}

	session, err := s.payProvider.CreateCheckoutSession(payments.CheckoutRequest{
		IdempotencyKey:   idemKey,
		AmountPence:      attempt.AmountPence,
		Currency:         attempt.Currency,
		Description:      fmt.Sprintf("%s — %s (%s to %s)", prop.Name, quote.UnitTypeName, hold.Arrival, hold.Departure),
		CustomerEmail:    hold.GuestEmail,
		SuccessURL:       successURL,
		CancelURL:        cancelURL,
		PropertyID:       hold.PropertyID.String(),
		HoldID:           hold.ID.String(),
		HoldReference:    hold.HoldReference,
		PaymentAttemptID: attempt.ID.String(),
	})
	if err != nil {
		_ = s.store.MarkPaymentAttemptFailed(ctx, nil, attempt.ID, err.Error())
		return CreateCheckoutResponse{}, fmt.Errorf("create payment session: %w", err)
	}

	if err := s.store.UpdatePaymentAttemptSession(ctx, nil, attempt.ID, session.SessionID, session.PaymentIntentID, session.CheckoutURL, domain.AttemptStatusRequiresAction); err != nil {
		return CreateCheckoutResponse{}, err
	}
	attempt.ProviderSessionID = session.SessionID
	attempt.ProviderPaymentIntentID = session.PaymentIntentID
	attempt.CheckoutURL = session.CheckoutURL
	attempt.Status = domain.AttemptStatusRequiresAction

	return CreateCheckoutResponse{
		Hold:             hold,
		Attempt:          attempt,
		CheckoutURL:      session.CheckoutURL,
		PaymentAttemptID: attempt.ID,
		HoldExpiresAt:    hold.ExpiresAt,
	}, nil
}

// WebhookResult describes the outcome of processing an incoming Stripe webhook event.
type WebhookResult struct {
	Duplicate bool   `json:"duplicate"`
	Rejected  bool   `json:"rejected,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type stripeWebhookEnvelope struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Created  int64  `json:"created"`
	LiveMode *bool  `json:"livemode"`
	Data     struct {
		Object struct {
			ID               string `json:"id"`
			PaymentIntent    string `json:"payment_intent"`
			Amount           int64  `json:"amount"`
			AmountTotal      int64  `json:"amount_total"`
			AmountReceived   int64  `json:"amount_received"`
			AmountRefunded   int64  `json:"amount_refunded"`
			Currency         string `json:"currency"`
			Created          int64  `json:"created"`
			PaymentStatus    string `json:"payment_status"`
			LatestCharge     string `json:"latest_charge"`
			LastPaymentError *struct {
				Message string `json:"message"`
			} `json:"last_payment_error"`
			Refunds *struct {
				Data []struct {
					ID       string `json:"id"`
					Amount   int64  `json:"amount"`
					Currency string `json:"currency"`
					Status   string `json:"status"`
					Reason   string `json:"reason"`
					Created  int64  `json:"created"`
				} `json:"data"`
			} `json:"refunds"`
			Metadata map[string]string `json:"metadata"`
		} `json:"object"`
	} `json:"data"`
}

func (s *Service) ProcessStripeWebhook(ctx context.Context, meta Actor, rawBody []byte, sigHeader string) (WebhookResult, error) {
	nowUTC := s.nowUTC()
	if err := payments.VerifyStripeSignature(rawBody, sigHeader, s.cfg.StripeWebhookSecret, 5*time.Minute, nowUTC); err != nil {
		return WebhookResult{}, fmt.Errorf("%w: %s", domain.ErrUnauthorized, err.Error())
	}

	var evt stripeWebhookEnvelope
	if err := json.Unmarshal(rawBody, &evt); err != nil || evt.ID == "" || evt.Type == "" || evt.LiveMode == nil {
		return WebhookResult{}, domain.NewValidationError(map[string]string{"payload": "malformed Stripe webhook event"})
	}

	sum := sha256.Sum256(rawBody)
	payloadHash := hex.EncodeToString(sum[:])

	var resOut WebhookResult
	var outboxQueueErr error

	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		seen, txErr := s.store.BeginWebhookEvent(ctx, tx, "stripe", evt.ID, evt.Type, evt.LiveMode, payloadHash)
		if txErr != nil {
			return txErr
		}
		if seen {
			resOut.Duplicate = true
			return nil
		}

		obj := evt.Data.Object
		attIDStr := obj.Metadata["payment_attempt_id"]
		var attPtr *uuid.UUID
		if attIDStr != "" {
			if id, pErr := uuid.Parse(attIDStr); pErr == nil {
				attPtr = &id
			}
		}

		whActor := Actor{
			Email:     "stripe@webhook.lovelace",
			Role:      "stripe_webhook",
			RequestID: meta.RequestID,
			IPAddress: meta.IPAddress,
			UserAgent: meta.UserAgent,
		}

		expectedLive := s.cfg.ExpectedStripeLiveMode()
		if *evt.LiveMode != expectedLive {
			var propIDPtr *uuid.UUID
			if attPtr != nil || obj.ID != "" {
				if att, aErr := s.store.GetPaymentAttemptByIDOrSession(ctx, tx, attPtr, obj.ID); aErr == nil {
					p := att.PropertyID
					propIDPtr = &p
				}
			}
			anomaly := map[string]any{
				"event_id":          evt.ID,
				"event_type":        evt.Type,
				"event_livemode":    *evt.LiveMode,
				"expected_livemode": expectedLive,
				"environment":       s.cfg.AppEnv,
				"payment_attempt":   attIDStr,
			}
			if aErr := s.recordAudit(ctx, tx, whActor, propIDPtr, "payment.webhook_livemode_mismatch", "payment_webhook_event", evt.ID, nil, anomaly); aErr != nil {
				return aErr
			}
			reason := fmt.Sprintf("livemode mismatch: event livemode=%v expected=%v", *evt.LiveMode, expectedLive)
			resOut.Rejected = true
			resOut.Reason = "livemode_mismatch"
			return s.store.FinishWebhookEvent(ctx, tx, "stripe", evt.ID, "REJECTED", reason)
		}

		switch evt.Type {
		case "checkout.session.completed", "payment_intent.succeeded":
			if evt.Type == "checkout.session.completed" && obj.PaymentStatus != "" && obj.PaymentStatus != "paid" {
				return s.store.FinishWebhookEvent(ctx, tx, "stripe", evt.ID, "IGNORED", "checkout session not yet paid")
			}

			att, err := s.store.GetPaymentAttemptByIDOrSession(ctx, tx, attPtr, obj.ID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return s.store.FinishWebhookEvent(ctx, tx, "stripe", evt.ID, "IGNORED", "payment attempt not found")
				}
				return err
			}

			paidAmt := obj.AmountTotal
			if paidAmt == 0 {
				paidAmt = obj.AmountReceived
			}
			piID := obj.PaymentIntent
			if evt.Type == "payment_intent.succeeded" && piID == "" {
				piID = obj.ID
			}
			res, err := s.store.ConfirmPaymentAndHold(ctx, tx, att.ID, piID, obj.LatestCharge, paidAmt, strings.ToUpper(obj.Currency), nowUTC)
			if err != nil {
				return err
			}

			if res.ExceptionRecorded != nil {
				ex := *res.ExceptionRecorded
				// Attempt best-effort automatic idempotent refund for expired hold paid or mismatch if provider available & PI known.
				if s.payProvider != nil && piID != "" && (ex.ExceptionType == "EXPIRED_HOLD_PAID" || ex.ExceptionType == "OVERLAP_ON_CONFIRM") {
					if refRes, rErr := s.payProvider.Refund(payments.RefundRequest{
						IdempotencyKey:  "auto-refund-" + att.ID.String(),
						PaymentIntentID: piID,
						AmountPence:     paidAmt,
						Reason:          ex.ExceptionType,
					}); rErr == nil && refRes.RefundID != "" {
						_, _ = tx.Exec(ctx, `UPDATE payment_exceptions SET status = 'AUTO_REFUNDED', resolution_notes = $2, resolved_at = NOW(), updated_at = NOW() WHERE id = $1`,
							ex.ID, "Automatic refund issued: "+refRes.RefundID)
						_, _ = tx.Exec(ctx, `UPDATE payment_attempts SET status = 'REFUNDED', updated_at = NOW() WHERE id = $1`, att.ID)
					}
				}
				if err := s.recordAudit(ctx, tx, whActor, &ex.PropertyID, "payment.reconciliation_required", "payment_exception", ex.ID.String(), nil, ex); err != nil {
					return err
				}
			} else if res.Reservation != nil && !res.AlreadyConfirmed {
				r := *res.Reservation
				if err := s.recordAudit(ctx, tx, whActor, &r.PropertyID, "reservation.confirmed_public", "reservation", r.ID.String(), nil, auditReservationSnapshot(r)); err != nil {
					return err
				}
				prop, _ := s.store.GetPropertyByID(ctx, tx, r.PropertyID)
				policy, _ := s.store.GetBookingPolicy(ctx, tx, r.PropertyID)
				quote, _ := s.store.GetLatestQuoteForHoldOrReservation(ctx, tx, r.PropertyID, nil, &r.ID)
				if quote != nil && r.GuestEmail != "" {
					subj, txt, htmlBody := mailer.BuildBookingConfirmation(prop, policy, r, *quote, paidAmt)
					if _, qErr := s.store.QueueEmail(ctx, tx, domain.EmailOutboxItem{
						PropertyID:     r.PropertyID,
						ReservationID:  &r.ID,
						GuestID:        &r.GuestID,
						RecipientEmail: r.GuestEmail,
						Template:       "booking_confirmation",
						Subject:        subj,
						BodyText:       txt,
						BodyHTML:       htmlBody,
						NextAttemptAt:  nowUTC,
					}); qErr != nil {
						outboxQueueErr = qErr
					}
				}
				if jErr := s.store.SyncReservationAutomationJobs(ctx, tx, prop, r, nowUTC); jErr != nil {
					return jErr
				}
			}
			return s.store.FinishWebhookEvent(ctx, tx, "stripe", evt.ID, "PROCESSED", "")

		case "checkout.session.expired", "payment_intent.payment_failed":
			att, err := s.store.GetPaymentAttemptByIDOrSession(ctx, tx, attPtr, obj.ID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return s.store.FinishWebhookEvent(ctx, tx, "stripe", evt.ID, "IGNORED", "payment attempt not found")
				}
				return err
			}
			reason := evt.Type
			if obj.LastPaymentError != nil && obj.LastPaymentError.Message != "" {
				reason = obj.LastPaymentError.Message
			}
			if err := s.store.RecordPaymentFailure(ctx, tx, att.ID, reason); err != nil {
				return err
			}
			if att.HoldID != nil {
				hold, hErr := s.store.GetBookingHoldForUpdate(ctx, tx, *att.HoldID)
				if hErr == nil && hold.Status == domain.HoldStatusActive && (evt.Type == "checkout.session.expired" || !hold.ExpiresAt.After(nowUTC)) {
					_, _ = s.store.CancelBookingHold(ctx, tx, hold.ID)
				}
			}
			return s.store.FinishWebhookEvent(ctx, tx, "stripe", evt.ID, "PROCESSED", "")

		case "charge.refunded":
			piID := obj.PaymentIntent
			if piID != "" {
				evtOccurredAt := nowUTC
				if evt.Created > 0 {
					evtOccurredAt = time.Unix(evt.Created, 0).UTC()
				}
				var entries []postgres.StripeRefundEntry
				if obj.Refunds != nil {
					for _, rf := range obj.Refunds.Data {
						if rf.Amount <= 0 {
							continue
						}
						st := "SUCCEEDED"
						switch strings.ToLower(strings.TrimSpace(rf.Status)) {
						case "pending", "requires_action":
							st = "PENDING"
						case "failed", "canceled":
							st = "FAILED"
						case "succeeded", "":
							st = "SUCCEEDED"
						}
						rfOccurredAt := evtOccurredAt
						if rf.Created > 0 {
							rfOccurredAt = time.Unix(rf.Created, 0).UTC()
						}
						cur := strings.ToUpper(strings.TrimSpace(rf.Currency))
						if cur == "" {
							cur = strings.ToUpper(strings.TrimSpace(obj.Currency))
						}
						idem := ""
						if strings.TrimSpace(rf.ID) != "" {
							idem = "stripe_refund:" + strings.TrimSpace(rf.ID)
						}
						entries = append(entries, postgres.StripeRefundEntry{
							ProviderRefundID: strings.TrimSpace(rf.ID),
							AmountPence:      rf.Amount,
							Currency:         cur,
							Status:           st,
							Reason:           strings.TrimSpace(rf.Reason),
							OccurredAt:       rfOccurredAt,
							IdempotencyKey:   idem,
						})
					}
				}
				if err := s.store.RecordRefundOutcome(
					ctx, tx, piID, obj.ID, evt.ID, evtOccurredAt,
					obj.AmountRefunded, strings.ToUpper(strings.TrimSpace(obj.Currency)), entries,
				); err != nil {
					return err
				}
			}
			return s.store.FinishWebhookEvent(ctx, tx, "stripe", evt.ID, "PROCESSED", "")

		default:
			return s.store.FinishWebhookEvent(ctx, tx, "stripe", evt.ID, "IGNORED", "unhandled event type")
		}
	})
	_ = outboxQueueErr
	if err != nil {
		return resOut, err
	}

	// Dispatch queued emails cleanly outside transaction (email errors never affect confirmation!).
	if !resOut.Duplicate && !resOut.Rejected {
		_, _ = s.DispatchPendingEmails(ctx)
	}
	return resOut, nil
}

func (s *Service) DispatchPendingEmails(ctx context.Context) (int, error) {
	nowUTC := s.nowUTC()
	if !s.HasEmailProvider() {
		_, _ = s.store.MarkOutboxEmailsProviderUnconfigured(ctx, nowUTC, 20, mailer.ErrProviderUnconfigured.Error())
		return 0, mailer.ErrProviderUnconfigured
	}
	cfg := s.effectiveDispatchConfig()
	leaseClock := s.leaseClockParam()

	// Fence ONLY genuinely expired worker leases (dispatch_lease_expires_at <= clock),
	// cancelling any matching active transport in this process. Live heartbeating workers
	// are left untouched regardless of total elapsed time.
	fencedIDs, _ := s.store.SweepStaleOutboxDispatches(ctx, leaseClock)
	for _, fid := range fencedIDs {
		s.cancelActiveDispatchIfPresent(fid)
	}

	items, err := s.store.ClaimDueOutboxEmails(ctx, s.nowUTC(), 20)
	if err != nil {
		return 0, err
	}
	sender := s.boundEmailSender(cfg.TransportTimeout)
	sentCount := 0
	for _, item := range items {
		if s.dispatchSingleOutboxItem(ctx, item, cfg, sender) {
			sentCount++
		}
	}
	return sentCount, nil
}

func (s *Service) boundEmailSender(maxTimeout time.Duration) mailer.Sender {
	if smtpSender, ok := s.emailSender.(*mailer.SMTPSender); ok && smtpSender != nil {
		bounded := *smtpSender
		if bounded.Timeout <= 0 || (maxTimeout > 0 && bounded.Timeout > maxTimeout) {
			bounded.Timeout = maxTimeout
		}
		return &bounded
	}
	return s.emailSender
}

func (s *Service) dispatchSingleOutboxItem(ctx context.Context, item domain.EmailOutboxItem, cfg DispatchConfig, sender mailer.Sender) bool {
	workerID := strings.TrimSpace(cfg.WorkerID)
	if workerID == "" {
		workerID = "outbox-worker"
	}
	if item.WorkerToken != nil {
		workerID = workerID + "-" + item.WorkerToken.String()[:8]
	}
	allowed, fenceEpoch, permErr := s.store.BeginOutboxDispatchLease(ctx, item.ID, item.WorkerToken, workerID, s.leaseClockParam(), cfg.LeaseTTL)
	if permErr != nil || !allowed {
		return false
	}
	if strings.TrimSpace(item.RecipientEmail) == "" || !strings.Contains(item.RecipientEmail, "@") || item.RecipientEmail == "[redacted]" {
		_ = s.store.MarkOutboxEmailPermanentFailureWithLeaseAt(ctx, item.ID, item.WorkerToken, fenceEpoch, "invalid recipient email address", s.leaseClockParam())
		return false
	}

	sendCtx, sendCancel := context.WithTimeout(ctx, cfg.TransportTimeout)
	defer sendCancel()

	var leaseTok uuid.UUID
	if item.WorkerToken != nil {
		leaseTok = *item.WorkerToken
	}
	s.registerActiveDispatch(item.ID, activeDispatchHandle{
		workerID: workerID,
		token:    leaseTok,
		cancel:   sendCancel,
	})
	// Remain locally active until BOTH transport termination AND durable PG state transition finish.
	defer s.unregisterActiveDispatch(item.ID, leaseTok)

	hbCtx, hbStop := context.WithCancel(sendCtx)
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func(outboxID uuid.UUID, tok *uuid.UUID, wID string, epoch int64, interval, leaseTTL time.Duration) {
		defer hbWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		renewTimeout := interval
		if half := leaseTTL / 2; half > 0 && half < renewTimeout {
			renewTimeout = half
		}
		if renewTimeout < 15*time.Millisecond {
			renewTimeout = 15 * time.Millisecond
		}
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(hbCtx, renewTimeout)
				renewed, rErr := s.store.RenewOutboxDispatchHeartbeat(renewCtx, outboxID, tok, wID, epoch, s.leaseClockParam(), leaseTTL)
				renewCancel()
				if rErr != nil || !renewed {
					sendCancel()
					return
				}
			}
		}
	}(item.ID, item.WorkerToken, workerID, fenceEpoch, cfg.HeartbeatInterval, cfg.LeaseTTL)

	sendErr := sender.Send(sendCtx, mailer.Message{
		From:     s.cfg.EmailFrom,
		To:       item.RecipientEmail,
		Subject:  item.Subject,
		BodyText: item.BodyText,
		BodyHTML: item.BodyHTML,
	})

	hbStop()
	hbWG.Wait()
	sendCancel()

	backoff := time.Duration(item.Attempts+1) * 5 * time.Minute
	leaseNow := s.leaseClockParam()
	if sendErr == nil {
		return s.store.MarkOutboxEmailSentWithLeaseAt(ctx, item.ID, item.WorkerToken, fenceEpoch, s.nowUTC(), leaseNow) == nil
	}
	if mailer.IsTerminatedBeforeSend(sendErr) {
		_ = s.store.MarkOutboxDispatchTerminatedBeforeSendWithLeaseAt(ctx, item.ID, item.WorkerToken, fenceEpoch, sendErr.Error(), s.nowUTC().Add(backoff), s.nowUTC(), leaseNow)
		return false
	}
	if mailer.IsUncertain(sendErr) || errors.Is(sendErr, context.DeadlineExceeded) || errors.Is(sendErr, context.Canceled) {
		_ = s.store.MarkOutboxDispatchUnknownWithLeaseAt(ctx, item.ID, item.WorkerToken, fenceEpoch, sendErr.Error(), s.nowUTC(), leaseNow)
		return false
	}
	if mailer.IsPermanent(sendErr) {
		_ = s.store.MarkOutboxEmailPermanentFailureWithLeaseAt(ctx, item.ID, item.WorkerToken, fenceEpoch, sendErr.Error(), leaseNow)
	} else {
		_ = s.store.MarkOutboxEmailRetryWithLeaseAt(ctx, item.ID, item.WorkerToken, fenceEpoch, sendErr.Error(), s.nowUTC().Add(backoff), leaseNow)
	}
	return false
}

func (s *Service) RotateConfirmationToken(ctx context.Context, meta Actor, ref, currentToken string) (string, error) {
	if strings.TrimSpace(currentToken) == "" {
		return "", domain.ErrNotFound
	}
	nowUTC := s.nowUTC()
	var rotated string
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		rotated, txErr = s.store.RotateConfirmationToken(ctx, tx, ref, currentToken, nowUTC)
		if txErr != nil {
			return txErr
		}
		metaActor := Actor{
			Email:     "guest@public.lovelace",
			Role:      "public_guest",
			RequestID: meta.RequestID,
			IPAddress: meta.IPAddress,
			UserAgent: meta.UserAgent,
		}
		return s.recordAudit(ctx, tx, metaActor, nil, "confirmation_token.rotated", "confirmation_access_token", strings.ToUpper(strings.TrimSpace(ref)), nil, map[string]any{"reference": ref})
	})
	return rotated, err
}

func (s *Service) GetPublicConfirmation(ctx context.Context, ref, token string) (domain.PublicConfirmationView, error) {
	if strings.TrimSpace(token) == "" {
		return domain.PublicConfirmationView{}, domain.ErrNotFound
	}
	view, _, _, _, err := s.store.GetPublicConfirmationByRefAndToken(ctx, nil, ref, token, s.nowUTC())
	return view, err
}

func (s *Service) RequestPublicCancellation(ctx context.Context, meta Actor, ref, token, note string) (domain.PublicConfirmationView, error) {
	if strings.TrimSpace(token) == "" {
		return domain.PublicConfirmationView{}, domain.ErrNotFound
	}
	nowUTC := s.nowUTC()
	var resID uuid.UUID
	var propID uuid.UUID
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		view, pID, rIDPtr, holdIDPtr, err := s.store.GetPublicConfirmationByRefAndToken(ctx, tx, ref, token, nowUTC)
		if err != nil {
			return err
		}
		if rIDPtr == nil {
			if holdIDPtr != nil && strings.HasPrefix(view.Status, "PENDING_PAYMENT_") {
				_, err = s.store.CancelBookingHold(ctx, tx, *holdIDPtr)
				return err
			}
			return domain.ErrNotFound
		}
		resID = *rIDPtr
		propID = pID
		updated, err := s.store.RecordCustomerCancellationRequest(ctx, tx, pID, resID, note)
		if err != nil {
			return err
		}
		metaActor := Actor{
			Email:     "guest@public.lovelace",
			Role:      "public_guest",
			RequestID: meta.RequestID,
			IPAddress: meta.IPAddress,
			UserAgent: meta.UserAgent,
		}
		return s.recordAudit(ctx, tx, metaActor, &propID, "reservation.cancellation_requested", "reservation", resID.String(), nil, auditReservationSnapshot(updated))
	})
	if err != nil {
		return domain.PublicConfirmationView{}, err
	}
	return s.GetPublicConfirmation(ctx, ref, token)
}

func (s *Service) SweepExpiredHolds(ctx context.Context) (int, error) {
	return s.store.ExpireStaleHolds(ctx, s.nowUTC())
}

// --- Admin Booking Policy & Financial Inspection ---

func (s *Service) GetBookingPolicy(ctx context.Context, actor Actor, propertyID uuid.UUID) (domain.PropertyBookingPolicy, error) {
	if err := s.requirePerm(actor, auth.PermPropertyRead); err != nil {
		return domain.PropertyBookingPolicy{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.PropertyBookingPolicy{}, err
	}
	return s.store.GetBookingPolicy(ctx, nil, propertyID)
}

func (s *Service) UpdateBookingPolicy(ctx context.Context, actor Actor, policy domain.PropertyBookingPolicy) (domain.PropertyBookingPolicy, error) {
	if err := s.requirePerm(actor, auth.PermPropertyWrite); err != nil {
		return domain.PropertyBookingPolicy{}, err
	}
	if err := s.requirePropertyScope(actor, policy.PropertyID); err != nil {
		return domain.PropertyBookingPolicy{}, err
	}
	if err := policy.Validate(); err != nil {
		return domain.PropertyBookingPolicy{}, err
	}
	var updated domain.PropertyBookingPolicy
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetBookingPolicy(ctx, tx, policy.PropertyID)
		if err != nil {
			return err
		}
		updated, err = s.store.UpdateBookingPolicy(ctx, tx, policy)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &policy.PropertyID, "booking_policy.updated", "property_booking_policy", policy.PropertyID.String(), before, updated)
	})
	return updated, err
}

func (s *Service) ListPaymentExceptions(ctx context.Context, actor Actor, propertyID uuid.UUID) ([]domain.PaymentException, error) {
	if err := s.requirePerm(actor, auth.PermReservationsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListPaymentExceptions(ctx, propertyID)
}

func (s *Service) ResolvePaymentException(ctx context.Context, actor Actor, propertyID, exceptionID uuid.UUID, notes string) (domain.PaymentException, error) {
	if err := s.requirePerm(actor, auth.PermReservationsWrite); err != nil {
		return domain.PaymentException{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.PaymentException{}, err
	}
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return domain.PaymentException{}, domain.NewValidationError(map[string]string{
			"resolution_notes": "resolution notes are required",
		})
	}
	var updated domain.PaymentException
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetPaymentException(ctx, tx, propertyID, exceptionID)
		if err != nil {
			return err
		}
		updated, err = s.store.ResolvePaymentException(ctx, tx, propertyID, exceptionID, actor.UserID, notes)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "payment_exception.resolved", "payment_exception", updated.ID.String(), before, updated)
	})
	return updated, err
}

func (s *Service) GetReservationFinancials(ctx context.Context, actor Actor, propertyID, reservationID uuid.UUID) (ReservationFinancials, error) {
	if err := s.requirePerm(actor, auth.PermReservationsRead); err != nil {
		return ReservationFinancials{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return ReservationFinancials{}, err
	}
	res, err := s.store.GetReservation(ctx, nil, propertyID, reservationID)
	if err != nil {
		return ReservationFinancials{}, err
	}
	quote, err := s.store.GetLatestQuoteForHoldOrReservation(ctx, nil, propertyID, nil, &res.ID)
	if err != nil {
		return ReservationFinancials{}, err
	}
	paymentsList, err := s.store.ListPaymentsForReservation(ctx, nil, propertyID, res.ID)
	if err != nil {
		return ReservationFinancials{}, err
	}
	return ReservationFinancials{
		Reservation: res,
		Quote:       quote,
		Payments:    paymentsList,
	}, nil
}

// CompleteSandboxPayment executes a non-production local Stripe Checkout completion
// by generating a genuine signed Stripe webhook event and feeding it through ProcessStripeWebhook.
func (s *Service) CompleteSandboxPayment(ctx context.Context, meta Actor, sessionID, outcome string) error {
	if !s.cfg.StripeSandbox || strings.EqualFold(s.cfg.AppEnv, "production") {
		return domain.ErrForbidden
	}
	att, err := s.store.GetPaymentAttemptByIDOrSession(ctx, nil, nil, strings.TrimSpace(sessionID))
	if err != nil {
		return err
	}
	eventType := "checkout.session.completed"
	payStatus := "paid"
	if strings.EqualFold(strings.TrimSpace(outcome), "failed") {
		eventType = "payment_intent.payment_failed"
		payStatus = "unpaid"
	}
	evt := map[string]any{
		"id":       "evt_sandbox_" + uuid.New().String(),
		"type":     eventType,
		"livemode": false,
		"data": map[string]any{
			"object": map[string]any{
				"id":             att.ProviderSessionID,
				"payment_intent": att.ProviderPaymentIntentID,
				"amount_total":   att.AmountPence,
				"currency":       strings.ToLower(att.Currency),
				"payment_status": payStatus,
				"last_payment_error": map[string]any{
					"message": "Simulated sandbox payment failure",
				},
				"metadata": map[string]any{
					"payment_attempt_id": att.ID.String(),
				},
			},
		},
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	sig := payments.ComputeStripeSignature(raw, s.cfg.StripeWebhookSecret, s.nowUTC())
	_, err = s.ProcessStripeWebhook(ctx, meta, raw, sig)
	return err
}
