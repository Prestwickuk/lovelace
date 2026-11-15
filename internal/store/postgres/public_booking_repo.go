package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

func generateHoldReference(now time.Time) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("HLD-%d-%s", now.UTC().Year(), strings.ToUpper(hex.EncodeToString(b)))
}

func GenerateConfirmationToken() (plain string, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	plain = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(plain))
	hash = hex.EncodeToString(sum[:])
	return plain, hash, nil
}

func HashConfirmationToken(plain string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(plain)))
	return hex.EncodeToString(sum[:])
}

func (s *Store) GetPublicPropertyBySlug(ctx context.Context, db DBTX, slug string) (domain.PublicPropertyView, error) {
	if db == nil {
		db = s.pool
	}
	slug = strings.TrimSpace(strings.ToLower(slug))
	var out domain.PublicPropertyView
	var p domain.PropertyBookingPolicy
	err := db.QueryRow(ctx, `
		SELECT prop.id, prop.slug, prop.name, prop.tagline, prop.public_description,
		       prop.country_code, prop.region, prop.address_line1, prop.address_line2,
		       prop.town, prop.postcode, prop.timezone, prop.currency,
		       to_char(prop.check_in_time, 'HH24:MI'), to_char(prop.check_out_time, 'HH24:MI'),
		       prop.vat_registered, prop.contact_email, prop.contact_phone,
		       prop.website_url, prop.custom_domain, prop.branding_config,
		       pol.property_id, pol.hold_duration_minutes, pol.deposit_mode,
		       pol.deposit_percentage, pol.deposit_fixed_pence, pol.balance_due_days_before_arrival,
		       pol.extra_adult_nightly_pence, pol.child_nightly_pence, pol.infant_nightly_pence,
		       pol.booking_terms_version, pol.booking_terms_url, pol.privacy_policy_url,
		       pol.cancellation_policy, pol.quiet_hours, pol.fire_policy, pol.pet_policy, pol.privacy_notice,
		       pol.stripe_enabled, pol.version, pol.updated_at
		FROM properties prop
		LEFT JOIN property_booking_policies pol ON pol.property_id = prop.id
		WHERE prop.slug = $1 AND prop.public_enabled = TRUE
	`, slug).Scan(
		&out.ID, &out.Slug, &out.Name, &out.Tagline, &out.PublicDescription,
		&out.CountryCode, &out.Region, &out.AddressLine1, &out.AddressLine2,
		&out.Town, &out.Postcode, &out.Timezone, &out.Currency,
		&out.CheckInTime, &out.CheckOutTime, &out.VATRegistered,
		&out.ContactEmail, &out.ContactPhone, &out.WebsiteURL, &out.CustomDomain, &out.Branding,
		&p.PropertyID, &p.HoldDurationMinutes, &p.DepositMode,
		&p.DepositPercentage, &p.DepositFixedPence, &p.BalanceDueDaysBeforeArrival,
		&p.ExtraAdultNightlyPence, &p.ChildNightlyPence, &p.InfantNightlyPence,
		&p.BookingTermsVersion, &p.BookingTermsURL, &p.PrivacyPolicyURL,
		&p.CancellationPolicy, &p.QuietHours, &p.FirePolicy, &p.PetPolicy, &p.PrivacyNotice,
		&p.StripeEnabled, &p.Version, &p.UpdatedAt,
	)
	if err != nil {
		return domain.PublicPropertyView{}, MapErr(err)
	}
	out.Policy = p
	return out, nil
}

func (s *Store) GetBookingPolicy(ctx context.Context, db DBTX, propertyID uuid.UUID) (domain.PropertyBookingPolicy, error) {
	if db == nil {
		db = s.pool
	}
	var p domain.PropertyBookingPolicy
	err := db.QueryRow(ctx, `
		SELECT property_id, hold_duration_minutes, deposit_mode,
		       deposit_percentage, deposit_fixed_pence, balance_due_days_before_arrival,
		       extra_adult_nightly_pence, child_nightly_pence, infant_nightly_pence,
		       booking_terms_version, booking_terms_url, privacy_policy_url,
		       cancellation_policy, quiet_hours, fire_policy, pet_policy, privacy_notice,
		       stripe_enabled, version, updated_at
		FROM property_booking_policies
		WHERE property_id = $1
	`, propertyID).Scan(
		&p.PropertyID, &p.HoldDurationMinutes, &p.DepositMode,
		&p.DepositPercentage, &p.DepositFixedPence, &p.BalanceDueDaysBeforeArrival,
		&p.ExtraAdultNightlyPence, &p.ChildNightlyPence, &p.InfantNightlyPence,
		&p.BookingTermsVersion, &p.BookingTermsURL, &p.PrivacyPolicyURL,
		&p.CancellationPolicy, &p.QuietHours, &p.FirePolicy, &p.PetPolicy, &p.PrivacyNotice,
		&p.StripeEnabled, &p.Version, &p.UpdatedAt,
	)
	return p, MapErr(err)
}

func (s *Store) UpdateBookingPolicy(ctx context.Context, tx pgx.Tx, p domain.PropertyBookingPolicy) (domain.PropertyBookingPolicy, error) {
	var out domain.PropertyBookingPolicy
	err := tx.QueryRow(ctx, `
		UPDATE property_booking_policies
		SET hold_duration_minutes = $2,
		    deposit_mode = $3,
		    deposit_percentage = $4,
		    deposit_fixed_pence = $5,
		    balance_due_days_before_arrival = $6,
		    extra_adult_nightly_pence = $7,
		    child_nightly_pence = $8,
		    infant_nightly_pence = $9,
		    booking_terms_version = $10,
		    booking_terms_url = $11,
		    privacy_policy_url = $12,
		    cancellation_policy = $13,
		    quiet_hours = $14,
		    fire_policy = $15,
		    pet_policy = $16,
		    privacy_notice = $17,
		    stripe_enabled = $18,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND version = $19
		RETURNING property_id, hold_duration_minutes, deposit_mode,
		          deposit_percentage, deposit_fixed_pence, balance_due_days_before_arrival,
		          extra_adult_nightly_pence, child_nightly_pence, infant_nightly_pence,
		          booking_terms_version, booking_terms_url, privacy_policy_url,
		          cancellation_policy, quiet_hours, fire_policy, pet_policy, privacy_notice,
		          stripe_enabled, version, updated_at
	`,
		p.PropertyID, p.HoldDurationMinutes, p.DepositMode,
		p.DepositPercentage, p.DepositFixedPence, p.BalanceDueDaysBeforeArrival,
		p.ExtraAdultNightlyPence, p.ChildNightlyPence, p.InfantNightlyPence,
		p.BookingTermsVersion, p.BookingTermsURL, p.PrivacyPolicyURL,
		p.CancellationPolicy, p.QuietHours, p.FirePolicy, p.PetPolicy, p.PrivacyNotice,
		p.StripeEnabled, p.Version,
	).Scan(
		&out.PropertyID, &out.HoldDurationMinutes, &out.DepositMode,
		&out.DepositPercentage, &out.DepositFixedPence, &out.BalanceDueDaysBeforeArrival,
		&out.ExtraAdultNightlyPence, &out.ChildNightlyPence, &out.InfantNightlyPence,
		&out.BookingTermsVersion, &out.BookingTermsURL, &out.PrivacyPolicyURL,
		&out.CancellationPolicy, &out.QuietHours, &out.FirePolicy, &out.PetPolicy, &out.PrivacyNotice,
		&out.StripeEnabled, &out.Version, &out.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return domain.PropertyBookingPolicy{}, domain.ErrConflict
	}
	return out, MapErr(err)
}

func (s *Store) ListPublicUnitTypes(ctx context.Context, db DBTX, propertyID uuid.UUID) ([]domain.PublicUnitTypeView, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT id, code, name, category, description,
		       base_capacity_adults, max_adults, max_children, max_infants, max_guests,
		       pets_allowed, max_pets, base_nightly_rate_pence, tax_rate_bps,
		       min_nights, max_nights, amenities, images
		FROM unit_types
		WHERE property_id = $1
		  AND archived_at IS NULL
		  AND is_active = TRUE
		  AND is_public_bookable = TRUE
		ORDER BY sort_order ASC, name ASC
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.PublicUnitTypeView
	for rows.Next() {
		v, err := scanPublicUnitType(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, v)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetPublicUnitTypeByCode(ctx context.Context, db DBTX, propertyID uuid.UUID, code string) (domain.PublicUnitTypeView, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT id, code, name, category, description,
		       base_capacity_adults, max_adults, max_children, max_infants, max_guests,
		       pets_allowed, max_pets, base_nightly_rate_pence, tax_rate_bps,
		       min_nights, max_nights, amenities, images
		FROM unit_types
		WHERE property_id = $1
		  AND LOWER(code) = LOWER($2)
		  AND archived_at IS NULL
		  AND is_active = TRUE
		  AND is_public_bookable = TRUE
	`, propertyID, strings.TrimSpace(code))
	v, err := scanPublicUnitType(row)
	return v, MapErr(err)
}

func scanPublicUnitType(row scannable) (domain.PublicUnitTypeView, error) {
	var v domain.PublicUnitTypeView
	var cat string
	var amenitiesRaw, imagesRaw []byte
	if err := row.Scan(
		&v.ID, &v.Code, &v.Name, &cat, &v.Description,
		&v.BaseCapacityAdults, &v.MaxAdults, &v.MaxChildren, &v.MaxInfants, &v.MaxGuests,
		&v.PetsAllowed, &v.MaxPets, &v.BaseNightlyRatePence, &v.TaxRateBPS,
		&v.MinNights, &v.MaxNights, &amenitiesRaw, &imagesRaw,
	); err != nil {
		return domain.PublicUnitTypeView{}, err
	}
	v.Category = domain.UnitCategory(cat)
	if len(amenitiesRaw) > 0 {
		_ = json.Unmarshal(amenitiesRaw, &v.Amenities)
	}
	if len(imagesRaw) > 0 {
		_ = json.Unmarshal(imagesRaw, &v.Images)
	}
	if v.Amenities == nil {
		v.Amenities = []string{}
	}
	if v.Images == nil {
		v.Images = []domain.UnitTypeImage{}
	}
	return v, nil
}

func (s *Store) ListPublicExtras(ctx context.Context, db DBTX, propertyID uuid.UUID) ([]domain.PublicExtraView, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT id, code, name, description, pricing_model, price_pence, tax_rate_bps, max_quantity
		FROM extras
		WHERE property_id = $1 AND archived_at IS NULL AND is_active = TRUE
		ORDER BY sort_order ASC, name ASC
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.PublicExtraView
	for rows.Next() {
		var e domain.PublicExtraView
		var pm string
		if err := rows.Scan(&e.ID, &e.Code, &e.Name, &e.Description, &pm, &e.PricePence, &e.TaxRateBPS, &e.MaxQuantity); err != nil {
			return nil, MapErr(err)
		}
		e.PricingModel = domain.PricingModel(pm)
		out = append(out, e)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetDefaultRatePlan(ctx context.Context, db DBTX, propertyID uuid.UUID) (domain.RatePlan, error) {
	if db == nil {
		db = s.pool
	}
	var p domain.RatePlan
	err := db.QueryRow(ctx, `
		SELECT id, property_id, code, name, description, is_default, is_active, priority, version, archived_at, created_at, updated_at
		FROM rate_plans
		WHERE property_id = $1 AND archived_at IS NULL AND is_active = TRUE
		ORDER BY is_default DESC, priority DESC, created_at ASC
		LIMIT 1
	`, propertyID).Scan(
		&p.ID, &p.PropertyID, &p.Code, &p.Name, &p.Description, &p.IsDefault,
		&p.IsActive, &p.Priority, &p.Version, &p.ArchivedAt, &p.CreatedAt, &p.UpdatedAt,
	)
	return p, MapErr(err)
}

func (s *Store) ListActiveUnitsForPublicBooking(ctx context.Context, db DBTX, propertyID uuid.UUID, unitTypeID *uuid.UUID) ([]domain.Unit, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT u.id, u.property_id, u.unit_type_id, ut.name, u.code, u.name, u.zone,
		       u.status, u.access_notes, u.operational_notes, u.attributes, u.sort_order,
		       u.version, u.archived_at, u.created_at, u.updated_at
		FROM units u
		JOIN unit_types ut ON ut.id = u.unit_type_id
		WHERE u.property_id = $1
		  AND u.archived_at IS NULL
		  AND u.status = 'active'
		  AND ut.archived_at IS NULL
		  AND ut.is_active = TRUE
		  AND ut.is_public_bookable = TRUE
		  AND ($2::uuid IS NULL OR u.unit_type_id = $2)
		ORDER BY u.sort_order ASC, u.code ASC
	`, propertyID, unitTypeID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.Unit
	for rows.Next() {
		var u domain.Unit
		var st string
		if err := rows.Scan(
			&u.ID, &u.PropertyID, &u.UnitTypeID, &u.UnitTypeName, &u.Code, &u.Name, &u.Zone,
			&st, &u.AccessNotes, &u.OperationalNotes, &u.Attributes, &u.SortOrder,
			&u.Version, &u.ArchivedAt, &u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		u.Status = domain.UnitStatus(st)
		out = append(out, u)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) InsertBookingQuote(ctx context.Context, tx pgx.Tx, q domain.BookingQuote) (domain.BookingQuote, error) {
	nb, err := json.Marshal(q.NightlyBreakdown)
	if err != nil {
		return domain.BookingQuote{}, err
	}
	eb, err := json.Marshal(q.ExtrasBreakdown)
	if err != nil {
		return domain.BookingQuote{}, err
	}
	tb, err := json.Marshal(q.TaxBreakdown)
	if err != nil {
		return domain.BookingQuote{}, err
	}
	var balanceDueDate any
	if q.BalanceDueDate != "" {
		balanceDueDate = q.BalanceDueDate
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO booking_quotes (
			id, property_id, hold_id, reservation_id, unit_type_id, rate_plan_id,
			currency, start_date, end_date, nights, adults, children, infants,
			nightly_breakdown, extras_breakdown, tax_breakdown,
			accommodation_subtotal_pence, occupancy_surcharge_pence, extras_subtotal_pence,
			discount_pence, tax_pence, total_pence, deposit_mode,
			deposit_due_pence, balance_due_pence, balance_due_date,
			policy_version, quote_hash, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8::date, $9::date, $10, $11, $12, $13,
			$14, $15, $16,
			$17, $18, $19,
			$20, $21, $22, $23,
			$24, $25, $26::date,
			$27, $28, $29
		)
		RETURNING created_at
	`,
		q.ID, q.PropertyID, q.HoldID, q.ReservationID, q.UnitTypeID, q.RatePlanID,
		q.Currency, q.StartDate, q.EndDate, q.Nights, q.Adults, q.Children, q.Infants,
		nb, eb, tb,
		q.AccommodationSubtotalPence, q.OccupancySurchargePence, q.ExtrasSubtotalPence,
		q.DiscountPence, q.TaxPence, q.TotalPence, q.DepositMode,
		q.DepositDuePence, q.BalanceDuePence, balanceDueDate,
		q.PolicyVersion, q.QuoteHash, q.ExpiresAt,
	).Scan(&q.CreatedAt)
	return q, MapErr(err)
}

func (s *Store) GetLatestQuoteForHoldOrReservation(ctx context.Context, db DBTX, propertyID uuid.UUID, holdID, reservationID *uuid.UUID) (*domain.BookingQuote, error) {
	if db == nil {
		db = s.pool
	}
	var q domain.BookingQuote
	var startDt, endDt time.Time
	var balDt *time.Time
	var nbRaw, ebRaw, tbRaw []byte
	err := db.QueryRow(ctx, `
		SELECT q.id, q.property_id, q.hold_id, q.reservation_id, q.unit_type_id, ut.code, ut.name,
		       q.rate_plan_id, rp.name, q.currency, q.start_date, q.end_date, q.nights,
		       q.adults, q.children, q.infants, q.nightly_breakdown, q.extras_breakdown, q.tax_breakdown,
		       q.accommodation_subtotal_pence, q.occupancy_surcharge_pence, q.extras_subtotal_pence,
		       q.discount_pence, q.tax_pence, q.total_pence, q.deposit_mode,
		       q.deposit_due_pence, q.balance_due_pence, q.balance_due_date,
		       q.policy_version, q.quote_hash, q.expires_at, q.created_at
		FROM booking_quotes q
		JOIN unit_types ut ON ut.id = q.unit_type_id
		JOIN rate_plans rp ON rp.id = q.rate_plan_id
		WHERE q.property_id = $1
		  AND (($2::uuid IS NOT NULL AND q.hold_id = $2) OR ($3::uuid IS NOT NULL AND q.reservation_id = $3))
		ORDER BY q.created_at DESC
		LIMIT 1
	`, propertyID, holdID, reservationID).Scan(
		&q.ID, &q.PropertyID, &q.HoldID, &q.ReservationID, &q.UnitTypeID, &q.UnitTypeCode, &q.UnitTypeName,
		&q.RatePlanID, &q.RatePlanName, &q.Currency, &startDt, &endDt, &q.Nights,
		&q.Adults, &q.Children, &q.Infants, &nbRaw, &ebRaw, &tbRaw,
		&q.AccommodationSubtotalPence, &q.OccupancySurchargePence, &q.ExtrasSubtotalPence,
		&q.DiscountPence, &q.TaxPence, &q.TotalPence, &q.DepositMode,
		&q.DepositDuePence, &q.BalanceDuePence, &balDt,
		&q.PolicyVersion, &q.QuoteHash, &q.ExpiresAt, &q.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, MapErr(err)
	}
	q.StartDate = domain.FormatDate(startDt)
	q.EndDate = domain.FormatDate(endDt)
	if balDt != nil {
		q.BalanceDueDate = domain.FormatDate(*balDt)
	}
	_ = json.Unmarshal(nbRaw, &q.NightlyBreakdown)
	_ = json.Unmarshal(ebRaw, &q.ExtrasBreakdown)
	_ = json.Unmarshal(tbRaw, &q.TaxBreakdown)
	return &q, nil
}

// CreateHoldWithClaim atomically locks and claims the first genuinely available physical unit of utID
// inside PostgreSQL, relying on GiST excl_inventory_claims_no_overlap to serialize concurrent holds.
func (s *Store) CreateHoldWithClaim(
	ctx context.Context,
	tx pgx.Tx,
	hold domain.BookingHold,
	quote domain.BookingQuote,
	nowUTC time.Time,
) (domain.BookingHold, domain.BookingQuote, error) {
	// Expire any stale holds covering overlapping dates first so abandoned holds do not block fresh guests.
	if _, err := s.expireHoldsInTx(ctx, tx, nowUTC); err != nil {
		return domain.BookingHold{}, domain.BookingQuote{}, err
	}

	units, err := s.ListActiveUnitsForPublicBooking(ctx, tx, hold.PropertyID, &hold.UnitTypeID)
	if err != nil {
		return domain.BookingHold{}, domain.BookingQuote{}, err
	}
	if len(units) == 0 {
		return domain.BookingHold{}, domain.BookingQuote{}, domain.ErrOverlap
	}

	rules, err := s.ListActiveReleaseRulesInRange(ctx, tx, hold.PropertyID, hold.ArrivalDate, hold.DepartureDate)
	if err != nil {
		return domain.BookingHold{}, domain.BookingQuote{}, err
	}

	var chosenUnit *domain.Unit
	for i := range units {
		u := units[i]
		// Release policy gate for DIRECT
		if allowed, _, _ := domain.EvaluateReleaseForStay(rules, u, "DIRECT", hold.ArrivalDate, hold.DepartureDate, nowUTC); !allowed {
			continue
		}
		// Claim conflict check
		conflicts, err := s.FindClaimConflictsForChannel(
			ctx, tx, hold.PropertyID, []uuid.UUID{u.ID},
			hold.ArrivalDate, hold.DepartureDate, domain.ClaimHold, "DIRECT", nil, nil,
		)
		if err != nil {
			return domain.BookingHold{}, domain.BookingQuote{}, err
		}
		if len(conflicts) > 0 {
			continue
		}
		chosenUnit = &u
		break
	}
	if chosenUnit == nil {
		return domain.BookingHold{}, domain.BookingQuote{}, fmt.Errorf("%w: no units of this type are available for the selected dates", domain.ErrOverlap)
	}

	if hold.HoldReference == "" {
		hold.HoldReference = generateHoldReference(nowUTC)
	}
	hold.UnitID = chosenUnit.ID
	hold.Status = domain.HoldStatusActive

	err = tx.QueryRow(ctx, `
		INSERT INTO booking_holds (
			id, property_id, guest_id, hold_reference, unit_type_id, unit_id, rate_plan_id, channel_code,
			stay_range, adults, children, infants, guest_name, guest_email, guest_phone,
			special_requests, terms_version, terms_accepted_at, customer_session_hash,
			status, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, 'DIRECT',
			daterange($8::date, $9::date, '[)'), $10, $11, $12, $13, $14, $15,
			$16, $17, $18, $19,
			'ACTIVE', $20
		)
		RETURNING version, created_at, updated_at
	`,
		hold.ID, hold.PropertyID, hold.GuestID, hold.HoldReference, hold.UnitTypeID, hold.UnitID, hold.RatePlanID,
		domain.FormatDate(hold.ArrivalDate), domain.FormatDate(hold.DepartureDate),
		hold.Adults, hold.Children, hold.Infants, hold.GuestName, strings.ToLower(strings.TrimSpace(hold.GuestEmail)),
		hold.GuestPhone, hold.SpecialRequests, hold.TermsVersion, hold.TermsAcceptedAt,
		hold.CustomerSessionHash, hold.ExpiresAt,
	).Scan(&hold.Version, &hold.CreatedAt, &hold.UpdatedAt)
	if err != nil {
		return domain.BookingHold{}, domain.BookingQuote{}, MapErr(err)
	}

	claimID, err := NewUUID()
	if err != nil {
		return domain.BookingHold{}, domain.BookingQuote{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO inventory_claims (
			id, property_id, unit_id, claim_type, hold_id, channel_scope, claim_range, status, reason
		) VALUES (
			$1, $2, $3, 'HOLD', $4, 'DIRECT', daterange($5::date, $6::date, '[)'), 'ACTIVE', $7
		)
	`,
		claimID, hold.PropertyID, hold.UnitID, hold.ID,
		domain.FormatDate(hold.ArrivalDate), domain.FormatDate(hold.DepartureDate),
		"Temporary customer checkout hold "+hold.HoldReference,
	); err != nil {
		return domain.BookingHold{}, domain.BookingQuote{}, MapErr(err)
	}

	quote.HoldID = &hold.ID
	savedQuote, err := s.InsertBookingQuote(ctx, tx, quote)
	if err != nil {
		return domain.BookingHold{}, domain.BookingQuote{}, err
	}
	hold.Arrival = domain.FormatDate(hold.ArrivalDate)
	hold.Departure = domain.FormatDate(hold.DepartureDate)
	hold.UnitTypeName = savedQuote.UnitTypeName
	return hold, savedQuote, nil
}

func (s *Store) expireHoldsInTx(ctx context.Context, tx pgx.Tx, nowUTC time.Time) (int, error) {
	rows, err := tx.Query(ctx, `
		UPDATE booking_holds
		SET status = 'EXPIRED', version = version + 1, updated_at = NOW()
		WHERE status = 'ACTIVE' AND expires_at <= $1
		RETURNING id
	`, nowUTC)
	if err != nil {
		return 0, MapErr(err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, MapErr(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, MapErr(err)
	}
	rows.Close()

	if len(ids) == 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_claims
		SET status = 'RELEASED', released_at = NOW(), updated_at = NOW()
		WHERE hold_id = ANY($1::uuid[]) AND status = 'ACTIVE'
	`, ids); err != nil {
		return 0, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_attempts
		SET status = 'EXPIRED',
		    failure_reason = CASE WHEN failure_reason = '' THEN 'booking hold expired before payment completion' ELSE failure_reason END,
		    updated_at = NOW()
		WHERE hold_id = ANY($1::uuid[]) AND status IN ('CREATED', 'REQUIRES_ACTION')
	`, ids); err != nil {
		return 0, MapErr(err)
	}
	return len(ids), nil
}

func (s *Store) ExpireStaleHolds(ctx context.Context, nowUTC time.Time) (int, error) {
	var n int
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		n, txErr = s.expireHoldsInTx(ctx, tx, nowUTC)
		return txErr
	})
	return n, err
}

func (s *Store) GetBookingHold(ctx context.Context, db DBTX, holdID uuid.UUID) (domain.BookingHold, error) {
	if db == nil {
		db = s.pool
	}
	var h domain.BookingHold
	err := db.QueryRow(ctx, `
		SELECT h.id, h.property_id, h.guest_id, h.hold_reference, h.unit_type_id, h.unit_id, ut.name,
		       h.rate_plan_id, h.channel_code, lower(h.stay_range), upper(h.stay_range),
		       h.adults, h.children, h.infants, h.guest_name, h.guest_email, h.guest_phone,
		       h.special_requests, h.terms_version, h.terms_accepted_at, h.customer_session_hash,
		       h.status, h.expires_at, h.confirmed_reservation_id, h.version, h.created_at, h.updated_at
		FROM booking_holds h
		JOIN unit_types ut ON ut.id = h.unit_type_id
		WHERE h.id = $1
	`, holdID).Scan(
		&h.ID, &h.PropertyID, &h.GuestID, &h.HoldReference, &h.UnitTypeID, &h.UnitID, &h.UnitTypeName,
		&h.RatePlanID, &h.ChannelCode, &h.ArrivalDate, &h.DepartureDate,
		&h.Adults, &h.Children, &h.Infants, &h.GuestName, &h.GuestEmail, &h.GuestPhone,
		&h.SpecialRequests, &h.TermsVersion, &h.TermsAcceptedAt, &h.CustomerSessionHash,
		&h.Status, &h.ExpiresAt, &h.ConfirmedReservationID, &h.Version, &h.CreatedAt, &h.UpdatedAt,
	)
	if err != nil {
		return domain.BookingHold{}, MapErr(err)
	}
	h.Arrival = domain.FormatDate(h.ArrivalDate)
	h.Departure = domain.FormatDate(h.DepartureDate)
	return h, nil
}

func (s *Store) CancelBookingHold(ctx context.Context, tx pgx.Tx, holdID uuid.UUID) (domain.BookingHold, error) {
	if _, err := tx.Exec(ctx, `
		UPDATE booking_holds
		SET status = 'CANCELLED', version = version + 1, updated_at = NOW()
		WHERE id = $1 AND status = 'ACTIVE'
	`, holdID); err != nil {
		return domain.BookingHold{}, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_claims
		SET status = 'RELEASED', released_at = NOW(), updated_at = NOW()
		WHERE hold_id = $1 AND status = 'ACTIVE'
	`, holdID); err != nil {
		return domain.BookingHold{}, MapErr(err)
	}
	return s.GetBookingHold(ctx, tx, holdID)
}

func (s *Store) CreateConfirmationToken(ctx context.Context, tx pgx.Tx, propertyID uuid.UUID, holdID, reservationID *uuid.UUID, expiresAt time.Time) (string, error) {
	plain, hash, err := GenerateConfirmationToken()
	if err != nil {
		return "", err
	}
	id, err := NewUUID()
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO confirmation_access_tokens (id, property_id, reservation_id, hold_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, id, propertyID, reservationID, holdID, hash, expiresAt)
	if err != nil {
		return "", MapErr(err)
	}
	return plain, nil
}

func (s *Store) CreateOrGetPaymentAttempt(ctx context.Context, tx pgx.Tx, att domain.PaymentAttempt) (domain.PaymentAttempt, bool, error) {
	const maxFailedPaymentAttemptsPerHold = 3

	// Serialize against the hold row when reserving a hold-scoped attempt so two
	// concurrent requests with different idempotency keys cannot both pass.
	if att.HoldID != nil {
		var holdStatus string
		if err := tx.QueryRow(ctx, `
			SELECT status
			FROM booking_holds
			WHERE id = $1
			FOR UPDATE
		`, *att.HoldID).Scan(&holdStatus); err != nil {
			return domain.PaymentAttempt{}, false, MapErr(err)
		}
		rows, err := tx.Query(ctx, `
			SELECT id, property_id, hold_id, reservation_id, provider, mode,
			       provider_session_id, provider_payment_intent_id, payment_type,
			       amount_pence, currency, status, idempotency_key, checkout_url, failure_reason,
			       created_at, updated_at
			FROM payment_attempts
			WHERE hold_id = $1
			ORDER BY created_at DESC
			FOR UPDATE
		`, *att.HoldID)
		if err != nil {
			return domain.PaymentAttempt{}, false, MapErr(err)
		}
		var failedCount int
		var activeAttempt *domain.PaymentAttempt
		for rows.Next() {
			var existing domain.PaymentAttempt
			if scanErr := rows.Scan(
				&existing.ID, &existing.PropertyID, &existing.HoldID, &existing.ReservationID,
				&existing.Provider, &existing.Mode, &existing.ProviderSessionID, &existing.ProviderPaymentIntentID,
				&existing.PaymentType, &existing.AmountPence, &existing.Currency, &existing.Status,
				&existing.IdempotencyKey, &existing.CheckoutURL, &existing.FailureReason,
				&existing.CreatedAt, &existing.UpdatedAt,
			); scanErr != nil {
				rows.Close()
				return domain.PaymentAttempt{}, false, MapErr(scanErr)
			}
			if existing.IdempotencyKey == att.IdempotencyKey && existing.PropertyID == att.PropertyID {
				rows.Close()
				return existing, true, nil
			}
			switch existing.Status {
			case "CREATED", "REQUIRES_ACTION", "SUCCEEDED", "RECONCILIATION_REQUIRED":
				copyAtt := existing
				activeAttempt = &copyAtt
			case "FAILED", "EXPIRED":
				failedCount++
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return domain.PaymentAttempt{}, false, MapErr(err)
		}
		rows.Close()

		if activeAttempt != nil {
			return *activeAttempt, false, domain.ErrActiveCheckoutExists
		}
		if failedCount >= maxFailedPaymentAttemptsPerHold {
			return domain.PaymentAttempt{}, false, domain.NewValidationError(map[string]string{
				"hold_id": "maximum payment attempts reached for this hold; please start a new booking hold",
			})
		}
	}

	var existing domain.PaymentAttempt
	err := tx.QueryRow(ctx, `
		SELECT id, property_id, hold_id, reservation_id, provider, mode,
		       provider_session_id, provider_payment_intent_id, payment_type,
		       amount_pence, currency, status, idempotency_key, checkout_url, failure_reason,
		       created_at, updated_at
		FROM payment_attempts
		WHERE property_id = $1 AND idempotency_key = $2
	`, att.PropertyID, att.IdempotencyKey).Scan(
		&existing.ID, &existing.PropertyID, &existing.HoldID, &existing.ReservationID,
		&existing.Provider, &existing.Mode, &existing.ProviderSessionID, &existing.ProviderPaymentIntentID,
		&existing.PaymentType, &existing.AmountPence, &existing.Currency, &existing.Status,
		&existing.IdempotencyKey, &existing.CheckoutURL, &existing.FailureReason,
		&existing.CreatedAt, &existing.UpdatedAt,
	)
	if err == nil {
		return existing, true, nil
	}
	if err != pgx.ErrNoRows {
		return domain.PaymentAttempt{}, false, MapErr(err)
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO payment_attempts (
			id, property_id, hold_id, reservation_id, provider, mode,
			provider_session_id, provider_payment_intent_id, payment_type,
			amount_pence, currency, status, idempotency_key, checkout_url
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING created_at, updated_at
	`,
		att.ID, att.PropertyID, att.HoldID, att.ReservationID, att.Provider, att.Mode,
		att.ProviderSessionID, att.ProviderPaymentIntentID, att.PaymentType,
		att.AmountPence, att.Currency, att.Status, att.IdempotencyKey, att.CheckoutURL,
	).Scan(&att.CreatedAt, &att.UpdatedAt)
	if err != nil {
		mapped := MapErr(err)
		if errors.Is(mapped, domain.ErrConflict) && att.HoldID != nil {
			return domain.PaymentAttempt{}, false, domain.ErrActiveCheckoutExists
		}
		return domain.PaymentAttempt{}, false, mapped
	}
	return att, false, nil
}

func (s *Store) MarkPaymentAttemptFailed(ctx context.Context, db DBTX, id uuid.UUID, reason string) error {
	if db == nil {
		db = s.pool
	}
	_, err := db.Exec(ctx, `
		UPDATE payment_attempts
		SET status = 'FAILED',
		    failure_reason = $2,
		    updated_at = NOW()
		WHERE id = $1 AND status IN ('CREATED', 'REQUIRES_ACTION')
	`, id, strings.TrimSpace(reason))
	return MapErr(err)
}

func (s *Store) MarkPaymentAttemptExpired(ctx context.Context, db DBTX, id uuid.UUID, reason string) error {
	if db == nil {
		db = s.pool
	}
	_, err := db.Exec(ctx, `
		UPDATE payment_attempts
		SET status = 'EXPIRED',
		    failure_reason = COALESCE(NULLIF($2, ''), failure_reason),
		    updated_at = NOW()
		WHERE id = $1 AND status IN ('CREATED', 'REQUIRES_ACTION')
	`, id, strings.TrimSpace(reason))
	return MapErr(err)
}

func (s *Store) ListPaymentAttemptsForHold(ctx context.Context, db DBTX, holdID uuid.UUID) ([]domain.PaymentAttempt, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, hold_id, reservation_id, provider, mode,
		       provider_session_id, provider_payment_intent_id, payment_type,
		       amount_pence, currency, status, idempotency_key, checkout_url, failure_reason,
		       created_at, updated_at
		FROM payment_attempts
		WHERE hold_id = $1
		ORDER BY created_at DESC
	`, holdID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.PaymentAttempt
	for rows.Next() {
		var att domain.PaymentAttempt
		if err := rows.Scan(
			&att.ID, &att.PropertyID, &att.HoldID, &att.ReservationID,
			&att.Provider, &att.Mode, &att.ProviderSessionID, &att.ProviderPaymentIntentID,
			&att.PaymentType, &att.AmountPence, &att.Currency, &att.Status,
			&att.IdempotencyKey, &att.CheckoutURL, &att.FailureReason,
			&att.CreatedAt, &att.UpdatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, att)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) UpdatePaymentAttemptSession(ctx context.Context, db DBTX, id uuid.UUID, sessionID, paymentIntentID, checkoutURL, status string) error {
	if db == nil {
		db = s.pool
	}
	_, err := db.Exec(ctx, `
		UPDATE payment_attempts
		SET provider_session_id = COALESCE(NULLIF($2, ''), provider_session_id),
		    provider_payment_intent_id = COALESCE(NULLIF($3, ''), provider_payment_intent_id),
		    checkout_url = COALESCE(NULLIF($4, ''), checkout_url),
		    status = $5,
		    updated_at = NOW()
		WHERE id = $1
	`, id, sessionID, paymentIntentID, checkoutURL, status)
	return MapErr(err)
}

func (s *Store) GetPaymentAttemptByIDOrSession(ctx context.Context, db DBTX, attemptID *uuid.UUID, sessionID string) (domain.PaymentAttempt, error) {
	if db == nil {
		db = s.pool
	}
	var att domain.PaymentAttempt
	err := db.QueryRow(ctx, `
		SELECT id, property_id, hold_id, reservation_id, provider, mode,
		       provider_session_id, provider_payment_intent_id, payment_type,
		       amount_pence, currency, status, idempotency_key, checkout_url, failure_reason,
		       created_at, updated_at
		FROM payment_attempts
		WHERE ($1::uuid IS NOT NULL AND id = $1) OR ($2::text <> '' AND provider_session_id = $2)
		ORDER BY created_at DESC
		LIMIT 1
	`, attemptID, sessionID).Scan(
		&att.ID, &att.PropertyID, &att.HoldID, &att.ReservationID,
		&att.Provider, &att.Mode, &att.ProviderSessionID, &att.ProviderPaymentIntentID,
		&att.PaymentType, &att.AmountPence, &att.Currency, &att.Status,
		&att.IdempotencyKey, &att.CheckoutURL, &att.FailureReason,
		&att.CreatedAt, &att.UpdatedAt,
	)
	return att, MapErr(err)
}

func (s *Store) BeginWebhookEvent(ctx context.Context, tx pgx.Tx, provider, eventID, eventType string, livemode *bool, payloadSHA256 string) (alreadySeen bool, err error) {
	id, err := NewUUID()
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO payment_webhook_events (id, provider, event_id, event_type, livemode, payload_sha256, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'PROCESSING')
		ON CONFLICT (provider, event_id) DO NOTHING
	`, id, provider, eventID, eventType, livemode, payloadSHA256)
	if err != nil {
		return false, MapErr(err)
	}
	return tag.RowsAffected() == 0, nil
}

func (s *Store) FinishWebhookEvent(ctx context.Context, db DBTX, provider, eventID, status, errMsg string) error {
	if db == nil {
		db = s.pool
	}
	_, err := db.Exec(ctx, `
		UPDATE payment_webhook_events
		SET status = $3, error_message = $4, processed_at = NOW()
		WHERE provider = $1 AND event_id = $2
	`, provider, eventID, status, errMsg)
	return MapErr(err)
}

// FinalizeHoldConfirmationResult summarizes the atomic confirmation or exception outcome of a payment webhook.
type FinalizeHoldConfirmationResult struct {
	Reservation       *domain.Reservation
	ExceptionRecorded *domain.PaymentException
	AlreadyConfirmed  bool
}

// ConfirmPaymentAndHold atomically converts an active unexpired hold + its inventory claim into a
// CONFIRMED DIRECT reservation with price snapshot & payment ledger row, OR records a payment_exception
// if the hold expired/disappeared so inventory is never silently overbooked.
func (s *Store) ConfirmPaymentAndHold(
	ctx context.Context,
	tx pgx.Tx,
	attemptID uuid.UUID,
	paymentIntentID string,
	chargeID string,
	paidAmountPence int64,
	paidCurrency string,
	nowUTC time.Time,
) (FinalizeHoldConfirmationResult, error) {
	var att domain.PaymentAttempt
	err := tx.QueryRow(ctx, `
		SELECT id, property_id, hold_id, reservation_id, provider, mode,
		       provider_session_id, provider_payment_intent_id, payment_type,
		       amount_pence, currency, status, idempotency_key, checkout_url, failure_reason,
		       created_at, updated_at
		FROM payment_attempts
		WHERE id = $1
		FOR UPDATE
	`, attemptID).Scan(
		&att.ID, &att.PropertyID, &att.HoldID, &att.ReservationID,
		&att.Provider, &att.Mode, &att.ProviderSessionID, &att.ProviderPaymentIntentID,
		&att.PaymentType, &att.AmountPence, &att.Currency, &att.Status,
		&att.IdempotencyKey, &att.CheckoutURL, &att.FailureReason,
		&att.CreatedAt, &att.UpdatedAt,
	)
	if err != nil {
		return FinalizeHoldConfirmationResult{}, MapErr(err)
	}

	if att.Status == domain.AttemptStatusSucceeded && att.ReservationID != nil {
		res, err := s.GetReservation(ctx, tx, att.PropertyID, *att.ReservationID)
		if err != nil {
			return FinalizeHoldConfirmationResult{}, err
		}
		return FinalizeHoldConfirmationResult{Reservation: &res, AlreadyConfirmed: true}, nil
	}

	if att.HoldID == nil {
		return FinalizeHoldConfirmationResult{}, domain.ErrNotFound
	}
	if paymentIntentID == "" {
		paymentIntentID = att.ProviderPaymentIntentID
	}

	// Lock hold row
	hold, err := s.GetBookingHoldForUpdate(ctx, tx, *att.HoldID)
	if err != nil {
		return FinalizeHoldConfirmationResult{}, err
	}

	// Amount/currency validation
	if paidAmountPence != att.AmountPence || !strings.EqualFold(paidCurrency, att.Currency) {
		ex, err := s.insertPaymentException(ctx, tx, domain.PaymentException{
			PropertyID:       att.PropertyID,
			HoldID:           att.HoldID,
			PaymentAttemptID: att.ID,
			ExceptionType:    "AMOUNT_MISMATCH",
			AmountPence:      paidAmountPence,
			Currency:         strings.ToUpper(paidCurrency),
			Details:          fmt.Sprintf("Paid %d %s differed from expected %d %s for hold %s", paidAmountPence, paidCurrency, att.AmountPence, att.Currency, hold.HoldReference),
		})
		if err != nil {
			return FinalizeHoldConfirmationResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_attempts SET status = 'RECONCILIATION_REQUIRED', provider_payment_intent_id = COALESCE(NULLIF($2,''), provider_payment_intent_id), updated_at = NOW() WHERE id = $1`, att.ID, paymentIntentID); err != nil {
			return FinalizeHoldConfirmationResult{}, MapErr(err)
		}
		return FinalizeHoldConfirmationResult{ExceptionRecorded: &ex}, nil
	}

	// Expired, cancelled, or no-longer-active hold -> NEVER overbook! Record exception cleanly.
	if hold.Status != domain.HoldStatusActive || !hold.ExpiresAt.After(nowUTC) {
		if hold.Status == domain.HoldStatusActive {
			_, _ = tx.Exec(ctx, `UPDATE booking_holds SET status = 'EXPIRED', version = version + 1, updated_at = NOW() WHERE id = $1`, hold.ID)
			_, _ = tx.Exec(ctx, `UPDATE inventory_claims SET status = 'RELEASED', released_at = NOW(), updated_at = NOW() WHERE hold_id = $1 AND status = 'ACTIVE'`, hold.ID)
		}
		ex, err := s.insertPaymentException(ctx, tx, domain.PaymentException{
			PropertyID:       att.PropertyID,
			HoldID:           &hold.ID,
			PaymentAttemptID: att.ID,
			ExceptionType:    "EXPIRED_HOLD_PAID",
			AmountPence:      paidAmountPence,
			Currency:         att.Currency,
			Details:          fmt.Sprintf("Stripe payment succeeded after hold %s expired or closed (status=%s); manual or automatic refund required.", hold.HoldReference, hold.Status),
		})
		if err != nil {
			return FinalizeHoldConfirmationResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_attempts SET status = 'RECONCILIATION_REQUIRED', provider_payment_intent_id = COALESCE(NULLIF($2,''), provider_payment_intent_id), updated_at = NOW() WHERE id = $1`, att.ID, paymentIntentID); err != nil {
			return FinalizeHoldConfirmationResult{}, MapErr(err)
		}
		return FinalizeHoldConfirmationResult{ExceptionRecorded: &ex}, nil
	}

	// Verify the hold's active inventory claim is still intact before converting it to the reservation claim
	var holdClaimID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM inventory_claims
		WHERE hold_id = $1 AND status = 'ACTIVE'
		FOR UPDATE
	`, hold.ID).Scan(&holdClaimID)
	if err == pgx.ErrNoRows {
		ex, exErr := s.insertPaymentException(ctx, tx, domain.PaymentException{
			PropertyID:       att.PropertyID,
			HoldID:           &hold.ID,
			PaymentAttemptID: att.ID,
			ExceptionType:    "OVERLAP_ON_CONFIRM",
			AmountPence:      paidAmountPence,
			Currency:         att.Currency,
			Details:          fmt.Sprintf("Active claim for hold %s missing during payment confirmation.", hold.HoldReference),
		})
		if exErr != nil {
			return FinalizeHoldConfirmationResult{}, exErr
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_attempts SET status = 'RECONCILIATION_REQUIRED', provider_payment_intent_id = COALESCE(NULLIF($2,''), provider_payment_intent_id), updated_at = NOW() WHERE id = $1`, att.ID, paymentIntentID); err != nil {
			return FinalizeHoldConfirmationResult{}, MapErr(err)
		}
		return FinalizeHoldConfirmationResult{ExceptionRecorded: &ex}, nil
	}
	if err != nil {
		return FinalizeHoldConfirmationResult{}, MapErr(err)
	}

	quote, err := s.GetLatestQuoteForHoldOrReservation(ctx, tx, hold.PropertyID, &hold.ID, nil)
	if err != nil || quote == nil {
		if err == nil {
			err = domain.ErrNotFound
		}
		return FinalizeHoldConfirmationResult{}, err
	}

	guest, err := s.FindOrCreateGuest(ctx, tx, hold.PropertyID, hold.GuestName, hold.GuestEmail, hold.GuestPhone)
	if err != nil {
		return FinalizeHoldConfirmationResult{}, err
	}
	canonGuestID, err := s.ResolveCanonicalGuestID(ctx, tx, hold.PropertyID, guest.ID)
	if err != nil {
		return FinalizeHoldConfirmationResult{}, err
	}

	resID, err := NewUUID()
	if err != nil {
		return FinalizeHoldConfirmationResult{}, err
	}
	resRef := generateReservationReference(hold.ArrivalDate.Year())
	balanceDue := quote.TotalPence - paidAmountPence
	if balanceDue < 0 {
		balanceDue = 0
	}
	payStatus := domain.PaymentStatusPaid
	if balanceDue > 0 {
		payStatus = domain.PaymentStatusPartiallyPaid
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO reservations (
			id, property_id, reference, guest_id, guest_name, guest_email, guest_phone,
			channel_code, stay_range, adults, children, infants, status,
			payment_status, total_pence, deposit_pence, amount_paid_pence, balance_due_pence,
			currency, origin_hold_id, terms_version, terms_accepted_at, confirmed_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			'DIRECT', daterange($8::date, $9::date, '[)'), $10, $11, $12, 'CONFIRMED',
			$13, $14, $15, $16, $17,
			$18, $19, $20, $21, $22
		)
	`,
		resID, hold.PropertyID, resRef, canonGuestID, hold.GuestName, hold.GuestEmail, hold.GuestPhone,
		hold.Arrival, hold.Departure, hold.Adults, hold.Children, hold.Infants,
		payStatus, quote.TotalPence, quote.DepositDuePence, paidAmountPence, balanceDue,
		quote.Currency, hold.ID, hold.TermsVersion, hold.TermsAcceptedAt, nowUTC,
	); err != nil {
		return FinalizeHoldConfirmationResult{}, MapErr(err)
	}

	ruID, err := NewUUID()
	if err != nil {
		return FinalizeHoldConfirmationResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO reservation_units (id, property_id, reservation_id, unit_id, adults, children, infants)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, ruID, hold.PropertyID, resID, hold.UnitID, hold.Adults, hold.Children, hold.Infants); err != nil {
		return FinalizeHoldConfirmationResult{}, MapErr(err)
	}

	// Atomically retire the HOLD claim and insert the confirmed RESERVATION claim (same TX + GiST protected)
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_claims
		SET status = 'COMPLETED', updated_at = NOW()
		WHERE id = $1 AND status = 'ACTIVE'
	`, holdClaimID); err != nil {
		return FinalizeHoldConfirmationResult{}, MapErr(err)
	}

	resClaimID, err := NewUUID()
	if err != nil {
		return FinalizeHoldConfirmationResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO inventory_claims (
			id, property_id, unit_id, claim_type, reservation_id, channel_scope, claim_range, status, reason
		) VALUES (
			$1, $2, $3, 'RESERVATION', $4, 'DIRECT', daterange($5::date, $6::date, '[)'), 'ACTIVE', $7
		)
	`,
		resClaimID, hold.PropertyID, hold.UnitID, resID,
		hold.Arrival, hold.Departure, "Direct online booking "+resRef,
	); err != nil {
		return FinalizeHoldConfirmationResult{}, MapErr(err)
	}

	// Link quote, hold, payment attempt, payment ledger, and confirmation access token
	if _, err := tx.Exec(ctx, `
		UPDATE booking_quotes SET reservation_id = $2 WHERE id = $1
	`, quote.ID, resID); err != nil {
		return FinalizeHoldConfirmationResult{}, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking_holds
		SET status = 'CONFIRMED', guest_id = $2, confirmed_reservation_id = $3, version = version + 1, updated_at = NOW()
		WHERE id = $1
	`, hold.ID, canonGuestID, resID); err != nil {
		return FinalizeHoldConfirmationResult{}, MapErr(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_attempts
		SET reservation_id = $2,
		    provider_payment_intent_id = COALESCE(NULLIF($3,''), provider_payment_intent_id),
		    status = 'SUCCEEDED',
		    updated_at = NOW()
		WHERE id = $1
	`, att.ID, resID, paymentIntentID); err != nil {
		return FinalizeHoldConfirmationResult{}, MapErr(err)
	}

	payID, err := NewUUID()
	if err != nil {
		return FinalizeHoldConfirmationResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO payments (
			id, property_id, reservation_id, payment_attempt_id, provider,
			provider_payment_intent_id, provider_charge_id, payment_type,
			amount_pence, currency, status, paid_at
		) VALUES ($1, $2, $3, $4, 'stripe', $5, $6, $7, $8, $9, 'SUCCEEDED', $10)
		ON CONFLICT (payment_attempt_id) DO NOTHING
	`, payID, hold.PropertyID, resID, att.ID, paymentIntentID, chargeID, att.PaymentType, paidAmountPence, att.Currency, nowUTC); err != nil {
		return FinalizeHoldConfirmationResult{}, MapErr(err)
	}

	postStayExpiry := hold.DepartureDate.AddDate(0, 0, 30)
	if _, err := tx.Exec(ctx, `
		UPDATE confirmation_access_tokens
		SET reservation_id = $2,
		    expires_at = GREATEST(expires_at, $3)
		WHERE hold_id = $1 AND reservation_id IS NULL AND revoked_at IS NULL
	`, hold.ID, resID, postStayExpiry); err != nil {
		return FinalizeHoldConfirmationResult{}, MapErr(err)
	}

	res, err := s.GetReservation(ctx, tx, hold.PropertyID, resID)
	if err != nil {
		return FinalizeHoldConfirmationResult{}, err
	}
	return FinalizeHoldConfirmationResult{Reservation: &res}, nil
}

func (s *Store) GetBookingHoldForUpdate(ctx context.Context, tx pgx.Tx, holdID uuid.UUID) (domain.BookingHold, error) {
	var h domain.BookingHold
	err := tx.QueryRow(ctx, `
		SELECT h.id, h.property_id, h.guest_id, h.hold_reference, h.unit_type_id, h.unit_id, ut.name,
		       h.rate_plan_id, h.channel_code, lower(h.stay_range), upper(h.stay_range),
		       h.adults, h.children, h.infants, h.guest_name, h.guest_email, h.guest_phone,
		       h.special_requests, h.terms_version, h.terms_accepted_at, h.customer_session_hash,
		       h.status, h.expires_at, h.confirmed_reservation_id, h.version, h.created_at, h.updated_at
		FROM booking_holds h
		JOIN unit_types ut ON ut.id = h.unit_type_id
		WHERE h.id = $1
		FOR UPDATE OF h
	`, holdID).Scan(
		&h.ID, &h.PropertyID, &h.GuestID, &h.HoldReference, &h.UnitTypeID, &h.UnitID, &h.UnitTypeName,
		&h.RatePlanID, &h.ChannelCode, &h.ArrivalDate, &h.DepartureDate,
		&h.Adults, &h.Children, &h.Infants, &h.GuestName, &h.GuestEmail, &h.GuestPhone,
		&h.SpecialRequests, &h.TermsVersion, &h.TermsAcceptedAt, &h.CustomerSessionHash,
		&h.Status, &h.ExpiresAt, &h.ConfirmedReservationID, &h.Version, &h.CreatedAt, &h.UpdatedAt,
	)
	if err != nil {
		return domain.BookingHold{}, MapErr(err)
	}
	h.Arrival = domain.FormatDate(h.ArrivalDate)
	h.Departure = domain.FormatDate(h.DepartureDate)
	return h, nil
}

func (s *Store) RecordPaymentFailure(ctx context.Context, tx pgx.Tx, attemptID uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx, `
		UPDATE payment_attempts
		SET status = CASE WHEN status = 'SUCCEEDED' THEN status ELSE 'FAILED' END,
		    failure_reason = CASE WHEN status = 'SUCCEEDED' THEN failure_reason ELSE $2 END,
		    updated_at = NOW()
		WHERE id = $1
	`, attemptID, reason)
	return MapErr(err)
}

type StripeRefundEntry struct {
	ProviderRefundID string
	AmountPence      int64
	Currency         string
	Status           string // PENDING | SUCCEEDED | FAILED
	Reason           string
	OccurredAt       time.Time
	IdempotencyKey   string
}

func (s *Store) RecordRefundOutcome(
	ctx context.Context,
	tx pgx.Tx,
	paymentIntentID, chargeID, eventID string,
	eventOccurredAt time.Time,
	amountRefundedPence int64,
	chargeCurrency string,
	entries []StripeRefundEntry,
) error {
	var attID, propID uuid.UUID
	var resID *uuid.UUID
	var paidAmt int64
	var attCur string
	err := tx.QueryRow(ctx, `
		SELECT id, property_id, reservation_id, amount_pence, currency
		FROM payment_attempts
		WHERE provider_payment_intent_id = $1
		LIMIT 1
		FOR UPDATE
	`, paymentIntentID).Scan(&attID, &propID, &resID, &paidAmt, &attCur)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return MapErr(err)
	}

	var payID *uuid.UUID
	var currentPayStatus string
	var pID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, status
		FROM payments
		WHERE payment_attempt_id = $1
		LIMIT 1
		FOR UPDATE
	`, attID).Scan(&pID, &currentPayStatus)
	if err == nil {
		payID = &pID
	} else if err != pgx.ErrNoRows {
		return MapErr(err)
	}

	defaultCur := strings.ToUpper(strings.TrimSpace(chargeCurrency))
	if defaultCur == "" {
		defaultCur = strings.ToUpper(strings.TrimSpace(attCur))
	}
	if defaultCur == "" {
		defaultCur = "GBP"
	}
	if eventOccurredAt.IsZero() {
		eventOccurredAt = time.Now().UTC()
	}

	if len(entries) > 0 {
		for idx, entry := range entries {
			if entry.AmountPence <= 0 {
				continue
			}
			cur := strings.ToUpper(strings.TrimSpace(entry.Currency))
			if cur == "" {
				cur = defaultCur
			}
			st := strings.ToUpper(strings.TrimSpace(entry.Status))
			switch st {
			case "PENDING", "SUCCEEDED", "FAILED":
			default:
				st = "SUCCEEDED"
			}
			occAt := entry.OccurredAt.UTC()
			if occAt.IsZero() {
				occAt = eventOccurredAt.UTC()
			}
			rfIDStr := strings.TrimSpace(entry.ProviderRefundID)
			idem := strings.TrimSpace(entry.IdempotencyKey)
			if idem == "" {
				if rfIDStr != "" {
					idem = "stripe_refund:" + rfIDStr
				} else {
					idem = fmt.Sprintf("stripe_event:%s:%d", eventID, idx)
				}
			}

			if rfIDStr != "" {
				newID, idErr := NewUUID()
				if idErr != nil {
					return idErr
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO refunds (
						id, property_id, payment_attempt_id, payment_id, reservation_id,
						provider, provider_refund_id, amount_pence, currency, reason, status,
						idempotency_key, occurred_at, created_at, updated_at
					) VALUES (
						$1, $2, $3, $4, $5,
						'stripe', $6, $7, $8, $9, $10,
						$11, $12, $12, NOW()
					)
					ON CONFLICT (property_id, provider, provider_refund_id) WHERE provider_refund_id <> ''
					DO UPDATE SET
						payment_id = COALESCE(refunds.payment_id, EXCLUDED.payment_id),
						status = EXCLUDED.status,
						reason = CASE WHEN EXCLUDED.reason <> '' THEN EXCLUDED.reason ELSE refunds.reason END,
						occurred_at = EXCLUDED.occurred_at,
						updated_at = NOW()
				`, newID, propID, attID, payID, resID, rfIDStr, entry.AmountPence, cur, entry.Reason, st, idem, occAt); err != nil {
					return MapErr(err)
				}
			} else {
				newID, idErr := NewUUID()
				if idErr != nil {
					return idErr
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO refunds (
						id, property_id, payment_attempt_id, payment_id, reservation_id,
						provider, provider_refund_id, amount_pence, currency, reason, status,
						idempotency_key, occurred_at, created_at, updated_at
					) VALUES (
						$1, $2, $3, $4, $5,
						'stripe', '', $6, $7, $8, $9,
						$10, $11, $11, NOW()
					)
					ON CONFLICT (property_id, idempotency_key) DO NOTHING
				`, newID, propID, attID, payID, resID, entry.AmountPence, cur, entry.Reason, st, idem, occAt); err != nil {
					return MapErr(err)
				}
			}
		}
	} else if amountRefundedPence > 0 && eventID != "" {
		// Legacy/compact webhook fixture omitting refunds.data[]: persist incremental delta using webhook event timestamp.
		var existingSucceeded int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount_pence), 0)::bigint
			FROM refunds
			WHERE payment_attempt_id = $1 AND status = 'SUCCEEDED'
		`, attID).Scan(&existingSucceeded); err != nil {
			return MapErr(err)
		}
		if delta := amountRefundedPence - existingSucceeded; delta > 0 {
			newID, idErr := NewUUID()
			if idErr != nil {
				return idErr
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO refunds (
					id, property_id, payment_attempt_id, payment_id, reservation_id,
					provider, provider_refund_id, amount_pence, currency, reason, status,
					idempotency_key, occurred_at, created_at, updated_at
				) VALUES (
					$1, $2, $3, $4, $5,
					'stripe', '', $6, $7, 'stripe_charge_refunded_webhook', 'SUCCEEDED',
					$8, $9, $9, NOW()
				)
				ON CONFLICT (property_id, idempotency_key) DO NOTHING
			`, newID, propID, attID, payID, resID, delta, defaultCur, "stripe_event:"+eventID, eventOccurredAt.UTC()); err != nil {
				return MapErr(err)
			}
		}
	}

	var totalSucceeded int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_pence), 0)::bigint
		FROM refunds
		WHERE payment_attempt_id = $1 AND status = 'SUCCEEDED'
	`, attID).Scan(&totalSucceeded); err != nil {
		return MapErr(err)
	}
	effectiveRefunded := totalSucceeded
	if amountRefundedPence > effectiveRefunded {
		effectiveRefunded = amountRefundedPence
	}

	nextAttStatus := domain.AttemptStatusSucceeded
	nextPayStatus := domain.PaymentStatusPaid
	if (paidAmt > 0 && effectiveRefunded >= paidAmt) || currentPayStatus == domain.AttemptStatusRefunded {
		nextAttStatus = domain.AttemptStatusRefunded
		nextPayStatus = domain.PaymentStatusRefunded
	} else if effectiveRefunded > 0 {
		nextAttStatus = domain.AttemptStatusPartiallyRefunded
		nextPayStatus = domain.PaymentStatusPartiallyRefunded
	} else if currentPayStatus == domain.AttemptStatusPartiallyRefunded {
		nextAttStatus = domain.AttemptStatusPartiallyRefunded
		nextPayStatus = domain.PaymentStatusPartiallyRefunded
	}

	if _, err := tx.Exec(ctx, `UPDATE payment_attempts SET status = $2, updated_at = NOW() WHERE id = $1`, attID, nextAttStatus); err != nil {
		return MapErr(err)
	}
	if payID != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE payments
			SET status = $2,
			    provider_charge_id = CASE WHEN $3 <> '' AND provider_charge_id = '' THEN $3 ELSE provider_charge_id END
			WHERE id = $1
		`, *payID, nextAttStatus, strings.TrimSpace(chargeID)); err != nil {
			return MapErr(err)
		}
	}
	if resID != nil {
		if _, err := tx.Exec(ctx, `UPDATE reservations SET payment_status = $2, version = version + 1, updated_at = NOW() WHERE id = $1`, *resID, nextPayStatus); err != nil {
			return MapErr(err)
		}
	}
	return nil
}

func (s *Store) insertPaymentException(ctx context.Context, tx pgx.Tx, ex domain.PaymentException) (domain.PaymentException, error) {
	id, err := NewUUID()
	if err != nil {
		return domain.PaymentException{}, err
	}
	ex.ID = id
	ex.Status = "OPEN"
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_exceptions (
			id, property_id, hold_id, payment_attempt_id, reservation_id,
			exception_type, amount_pence, currency, details, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'OPEN')
		RETURNING created_at
	`, ex.ID, ex.PropertyID, ex.HoldID, ex.PaymentAttemptID, ex.ReservationID,
		ex.ExceptionType, ex.AmountPence, ex.Currency, ex.Details,
	).Scan(&ex.CreatedAt)
	return ex, MapErr(err)
}

func (s *Store) ListPaymentsForReservation(ctx context.Context, db DBTX, propertyID, reservationID uuid.UUID) ([]domain.PaymentRecord, error) {
	if db == nil {
		db = s.pool
	}
	rows, err := db.Query(ctx, `
		SELECT id, property_id, reservation_id, payment_attempt_id, provider,
		       provider_payment_intent_id, provider_charge_id, payment_type,
		       amount_pence, currency, status, paid_at, created_at
		FROM payments
		WHERE property_id = $1 AND reservation_id = $2
		ORDER BY paid_at DESC
	`, propertyID, reservationID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.PaymentRecord
	for rows.Next() {
		var p domain.PaymentRecord
		if err := rows.Scan(
			&p.ID, &p.PropertyID, &p.ReservationID, &p.PaymentAttemptID, &p.Provider,
			&p.ProviderPaymentIntentID, &p.ProviderChargeID, &p.PaymentType,
			&p.AmountPence, &p.Currency, &p.Status, &p.PaidAt, &p.CreatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, p)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetPublicConfirmationByRefAndToken(ctx context.Context, db DBTX, ref, plainToken string, nowUTC time.Time) (domain.PublicConfirmationView, uuid.UUID, *uuid.UUID, *uuid.UUID, error) {
	if db == nil {
		db = s.pool
	}
	tokenHash := HashConfirmationToken(plainToken)
	var tokPropID uuid.UUID
	var resID, holdID *uuid.UUID
	err := db.QueryRow(ctx, `
		SELECT property_id, reservation_id, hold_id
		FROM confirmation_access_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2
	`, tokenHash, nowUTC).Scan(&tokPropID, &resID, &holdID)
	if err == pgx.ErrNoRows {
		return domain.PublicConfirmationView{}, uuid.Nil, nil, nil, domain.ErrNotFound
	}
	if err != nil {
		return domain.PublicConfirmationView{}, uuid.Nil, nil, nil, MapErr(err)
	}
	_, _ = db.Exec(ctx, `
		UPDATE confirmation_access_tokens
		SET last_used_at = $2
		WHERE token_hash = $1 AND revoked_at IS NULL
	`, tokenHash, nowUTC)

	prop, err := s.GetPropertyByID(ctx, db, tokPropID)
	if err != nil {
		return domain.PublicConfirmationView{}, uuid.Nil, nil, nil, err
	}
	policy, err := s.GetBookingPolicy(ctx, db, tokPropID)
	if err != nil {
		return domain.PublicConfirmationView{}, uuid.Nil, nil, nil, err
	}

	refUpper := strings.ToUpper(strings.TrimSpace(ref))
	if resID != nil {
		res, err := s.GetReservation(ctx, db, tokPropID, *resID)
		if err != nil {
			return domain.PublicConfirmationView{}, uuid.Nil, nil, nil, err
		}
		if refUpper != "" && res.Reference != refUpper {
			// Also allow matching via origin hold reference while transitioning
			if holdID != nil {
				h, hErr := s.GetBookingHold(ctx, db, *holdID)
				if hErr != nil || h.HoldReference != refUpper {
					return domain.PublicConfirmationView{}, uuid.Nil, nil, nil, domain.ErrNotFound
				}
			} else {
				return domain.PublicConfirmationView{}, uuid.Nil, nil, nil, domain.ErrNotFound
			}
		}
		quote, _ := s.GetLatestQuoteForHoldOrReservation(ctx, db, tokPropID, nil, &res.ID)
		payments, _ := s.ListPaymentsForReservation(ctx, db, tokPropID, res.ID)
		utName := ""
		if len(res.Units) > 0 {
			utName = res.Units[0].UnitTypeName
		} else if quote != nil {
			utName = quote.UnitTypeName
		}
		nights := int(res.DepartureDate.Sub(res.ArrivalDate).Hours() / 24)
		view := domain.PublicConfirmationView{
			Status:                string(res.Status),
			ReservationReference:  res.Reference,
			PropertyName:          prop.Name,
			PropertySlug:          prop.Slug,
			ContactEmail:          prop.ContactEmail,
			ContactPhone:          prop.ContactPhone,
			CheckInTime:           prop.CheckInTime,
			CheckOutTime:          prop.CheckOutTime,
			CancellationPolicy:    policy.CancellationPolicy,
			UnitTypeName:          utName,
			ArrivalDate:           domain.FormatDate(res.ArrivalDate),
			DepartureDate:         domain.FormatDate(res.DepartureDate),
			Nights:                nights,
			Adults:                res.Adults,
			Children:              res.Children,
			Infants:               res.Infants,
			GuestName:             res.GuestName,
			GuestEmail:            res.GuestEmail,
			PaymentStatus:         res.PaymentStatus,
			Quote:                 quote,
			Payments:              payments,
			CancellationRequested: res.CancellationRequestedAt != nil,
		}
		return view, tokPropID, &res.ID, holdID, nil
	}

	if holdID == nil {
		return domain.PublicConfirmationView{}, uuid.Nil, nil, nil, domain.ErrNotFound
	}
	hold, err := s.GetBookingHold(ctx, db, *holdID)
	if err != nil {
		return domain.PublicConfirmationView{}, uuid.Nil, nil, nil, err
	}
	if refUpper != "" && hold.HoldReference != refUpper {
		return domain.PublicConfirmationView{}, uuid.Nil, nil, nil, domain.ErrNotFound
	}
	if hold.ConfirmedReservationID != nil {
		res, err := s.GetReservation(ctx, db, tokPropID, *hold.ConfirmedReservationID)
		if err != nil {
			return domain.PublicConfirmationView{}, uuid.Nil, nil, nil, err
		}
		quote, _ := s.GetLatestQuoteForHoldOrReservation(ctx, db, tokPropID, nil, &res.ID)
		payments, _ := s.ListPaymentsForReservation(ctx, db, tokPropID, res.ID)
		nights := int(res.DepartureDate.Sub(res.ArrivalDate).Hours() / 24)
		view := domain.PublicConfirmationView{
			Status:                string(res.Status),
			HoldReference:         hold.HoldReference,
			ReservationReference:  res.Reference,
			PropertyName:          prop.Name,
			PropertySlug:          prop.Slug,
			ContactEmail:          prop.ContactEmail,
			ContactPhone:          prop.ContactPhone,
			CheckInTime:           prop.CheckInTime,
			CheckOutTime:          prop.CheckOutTime,
			CancellationPolicy:    policy.CancellationPolicy,
			UnitTypeName:          hold.UnitTypeName,
			ArrivalDate:           domain.FormatDate(res.ArrivalDate),
			DepartureDate:         domain.FormatDate(res.DepartureDate),
			Nights:                nights,
			Adults:                res.Adults,
			Children:              res.Children,
			Infants:               res.Infants,
			GuestName:             res.GuestName,
			GuestEmail:            res.GuestEmail,
			PaymentStatus:         res.PaymentStatus,
			Quote:                 quote,
			Payments:              payments,
			CancellationRequested: res.CancellationRequestedAt != nil,
		}
		return view, tokPropID, &res.ID, holdID, nil
	}

	quote, _ := s.GetLatestQuoteForHoldOrReservation(ctx, db, tokPropID, &hold.ID, nil)
	nights := int(hold.DepartureDate.Sub(hold.ArrivalDate).Hours() / 24)
	view := domain.PublicConfirmationView{
		Status:             "PENDING_PAYMENT_" + hold.Status,
		HoldReference:      hold.HoldReference,
		PropertyName:       prop.Name,
		PropertySlug:       prop.Slug,
		ContactEmail:       prop.ContactEmail,
		ContactPhone:       prop.ContactPhone,
		CheckInTime:        prop.CheckInTime,
		CheckOutTime:       prop.CheckOutTime,
		CancellationPolicy: policy.CancellationPolicy,
		UnitTypeName:       hold.UnitTypeName,
		ArrivalDate:        hold.Arrival,
		DepartureDate:      hold.Departure,
		Nights:             nights,
		Adults:             hold.Adults,
		Children:           hold.Children,
		Infants:            hold.Infants,
		GuestName:          hold.GuestName,
		GuestEmail:         hold.GuestEmail,
		PaymentStatus:      domain.PaymentStatusUnpaid,
		Quote:              quote,
		HoldExpiresAt:      &hold.ExpiresAt,
	}
	return view, tokPropID, nil, holdID, nil
}

func (s *Store) RecordCustomerCancellationRequest(ctx context.Context, tx pgx.Tx, propertyID, resID uuid.UUID, note string) (domain.Reservation, error) {
	if _, err := tx.Exec(ctx, `
		UPDATE reservations
		SET cancellation_requested_at = COALESCE(cancellation_requested_at, NOW()),
		    cancellation_request_note = $3,
		    version = version + 1,
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2
	`, propertyID, resID, strings.TrimSpace(note)); err != nil {
		return domain.Reservation{}, MapErr(err)
	}
	return s.GetReservation(ctx, tx, propertyID, resID)
}

func (s *Store) RotateConfirmationToken(ctx context.Context, tx pgx.Tx, ref, currentPlainToken string, nowUTC time.Time) (string, error) {
	view, propID, resID, holdID, err := s.GetPublicConfirmationByRefAndToken(ctx, tx, ref, currentPlainToken, nowUTC)
	if err != nil {
		return "", err
	}
	_ = view
	oldHash := HashConfirmationToken(currentPlainToken)
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE confirmation_access_tokens
		SET revoked_at = $2
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2
		RETURNING expires_at
	`, oldHash, nowUTC).Scan(&expiresAt)
	if err == pgx.ErrNoRows {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", MapErr(err)
	}
	return s.CreateConfirmationToken(ctx, tx, propID, holdID, resID, expiresAt)
}

func (s *Store) QueueEmail(ctx context.Context, db DBTX, item domain.EmailOutboxItem) (domain.EmailOutboxItem, error) {
	if db == nil {
		db = s.pool
	}
	if item.ID == uuid.Nil {
		id, err := NewUUID()
		if err != nil {
			return domain.EmailOutboxItem{}, err
		}
		item.ID = id
	}
	if item.NextAttemptAt.IsZero() {
		item.NextAttemptAt = time.Now().UTC()
	}
	if item.GuestID != nil || item.ReservationID != nil {
		// Resolve and lock canonical guest cluster BEFORE inserting into email_outbox so
		// concurrent AnonymizeGuest cannot pass validation simultaneously.
		var targetGuestID uuid.UUID
		if item.GuestID != nil {
			targetGuestID = *item.GuestID
		} else if item.ReservationID != nil {
			var resGuestID *uuid.UUID
			if err := db.QueryRow(ctx, `
				SELECT guest_id FROM reservations WHERE property_id = $1 AND id = $2 FOR UPDATE
			`, item.PropertyID, *item.ReservationID).Scan(&resGuestID); err != nil {
				return domain.EmailOutboxItem{}, MapErr(err)
			}
			if resGuestID != nil {
				targetGuestID = *resGuestID
			}
		}
		if targetGuestID != uuid.Nil {
			anonOrPending, lockErr := s.lockGuestClusterAndCheckErasureDB(ctx, db, item.PropertyID, targetGuestID)
			if lockErr != nil {
				return domain.EmailOutboxItem{}, lockErr
			}
			if anonOrPending {
				return domain.EmailOutboxItem{}, fmt.Errorf("%w: cannot queue email for an anonymized or erasure-pending guest", domain.ErrConflict)
			}
		}
	}
	item.Status = "PENDING"
	err := db.QueryRow(ctx, `
		INSERT INTO email_outbox (
			id, property_id, reservation_id, guest_id, communication_id,
			recipient_email, template, subject, body_text, body_html, status, next_attempt_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'PENDING', $11)
		ON CONFLICT (reservation_id, template) WHERE reservation_id IS NOT NULL DO NOTHING
		RETURNING next_attempt_at, created_at
	`,
		item.ID, item.PropertyID, item.ReservationID, item.GuestID, item.CommunicationID, item.RecipientEmail,
		item.Template, item.Subject, item.BodyText, item.BodyHTML, item.NextAttemptAt,
	).Scan(&item.NextAttemptAt, &item.CreatedAt)
	if err == pgx.ErrNoRows && item.ReservationID != nil {
		err = db.QueryRow(ctx, `
			SELECT id, guest_id, communication_id, status, attempts, last_error, next_attempt_at, sent_at, created_at
			FROM email_outbox
			WHERE reservation_id = $1 AND template = $2
		`, *item.ReservationID, item.Template).Scan(
			&item.ID, &item.GuestID, &item.CommunicationID, &item.Status, &item.Attempts,
			&item.LastError, &item.NextAttemptAt, &item.SentAt, &item.CreatedAt,
		)
	}
	return item, MapErr(err)
}

// sanitizeOutboxErrorForAnon maps raw transport/SMTP errors (which often echo recipient email
// addresses or display names) to safe non-PII diagnostic categories whenever the guest is
// anonymised or has a pending erasure request.
func sanitizeOutboxErrorForAnon(rawErr string, permanent bool) string {
	lower := strings.ToLower(strings.TrimSpace(rawErr))
	switch {
	case strings.Contains(lower, "unconfigured"):
		return "[redacted on anonymisation: provider unconfigured]"
	case strings.Contains(lower, "invalid recipient"):
		return "[redacted on anonymisation: invalid recipient]"
	case permanent || strings.Contains(lower, "bounce") || strings.Contains(lower, "550") || strings.Contains(lower, "user unknown"):
		return "[redacted on anonymisation: permanent delivery error]"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline"):
		return "[redacted on anonymisation: transport timeout]"
	default:
		return "[redacted on anonymisation: transient delivery error]"
	}
}

// SweepStaleOutboxDispatches fences ONLY active DISPATCHING rows whose worker
// heartbeat lease has actually expired (dispatch_lease_expires_at <= clk.ts),
// moving them to DISPATCH_UNKNOWN so late/stalled workers cannot renew or settle
// and operators must reconcile with external evidence. Live heartbeating workers
// are never fenced based on total runtime (dispatched_at) alone.
// When nowUTC.IsZero(), PostgreSQL clock_timestamp() is used as the single clock authority.
func (s *Store) SweepStaleOutboxDispatches(ctx context.Context, nowUTC time.Time) ([]uuid.UUID, error) {
	var nowPtr *time.Time
	if !nowUTC.IsZero() {
		u := nowUTC.UTC()
		nowPtr = &u
	}
	var fencedIDs []uuid.UUID
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		rows, qErr := tx.Query(ctx, `
			WITH clk AS (
				SELECT COALESCE($1::timestamptz, clock_timestamp()) AS ts
			),
			stale AS (
				SELECT o.id, clk.ts
				FROM email_outbox o
				CROSS JOIN clk
				WHERE o.status = 'DISPATCHING'
				  AND o.dispatch_fenced_at IS NULL
				  AND (
				      (o.dispatch_lease_expires_at IS NOT NULL AND o.dispatch_lease_expires_at <= clk.ts)
				      OR (o.dispatch_lease_expires_at IS NULL AND o.dispatched_at IS NOT NULL AND o.dispatched_at <= (clk.ts - INTERVAL '60 seconds'))
				  )
				ORDER BY COALESCE(o.dispatch_lease_expires_at, o.dispatched_at) ASC
				FOR UPDATE OF o SKIP LOCKED
			)
			UPDATE email_outbox o
			SET status = 'DISPATCH_UNKNOWN',
			    dispatch_fenced_at = stale.ts,
			    dispatch_fence_epoch = o.dispatch_fence_epoch + 1,
			    dispatch_outcome = 'WORKER_FENCED',
			    dispatch_lease_expires_at = NULL,
			    last_error = CASE
			        WHEN EXISTS (
			            SELECT 1
			            FROM reservations r
			            LEFT JOIN guests g ON g.property_id = o.property_id AND g.id = COALESCE(o.guest_id, r.guest_id)
			            WHERE (o.guest_id IS NOT NULL OR r.id = o.reservation_id)
			              AND (g.status = 'ANONYMIZED' OR g.anonymized_at IS NOT NULL OR g.erasure_requested_at IS NOT NULL)
			        ) THEN '[redacted on anonymisation: dispatch outcome unknown after worker lease expiry]'
			        ELSE 'Dispatch worker fenced after heartbeat lease expired; operator reconciliation required'
			    END,
			    locked_at = NULL,
			    worker_token = NULL,
			    updated_at = NOW()
			FROM stale
			WHERE o.id = stale.id
			  AND o.status = 'DISPATCHING'
			  AND o.dispatch_fenced_at IS NULL
			RETURNING o.property_id, o.id, o.communication_id, stale.ts
		`, nowPtr)
		if qErr != nil {
			return MapErr(qErr)
		}
		type unknownRef struct {
			propID   uuid.UUID
			id       uuid.UUID
			commID   *uuid.UUID
			fencedAt time.Time
		}
		var refs []unknownRef
		for rows.Next() {
			var r unknownRef
			if err := rows.Scan(&r.propID, &r.id, &r.commID, &r.fencedAt); err != nil {
				rows.Close()
				return MapErr(err)
			}
			refs = append(refs, r)
			fencedIDs = append(fencedIDs, r.id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return MapErr(err)
		}
		for _, r := range refs {
			if r.commID != nil {
				evID, err := NewUUID()
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO communication_delivery_events (
						id, property_id, communication_id, email_outbox_id, event_type, provider, detail, occurred_at
					) VALUES ($1, $2, $3, $4, 'DISPATCH_UNKNOWN', 'system', 'Worker heartbeat lease expired during external dispatch; worker fenced pending evidence-backed reconciliation', $5)
				`, evID, r.propID, *r.commID, r.id, r.fencedAt); err != nil {
					return MapErr(err)
				}
			}
		}
		return nil
	})
	return fencedIDs, err
}

// ClaimDueOutboxEmails first cancels any eligible/stale pre-transport outbox emails belonging to
// anonymized/erasure-pending guests or CANCEL_REQUESTED leases (explicitly excluding in-flight DISPATCHING
// or DISPATCH_UNKNOWN rows), then atomically claims due PENDING emails (or stale pre-transport PROCESSING
// leases) using FOR UPDATE SKIP LOCKED with a fresh worker_token per lease.
func (s *Store) ClaimDueOutboxEmails(ctx context.Context, nowUTC time.Time, limit int) ([]domain.EmailOutboxItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	staleLockCutoff := nowUTC.Add(-5 * time.Minute)
	var out []domain.EmailOutboxItem

	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		// 1. Cleanly cancel PENDING / FAILED / stale pre-transport PROCESSING/CANCEL_REQUESTED rows
		// whose guest is anonymized, erasure-pending, or already flagged CANCEL_REQUESTED.
		cRows, cErr := tx.Query(ctx, `
			WITH to_cancel AS (
				SELECT o.id
				FROM email_outbox o
				LEFT JOIN reservations r ON r.property_id = o.property_id AND r.id = o.reservation_id
				LEFT JOIN guests g ON g.property_id = o.property_id AND g.id = COALESCE(o.guest_id, r.guest_id)
				WHERE (
					o.status IN ('PENDING', 'FAILED')
					OR (o.status IN ('PROCESSING', 'CANCEL_REQUESTED') AND o.dispatched_at IS NULL AND o.locked_at IS NOT NULL AND o.locked_at < $2)
				)
				AND (
					o.status = 'CANCEL_REQUESTED'
					OR g.status = 'ANONYMIZED'
					OR g.anonymized_at IS NOT NULL
					OR g.erasure_requested_at IS NOT NULL
					OR o.recipient_email IN ('', '[redacted]')
				)
				FOR UPDATE OF o SKIP LOCKED
			)
			UPDATE email_outbox o
			SET status = 'CANCELLED',
			    recipient_email = '[redacted]',
			    subject = '[Anonymised Email]',
			    body_text = '[redacted by GDPR erasure]',
			    body_html = '',
			    last_error = CASE WHEN TRIM(o.last_error) = '' THEN '' ELSE '[redacted on anonymisation]' END,
			    cancel_reason = CASE
			        WHEN o.cancel_reason <> '' THEN o.cancel_reason
			        ELSE 'Cancelled before delivery: associated guest anonymised'
			    END,
			    cancelled_at = COALESCE(o.cancelled_at, $1),
			    locked_at = NULL,
			    worker_token = NULL,
			    updated_at = NOW()
			FROM to_cancel
			WHERE o.id = to_cancel.id
			RETURNING o.property_id, o.id, o.communication_id, o.cancel_reason
		`, nowUTC, staleLockCutoff)
		if cErr != nil {
			return MapErr(cErr)
		}
		type cancelledRef struct {
			propID uuid.UUID
			id     uuid.UUID
			commID *uuid.UUID
			reason string
		}
		var cancelledRefs []cancelledRef
		for cRows.Next() {
			var r cancelledRef
			if sErr := cRows.Scan(&r.propID, &r.id, &r.commID, &r.reason); sErr != nil {
				cRows.Close()
				return MapErr(sErr)
			}
			cancelledRefs = append(cancelledRefs, r)
		}
		cRows.Close()
		if err := cRows.Err(); err != nil {
			return MapErr(err)
		}
		for _, cr := range cancelledRefs {
			if cr.commID != nil {
				if err := s.recordCancelledCommunicationTx(ctx, tx, cr.propID, cr.id, *cr.commID, cr.reason, nowUTC); err != nil {
					return err
				}
			}
		}

		// 2. Claim valid non-anonymized due items with unique lease worker_token.
		// Never re-claim DISPATCHING or DISPATCH_UNKNOWN rows automatically.
		rows, err := tx.Query(ctx, `
			WITH due AS (
				SELECT o.id
				FROM email_outbox o
				LEFT JOIN reservations r ON r.property_id = o.property_id AND r.id = o.reservation_id
				LEFT JOIN guests g ON g.property_id = o.property_id AND g.id = COALESCE(o.guest_id, r.guest_id)
				WHERE o.attempts < 5
				  AND (
					(o.status = 'PENDING' AND o.next_attempt_at <= $1)
					OR (o.status = 'PROCESSING' AND o.dispatched_at IS NULL AND o.locked_at IS NOT NULL AND o.locked_at < $2)
				  )
				  AND (g.id IS NULL OR (g.status <> 'ANONYMIZED' AND g.anonymized_at IS NULL AND g.erasure_requested_at IS NULL))
				  AND o.recipient_email NOT IN ('', '[redacted]')
				ORDER BY o.created_at ASC
				LIMIT $3
				FOR UPDATE OF o SKIP LOCKED
			)
			UPDATE email_outbox o
			SET status = 'PROCESSING',
			    locked_at = $1,
			    worker_token = gen_random_uuid(),
			    updated_at = NOW()
			FROM due
			WHERE o.id = due.id
			RETURNING o.id, o.property_id, o.reservation_id, o.guest_id, o.communication_id,
			          o.recipient_email, o.template, o.subject, o.body_text, o.body_html,
			          o.status, o.attempts, o.last_error, o.cancel_reason, o.cancelled_at,
			          o.worker_token, o.dispatched_at, o.dispatch_worker_id, o.next_attempt_at, o.sent_at, o.created_at
		`, nowUTC, staleLockCutoff, limit)
		if err != nil {
			return MapErr(err)
		}
		defer rows.Close()
		for rows.Next() {
			var e domain.EmailOutboxItem
			if err := rows.Scan(
				&e.ID, &e.PropertyID, &e.ReservationID, &e.GuestID, &e.CommunicationID,
				&e.RecipientEmail, &e.Template, &e.Subject, &e.BodyText, &e.BodyHTML,
				&e.Status, &e.Attempts, &e.LastError, &e.CancelReason, &e.CancelledAt,
				&e.WorkerToken, &e.DispatchedAt, &e.DispatchWorkerID, &e.NextAttemptAt, &e.SentAt, &e.CreatedAt,
			); err != nil {
				return MapErr(err)
			}
			out = append(out, e)
		}
		return MapErr(rows.Err())
	})
	return out, err
}

func (s *Store) recordCancelledCommunicationTx(ctx context.Context, tx pgx.Tx, propertyID, outboxID, commID uuid.UUID, reason string, at time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE guest_communications
		SET delivery_status = CASE WHEN delivery_status IN ('ACCEPTED', 'DELIVERED') THEN delivery_status ELSE 'CANCELLED' END,
		    updated_at = NOW()
		WHERE id = $1
	`, commID); err != nil {
		return MapErr(err)
	}
	evID, err := NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO communication_delivery_events (
			id, property_id, communication_id, email_outbox_id, event_type, provider, detail, occurred_at
		) VALUES ($1, $2, $3, $4, 'CANCELLED', 'system', $5, $6)
	`, evID, propertyID, commID, outboxID, reason, at); err != nil {
		return MapErr(err)
	}
	return nil
}

// AcquireOutboxSendPermission transitions a PROCESSING item into durable DISPATCHING state using
// the default worker identifier and default 60s lease before invoking external mail transport.
func (s *Store) AcquireOutboxSendPermission(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, nowUTC time.Time) (bool, error) {
	return s.BeginOutboxDispatch(ctx, id, workerToken, "outbox-worker", nowUTC)
}

// BeginOutboxDispatch transitions a PROCESSING item into DISPATCHING with a 60s default lease.
func (s *Store) BeginOutboxDispatch(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, workerID string, nowUTC time.Time) (bool, error) {
	ok, _, err := s.BeginOutboxDispatchLease(ctx, id, workerToken, workerID, nowUTC, 60*time.Second)
	return ok, err
}

// BeginOutboxDispatchLease atomically validates ownership, active guest state (via canonical
// guest-cluster lock acquired BEFORE email_outbox), and reservation/enquiry links, then transitions
// the row from PROCESSING to DISPATCHING with worker identity, heartbeat lease expiry, and a
// monotonically incremented fence epoch before committing the DB transaction.
// When nowUTC.IsZero(), PostgreSQL clock_timestamp() is used as the single clock authority.
func (s *Store) BeginOutboxDispatchLease(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, workerID string, nowUTC time.Time, leaseTTL time.Duration) (bool, int64, error) {
	cleanWorker := strings.TrimSpace(workerID)
	if cleanWorker == "" {
		cleanWorker = "outbox-worker"
	}
	if leaseTTL <= 0 {
		leaseTTL = 60 * time.Second
	}
	leaseMicros := leaseTTL.Microseconds()
	if leaseMicros < 1000 {
		leaseMicros = 1000
	}
	var nowPtr *time.Time
	if !nowUTC.IsZero() {
		u := nowUTC.UTC()
		nowPtr = &u
	}
	allowed := false
	var fenceEpoch int64
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		// Step 1: Identify property and linked guest without locking email_outbox first, then lock the
		// canonical guest cluster in sorted UUID order BEFORE locking email_outbox (same hierarchy as AnonymizeGuestAt).
		var probePropID uuid.UUID
		var probeGuestID, probeResID *uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT o.property_id, o.guest_id, o.reservation_id
			FROM email_outbox o
			WHERE o.id = $1
		`, id).Scan(&probePropID, &probeGuestID, &probeResID); err != nil {
			return MapErr(err)
		}
		targetGuestID := uuid.Nil
		if probeGuestID != nil {
			targetGuestID = *probeGuestID
		} else if probeResID != nil {
			var rGuest *uuid.UUID
			_ = tx.QueryRow(ctx, `SELECT guest_id FROM reservations WHERE property_id = $1 AND id = $2`, probePropID, *probeResID).Scan(&rGuest)
			if rGuest != nil {
				targetGuestID = *rGuest
			}
		}
		guestBlocked := false
		if targetGuestID != uuid.Nil {
			var lockErr error
			guestBlocked, lockErr = s.lockGuestClusterAndCheckErasureDB(ctx, tx, probePropID, targetGuestID)
			if lockErr != nil && !errors.Is(lockErr, domain.ErrNotFound) {
				return lockErr
			}
		}

		var propID uuid.UUID
		var resID, guestID, commID, rowToken *uuid.UUID
		var status, recEmail, cancelReason string
		var rowGuestBlocked bool
		var evalNow time.Time
		err := tx.QueryRow(ctx, `
			SELECT o.property_id, o.reservation_id, o.guest_id, o.communication_id,
			       o.status, o.recipient_email, o.cancel_reason, o.worker_token,
			       COALESCE(g.status = 'ANONYMIZED' OR g.anonymized_at IS NOT NULL OR g.erasure_requested_at IS NOT NULL, FALSE),
			       COALESCE($2::timestamptz, clock_timestamp())
			FROM email_outbox o
			LEFT JOIN reservations r ON r.property_id = o.property_id AND r.id = o.reservation_id
			LEFT JOIN guests g ON g.property_id = o.property_id AND g.id = COALESCE(o.guest_id, r.guest_id)
			WHERE o.id = $1
			FOR UPDATE OF o
		`, id, nowPtr).Scan(&propID, &resID, &guestID, &commID, &status, &recEmail, &cancelReason, &rowToken, &rowGuestBlocked, &evalNow)
		if err != nil {
			return MapErr(err)
		}
		guestBlocked = guestBlocked || rowGuestBlocked
		if workerToken != nil && (rowToken == nil || *rowToken != *workerToken) {
			allowed = false
			return nil
		}

		// Verify reservation / communication / enquiry consistency within property scope.
		linkBroken := false
		if resID != nil {
			var resGuest *uuid.UUID
			if rErr := tx.QueryRow(ctx, `SELECT guest_id FROM reservations WHERE property_id = $1 AND id = $2`, propID, *resID).Scan(&resGuest); rErr != nil {
				linkBroken = true
			} else if guestID != nil && resGuest != nil && !s.sameCanonicalClusterTx(ctx, tx, propID, *guestID, *resGuest) {
				linkBroken = true
			}
		}
		if commID != nil && !linkBroken {
			var commGuest, commRes, commEnq *uuid.UUID
			if cErr := tx.QueryRow(ctx, `
				SELECT guest_id, reservation_id, enquiry_id
				FROM guest_communications
				WHERE property_id = $1 AND id = $2
			`, propID, *commID).Scan(&commGuest, &commRes, &commEnq); cErr != nil {
				linkBroken = true
			} else if commEnq != nil {
				var enqGuest, enqRes *uuid.UUID
				if eErr := tx.QueryRow(ctx, `SELECT guest_id, reservation_id FROM guest_enquiries WHERE property_id = $1 AND id = $2`, propID, *commEnq).Scan(&enqGuest, &enqRes); eErr != nil {
					linkBroken = true
				} else if commGuest != nil && enqGuest != nil && !s.sameCanonicalClusterTx(ctx, tx, propID, *commGuest, *enqGuest) {
					linkBroken = true
				}
			}
		}

		if status == domain.OutboxStatusCancelled || status == domain.OutboxStatusCancelRequested || guestBlocked || linkBroken || recEmail == "" || recEmail == "[redacted]" {
			if status != domain.OutboxStatusCancelled && status != domain.OutboxStatusSent && status != domain.OutboxStatusDispatching && status != domain.OutboxStatusDispatchUnknown {
				reason := cancelReason
				if linkBroken && reason == "" {
					reason = "Cancelled pre-dispatch: guest/reservation/enquiry link invalid"
				} else if reason == "" {
					reason = "Cancelled pre-dispatch: associated guest anonymised or erasure pending"
				}
				if _, uErr := tx.Exec(ctx, `
					UPDATE email_outbox
					SET status = 'CANCELLED',
					    recipient_email = '[redacted]',
					    subject = '[Anonymised Email]',
					    body_text = '[redacted by GDPR erasure]',
					    body_html = '',
					    last_error = CASE WHEN TRIM(last_error) = '' THEN '' ELSE '[redacted on anonymisation]' END,
					    cancel_reason = $2,
					    cancelled_at = COALESCE(cancelled_at, $3),
					    locked_at = NULL,
					    worker_token = NULL,
					    updated_at = NOW()
					WHERE id = $1
				`, id, reason, evalNow); uErr != nil {
					return MapErr(uErr)
				}
				if commID != nil {
					if err := s.recordCancelledCommunicationTx(ctx, tx, propID, id, *commID, reason, evalNow); err != nil {
						return err
					}
				}
			}
			allowed = false
			return nil
		}
		if status != domain.OutboxStatusProcessing {
			allowed = false
			return nil
		}
		if err := tx.QueryRow(ctx, `
			UPDATE email_outbox
			SET status = 'DISPATCHING',
			    dispatched_at = clk.ts,
			    dispatch_heartbeat_at = clk.ts,
			    dispatch_lease_expires_at = clk.ts + ($3::bigint * INTERVAL '1 microsecond'),
			    dispatch_fence_epoch = dispatch_fence_epoch + 1,
			    dispatch_fenced_at = NULL,
			    dispatch_outcome = '',
			    locked_at = clk.ts,
			    dispatch_worker_id = $4,
			    updated_at = NOW()
			FROM (SELECT COALESCE($2::timestamptz, clock_timestamp()) AS ts) clk
			WHERE id = $1
			  AND status = 'PROCESSING'
			RETURNING dispatch_fence_epoch
		`, id, nowPtr, leaseMicros, cleanWorker).Scan(&fenceEpoch); err != nil {
			return MapErr(err)
		}
		if commID != nil {
			if _, err := tx.Exec(ctx, `
				UPDATE guest_communications
				SET delivery_status = CASE WHEN delivery_status IN ('ACCEPTED', 'DELIVERED') THEN delivery_status ELSE 'SENDING' END,
				    updated_at = NOW()
				WHERE id = $1
			`, *commID); err != nil {
				return MapErr(err)
			}
		}
		allowed = true
		return nil
	})
	return allowed, fenceEpoch, err
}

// RenewOutboxDispatchHeartbeat extends the worker liveness lease for an active DISPATCHING
// row only if this exact worker instance, lease token, and fence epoch are still unfenced AND
// the existing dispatch_lease_expires_at is strictly in the future under the active clock authority.
// When nowUTC.IsZero(), PostgreSQL clock_timestamp() is used as the single clock authority so
// host clock skew cannot prematurely fail or illicitly extend a distributed lease.
// Returning renewed=false signals that the lease has expired or been fenced and must stop.
func (s *Store) RenewOutboxDispatchHeartbeat(
	ctx context.Context,
	id uuid.UUID,
	workerToken *uuid.UUID,
	workerID string,
	fenceEpoch int64,
	nowUTC time.Time,
	leaseTTL time.Duration,
) (bool, error) {
	if workerToken == nil || fenceEpoch <= 0 {
		return false, nil
	}
	cleanWorker := strings.TrimSpace(workerID)
	if cleanWorker == "" {
		return false, nil
	}
	if leaseTTL <= 0 {
		leaseTTL = 60 * time.Second
	}
	leaseMicros := leaseTTL.Microseconds()
	if leaseMicros < 1000 {
		leaseMicros = 1000
	}
	var nowPtr *time.Time
	if !nowUTC.IsZero() {
		u := nowUTC.UTC()
		nowPtr = &u
	}
	tag, err := s.pool.Exec(ctx, `
		WITH clk AS (
			SELECT COALESCE($5::timestamptz, clock_timestamp()) AS ts
		)
		UPDATE email_outbox
		SET dispatch_heartbeat_at = clk.ts,
		    dispatch_lease_expires_at = clk.ts + ($6::bigint * INTERVAL '1 microsecond'),
		    locked_at = clk.ts,
		    updated_at = NOW()
		FROM clk
		WHERE id = $1
		  AND status = 'DISPATCHING'
		  AND dispatch_fenced_at IS NULL
		  AND dispatch_fence_epoch = $2
		  AND worker_token = $3
		  AND dispatch_worker_id = $4
		  AND dispatch_lease_expires_at IS NOT NULL
		  AND dispatch_lease_expires_at > clk.ts
	`, id, fenceEpoch, *workerToken, cleanWorker, nowPtr, leaseMicros)
	if err != nil {
		return false, MapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) sameCanonicalClusterTx(ctx context.Context, tx pgx.Tx, propertyID, guestA, guestB uuid.UUID) bool {
	if guestA == guestB {
		return true
	}
	var cA, cB uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT public.resolve_canonical_guest_id($1, $2), public.resolve_canonical_guest_id($1, $3)`, propertyID, guestA, guestB).Scan(&cA, &cB); err != nil {
		return false
	}
	return cA == cB
}

func (s *Store) ListDueOutboxEmails(ctx context.Context, nowUTC time.Time, limit int) ([]domain.EmailOutboxItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.property_id, o.reservation_id, o.guest_id, o.communication_id,
		       o.recipient_email, o.template, o.subject, o.body_text, o.body_html,
		       o.status, o.attempts, o.last_error, o.next_attempt_at, o.sent_at, o.created_at
		FROM email_outbox o
		LEFT JOIN reservations r ON r.property_id = o.property_id AND r.id = o.reservation_id
		LEFT JOIN guests g ON g.property_id = o.property_id AND g.id = COALESCE(o.guest_id, r.guest_id)
		WHERE o.status = 'PENDING' AND o.next_attempt_at <= $1 AND o.attempts < 5
		  AND (g.id IS NULL OR (g.status <> 'ANONYMIZED' AND g.anonymized_at IS NULL))
		  AND o.recipient_email NOT IN ('', '[redacted]')
		ORDER BY o.created_at ASC
		LIMIT $2
	`, nowUTC, limit)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.EmailOutboxItem
	for rows.Next() {
		var e domain.EmailOutboxItem
		if err := rows.Scan(
			&e.ID, &e.PropertyID, &e.ReservationID, &e.GuestID, &e.CommunicationID,
			&e.RecipientEmail, &e.Template, &e.Subject, &e.BodyText, &e.BodyHTML,
			&e.Status, &e.Attempts, &e.LastError, &e.NextAttemptAt, &e.SentAt, &e.CreatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, e)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) MarkOutboxEmailsProviderUnconfigured(ctx context.Context, nowUTC time.Time, limit int, reason string) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tag, err := s.pool.Exec(ctx, `
		WITH pending_due AS (
			SELECT o.id
			FROM email_outbox o
			LEFT JOIN reservations r ON r.property_id = o.property_id AND r.id = o.reservation_id
			LEFT JOIN guests g ON g.property_id = o.property_id AND g.id = COALESCE(o.guest_id, r.guest_id)
			WHERE o.status = 'PENDING' AND o.next_attempt_at <= $1 AND o.attempts < 5
			  AND (g.id IS NULL OR (g.status <> 'ANONYMIZED' AND g.anonymized_at IS NULL AND g.erasure_requested_at IS NULL))
			ORDER BY o.created_at ASC
			LIMIT $2
		)
		UPDATE email_outbox o
		SET last_error = $3,
		    locked_at = NULL,
		    updated_at = NOW()
		FROM pending_due
		WHERE o.id = pending_due.id
	`, nowUTC, limit, strings.TrimSpace(reason))
	if err != nil {
		return 0, MapErr(err)
	}
	return int(tag.RowsAffected()), nil
}

// lockGuestForOutboxTx locks the canonical guest cluster in ascending UUID order BEFORE locking email_outbox
// so outbox completion/dispatch workers and guest anonymisation never deadlock on opposite lock orders.
func (s *Store) lockGuestForOutboxTx(ctx context.Context, tx pgx.Tx, outboxID uuid.UUID) (bool, error) {
	var probePropID uuid.UUID
	var probeGuestID, probeResID *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT o.property_id, o.guest_id, o.reservation_id
		FROM email_outbox o
		WHERE o.id = $1
	`, outboxID).Scan(&probePropID, &probeGuestID, &probeResID); err != nil {
		return false, MapErr(err)
	}
	targetGuestID := uuid.Nil
	if probeGuestID != nil {
		targetGuestID = *probeGuestID
	} else if probeResID != nil {
		var rGuest *uuid.UUID
		_ = tx.QueryRow(ctx, `SELECT guest_id FROM reservations WHERE property_id = $1 AND id = $2`, probePropID, *probeResID).Scan(&rGuest)
		if rGuest != nil {
			targetGuestID = *rGuest
		}
	}
	if targetGuestID == uuid.Nil {
		return false, nil
	}
	blocked, err := s.lockGuestClusterAndCheckErasureDB(ctx, tx, probePropID, targetGuestID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return false, err
	}
	return blocked, nil
}

func (s *Store) MarkOutboxEmailSent(ctx context.Context, id uuid.UUID, sentAt time.Time) error {
	return s.MarkOutboxEmailSentWithLease(ctx, id, nil, 0, sentAt)
}

// MarkOutboxEmailSentWithToken transitions an active PROCESSING/DISPATCHING row to SENT cleanly.
func (s *Store) MarkOutboxEmailSentWithToken(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, sentAt time.Time) error {
	return s.MarkOutboxEmailSentWithLease(ctx, id, workerToken, 0, sentAt)
}

type outboxLeaseSnapshot struct {
	PropertyID      uuid.UUID
	ReservationID   *uuid.UUID
	GuestID         *uuid.UUID
	CommunicationID *uuid.UUID
	WorkerToken     *uuid.UUID
	Status          string
	CancelReason    string
	Attempts        int
	RecipientEmail  string
	Template        string
	Subject         string
	BodyText        string
	BodyHTML        string
	FenceEpoch      int64
	FencedAt        *time.Time
	LeaseExpiresAt  *time.Time
	GuestBlocked    bool
	EvalNow         time.Time
}

// lockAndVerifyOutboxLeaseTx acquires the canonical guest lock first, locks the email_outbox row
// FOR UPDATE, and verifies that the caller holds the exact unfenced worker token, fence epoch,
// and an unexpired dispatch lease (for DISPATCHING rows) under the unified clock authority.
func (s *Store) lockAndVerifyOutboxLeaseTx(
	ctx context.Context,
	tx pgx.Tx,
	id uuid.UUID,
	workerToken *uuid.UUID,
	fenceEpoch int64,
	nowUTC time.Time,
) (outboxLeaseSnapshot, error) {
	guestBlocked, lockErr := s.lockGuestForOutboxTx(ctx, tx, id)
	if lockErr != nil {
		return outboxLeaseSnapshot{}, lockErr
	}
	var nowPtr *time.Time
	if !nowUTC.IsZero() {
		u := nowUTC.UTC()
		nowPtr = &u
	}
	var snap outboxLeaseSnapshot
	var rowGuestBlocked bool
	err := tx.QueryRow(ctx, `
		SELECT o.property_id, o.reservation_id, o.guest_id, o.communication_id, o.worker_token,
		       o.status, o.cancel_reason, o.attempts, o.recipient_email, o.template, o.subject, o.body_text, o.body_html,
		       o.dispatch_fence_epoch, o.dispatch_fenced_at, o.dispatch_lease_expires_at,
		       COALESCE(g.status = 'ANONYMIZED' OR g.anonymized_at IS NOT NULL OR g.erasure_requested_at IS NOT NULL, FALSE),
		       COALESCE($2::timestamptz, clock_timestamp())
		FROM email_outbox o
		LEFT JOIN reservations r ON r.property_id = o.property_id AND r.id = o.reservation_id
		LEFT JOIN guests g ON g.property_id = o.property_id AND g.id = COALESCE(o.guest_id, r.guest_id)
		WHERE o.id = $1
		FOR UPDATE OF o
	`, id, nowPtr).Scan(
		&snap.PropertyID, &snap.ReservationID, &snap.GuestID, &snap.CommunicationID, &snap.WorkerToken,
		&snap.Status, &snap.CancelReason, &snap.Attempts, &snap.RecipientEmail, &snap.Template, &snap.Subject, &snap.BodyText, &snap.BodyHTML,
		&snap.FenceEpoch, &snap.FencedAt, &snap.LeaseExpiresAt,
		&rowGuestBlocked, &snap.EvalNow,
	)
	if err != nil {
		return outboxLeaseSnapshot{}, MapErr(err)
	}
	snap.GuestBlocked = guestBlocked || rowGuestBlocked

	if snap.FencedAt != nil {
		return snap, fmt.Errorf("%w: outbox dispatch %s was already fenced at %s", domain.ErrConflict, id, snap.FencedAt.Format(time.RFC3339))
	}
	if workerToken != nil && (snap.WorkerToken == nil || *snap.WorkerToken != *workerToken) {
		return snap, fmt.Errorf("%w: stale outbox lease token for email %s", domain.ErrConflict, id)
	}
	if fenceEpoch > 0 && snap.FenceEpoch != fenceEpoch {
		return snap, fmt.Errorf("%w: stale outbox fence epoch (%d != %d) for email %s", domain.ErrConflict, fenceEpoch, snap.FenceEpoch, id)
	}
	if snap.Status == domain.OutboxStatusDispatching && (snap.LeaseExpiresAt == nil || !snap.LeaseExpiresAt.After(snap.EvalNow)) {
		return snap, fmt.Errorf("%w: dispatch lease for outbox email %s has expired; worker must not settle or overwrite row", domain.ErrConflict, id)
	}
	return snap, nil
}

// MarkOutboxEmailSentWithLease transitions an unfenced, unexpired DISPATCHING/PROCESSING (or concurrently
// CANCEL_REQUESTED whose provider send succeeded) email to SENT cleanly.
func (s *Store) MarkOutboxEmailSentWithLease(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, fenceEpoch int64, sentAt time.Time) error {
	return s.MarkOutboxEmailSentWithLeaseAt(ctx, id, workerToken, fenceEpoch, sentAt, sentAt)
}

// MarkOutboxEmailSentWithLeaseAt behaves like MarkOutboxEmailSentWithLease with an explicit lease clock parameter
// (time.Time{} evaluates PostgreSQL clock_timestamp() as the single clock authority).
func (s *Store) MarkOutboxEmailSentWithLeaseAt(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, fenceEpoch int64, sentAt time.Time, nowUTC time.Time) error {
	return s.WithTx(ctx, func(tx pgx.Tx) error {
		snap, err := s.lockAndVerifyOutboxLeaseTx(ctx, tx, id, workerToken, fenceEpoch, nowUTC)
		if err != nil {
			return err
		}
		if snap.Status == domain.OutboxStatusCancelled || snap.Status == domain.OutboxStatusSent || snap.Status == domain.OutboxStatusFailed || snap.Status == domain.OutboxStatusDispatchUnknown {
			return fmt.Errorf("%w: cannot mark outbox email %s in status %s as SENT", domain.ErrConflict, id, snap.Status)
		}
		effectiveSentAt := sentAt.UTC()
		if sentAt.IsZero() {
			effectiveSentAt = snap.EvalNow.UTC()
		}
		propID := snap.PropertyID
		resID := snap.ReservationID
		guestID := snap.GuestID
		commID := snap.CommunicationID
		recipient := snap.RecipientEmail
		tpl := snap.Template
		subj := snap.Subject
		bodyText := snap.BodyText
		bodyHTML := snap.BodyHTML

		concurrentAnon := snap.Status == domain.OutboxStatusCancelRequested || snap.GuestBlocked
		if concurrentAnon {
			if _, err := tx.Exec(ctx, `
				UPDATE email_outbox
				SET status = 'SENT',
				    attempts = attempts + 1,
				    sent_at = $2,
				    dispatch_outcome = 'PROVIDER_ACCEPTED',
				    dispatch_fenced_at = $2,
				    dispatch_fence_epoch = dispatch_fence_epoch + 1,
				    dispatch_lease_expires_at = NULL,
				    last_error = 'Delivered to transport before concurrent GDPR cancellation finalised',
				    recipient_email = '[redacted]',
				    subject = '[Anonymised Email]',
				    body_text = '[redacted by GDPR erasure]',
				    body_html = '',
				    locked_at = NULL,
				    worker_token = NULL,
				    updated_at = NOW()
				WHERE id = $1
			`, id, effectiveSentAt); err != nil {
				return MapErr(err)
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE email_outbox
				SET status = 'SENT',
				    attempts = attempts + 1,
				    sent_at = $2,
				    dispatch_outcome = 'PROVIDER_ACCEPTED',
				    dispatch_fenced_at = $2,
				    dispatch_fence_epoch = dispatch_fence_epoch + 1,
				    dispatch_lease_expires_at = NULL,
				    last_error = '',
				    locked_at = NULL,
				    worker_token = NULL,
				    updated_at = NOW()
				WHERE id = $1
			`, id, effectiveSentAt); err != nil {
				return MapErr(err)
			}
		}

		// Ensure Phase 2 booking confirmations / direct outbox emails appear on the guest timeline cleanly.
		if commID == nil {
			if guestID == nil && resID != nil {
				var g uuid.UUID
				if gErr := tx.QueryRow(ctx, `SELECT guest_id FROM reservations WHERE property_id = $1 AND id = $2`, propID, *resID).Scan(&g); gErr == nil {
					guestID = &g
				}
			}
			newCommID, err := NewUUID()
			if err != nil {
				return err
			}
			intType := string(domain.InteractionEmailOutbound)
			if strings.EqualFold(tpl, "booking_confirmation") {
				intType = string(domain.InteractionBookingNotice)
			}
			commSubj, commBodyText, commBodyHTML, commRecip := subj, bodyText, bodyHTML, recipient
			commSummary := "Automated email dispatched (" + tpl + ")"
			if concurrentAnon {
				commSubj = "[Anonymised Communication]"
				commBodyText = "[redacted by GDPR erasure]"
				commBodyHTML = ""
				commRecip = "[redacted]"
				commSummary = "Email accepted by transport concurrently with guest anonymisation"
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO guest_communications (
					id, property_id, guest_id, reservation_id, interaction_type, direction,
					subject, body_text, body_html, summary, recipient_email,
					email_outbox_id, delivery_status, occurred_at
				) VALUES (
					$1, $2, $3, $4, $5, 'OUTBOUND',
					$6, $7, $8, $9, $10,
					$11, 'ACCEPTED', $12
				)
			`, newCommID, propID, guestID, resID, intType,
				commSubj, commBodyText, commBodyHTML, commSummary, commRecip,
				id, effectiveSentAt,
			)
			if err != nil {
				return MapErr(err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE email_outbox SET communication_id = $2, guest_id = COALESCE(guest_id, $3) WHERE id = $1
			`, id, newCommID, guestID); err != nil {
				return MapErr(err)
			}
			commID = &newCommID
		} else {
			if concurrentAnon {
				if _, err := tx.Exec(ctx, `
					UPDATE guest_communications
					SET delivery_status = 'ACCEPTED',
					    subject = '[Anonymised Communication]',
					    body_text = '[redacted by GDPR erasure]',
					    body_html = '',
					    recipient_email = '[redacted]',
					    summary = 'Email accepted by transport concurrently with guest anonymisation',
					    updated_at = NOW()
					WHERE id = $1
				`, *commID); err != nil {
					return MapErr(err)
				}
			} else {
				if _, err := tx.Exec(ctx, `
					UPDATE guest_communications
					SET delivery_status = 'ACCEPTED', updated_at = NOW()
					WHERE id = $1 AND delivery_status IN ('DRAFT', 'QUEUED', 'SENDING', 'FAILED', 'CANCELLED')
				`, *commID); err != nil {
					return MapErr(err)
				}
			}
		}

		evID, err := NewUUID()
		if err != nil {
			return err
		}
		detail := "Accepted by configured mail transport (awaiting provider delivery confirmation)"
		if concurrentAnon {
			detail = "Accepted by mail transport before concurrent anonymisation cancellation completed (not recalled; recipient content redacted)"
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO communication_delivery_events (
				id, property_id, communication_id, email_outbox_id, event_type, provider, detail, occurred_at
			) VALUES ($1, $2, $3, $4, 'ACCEPTED', 'smtp', $5, $6)
		`, evID, propID, *commID, id, detail, effectiveSentAt)
		return MapErr(err)
	})
}

// MarkOutboxDispatchUnknownWithLease transitions an unfenced, unexpired DISPATCHING item into
// DISPATCH_UNKNOWN after a transport timeout or interruption where external delivery outcome
// could not be verified.
func (s *Store) MarkOutboxDispatchUnknownWithLease(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, fenceEpoch int64, errMsg string, at time.Time) error {
	return s.MarkOutboxDispatchUnknownWithLeaseAt(ctx, id, workerToken, fenceEpoch, errMsg, at, at)
}

// MarkOutboxDispatchUnknownWithLeaseAt behaves like MarkOutboxDispatchUnknownWithLease with an
// explicit lease clock parameter (time.Time{} uses PostgreSQL clock_timestamp()).
func (s *Store) MarkOutboxDispatchUnknownWithLeaseAt(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, fenceEpoch int64, errMsg string, at time.Time, nowUTC time.Time) error {
	return s.WithTx(ctx, func(tx pgx.Tx) error {
		snap, err := s.lockAndVerifyOutboxLeaseTx(ctx, tx, id, workerToken, fenceEpoch, nowUTC)
		if err != nil {
			return err
		}
		if snap.Status != domain.OutboxStatusDispatching && snap.Status != domain.OutboxStatusCancelRequested {
			return fmt.Errorf("%w: cannot mark outbox %s in status %s as DISPATCH_UNKNOWN", domain.ErrConflict, id, snap.Status)
		}
		effectiveAt := at.UTC()
		if at.IsZero() {
			effectiveAt = snap.EvalNow.UTC()
		}
		safeErr := "Transport timeout/interruption during external submission; outcome uncertain pending reconciliation"
		if snap.GuestBlocked || snap.Status == domain.OutboxStatusCancelRequested {
			safeErr = sanitizeOutboxErrorForAnon(errMsg, false)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE email_outbox
			SET status = 'DISPATCH_UNKNOWN',
			    attempts = attempts + 1,
			    dispatch_outcome = 'UNCERTAIN_TIMEOUT',
			    dispatch_fenced_at = $2,
			    dispatch_fence_epoch = dispatch_fence_epoch + 1,
			    dispatch_lease_expires_at = NULL,
			    last_error = $3,
			    locked_at = NULL,
			    worker_token = NULL,
			    updated_at = NOW()
			WHERE id = $1
		`, id, effectiveAt, safeErr); err != nil {
			return MapErr(err)
		}
		if snap.CommunicationID != nil {
			evID, err := NewUUID()
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO communication_delivery_events (
					id, property_id, communication_id, email_outbox_id, event_type, provider, detail, occurred_at
				) VALUES ($1, $2, $3, $4, 'DISPATCH_UNKNOWN', 'smtp', 'SMTP transport timed out or disconnected after submission; operator reconciliation required', $5)
			`, evID, snap.PropertyID, *snap.CommunicationID, id, effectiveAt); err != nil {
				return MapErr(err)
			}
		}
		return nil
	})
}

// MarkOutboxDispatchTerminatedBeforeSendWithLease settles an unfenced, unexpired DISPATCHING item
// whose transport aborted cleanly BEFORE external submission (DATA).
func (s *Store) MarkOutboxDispatchTerminatedBeforeSendWithLease(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, fenceEpoch int64, reason string, nextRetry time.Time, at time.Time) error {
	return s.MarkOutboxDispatchTerminatedBeforeSendWithLeaseAt(ctx, id, workerToken, fenceEpoch, reason, nextRetry, at, at)
}

// MarkOutboxDispatchTerminatedBeforeSendWithLeaseAt behaves like MarkOutboxDispatchTerminatedBeforeSendWithLease
// with an explicit lease clock parameter (time.Time{} uses PostgreSQL clock_timestamp()).
func (s *Store) MarkOutboxDispatchTerminatedBeforeSendWithLeaseAt(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, fenceEpoch int64, reason string, nextRetry time.Time, at time.Time, nowUTC time.Time) error {
	return s.WithTx(ctx, func(tx pgx.Tx) error {
		snap, err := s.lockAndVerifyOutboxLeaseTx(ctx, tx, id, workerToken, fenceEpoch, nowUTC)
		if err != nil {
			return err
		}
		if snap.Status == domain.OutboxStatusDispatchUnknown || snap.Status == domain.OutboxStatusSent || snap.Status == domain.OutboxStatusCancelled {
			return fmt.Errorf("%w: cannot terminate outbox %s in status %s before send", domain.ErrConflict, id, snap.Status)
		}
		effectiveAt := at.UTC()
		if at.IsZero() {
			effectiveAt = snap.EvalNow.UTC()
		}
		effectiveRetry := nextRetry.UTC()
		if nextRetry.IsZero() {
			effectiveRetry = effectiveAt.Add(2 * time.Minute)
		}
		if snap.Status == domain.OutboxStatusCancelRequested || snap.GuestBlocked {
			cReason := snap.CancelReason
			if cReason == "" {
				cReason = "Aborted prior to SMTP submission due to guest anonymisation"
			}
			if _, err := tx.Exec(ctx, `
				UPDATE email_outbox
				SET status = 'CANCELLED',
				    attempts = attempts + 1,
				    dispatch_outcome = 'TERMINATED_BEFORE_SEND',
				    dispatch_fenced_at = $2,
				    dispatch_fence_epoch = dispatch_fence_epoch + 1,
				    dispatch_lease_expires_at = NULL,
				    last_error = '[redacted on anonymisation: aborted prior to submission]',
				    recipient_email = '[redacted]',
				    subject = '[Anonymised Email]',
				    body_text = '[redacted by GDPR erasure]',
				    body_html = '',
				    cancel_reason = $3,
				    cancelled_at = COALESCE(cancelled_at, $2),
				    locked_at = NULL,
				    worker_token = NULL,
				    updated_at = NOW()
				WHERE id = $1
			`, id, effectiveAt, cReason); err != nil {
				return MapErr(err)
			}
			if snap.CommunicationID != nil {
				return s.recordCancelledCommunicationTx(ctx, tx, snap.PropertyID, id, *snap.CommunicationID, cReason, effectiveAt)
			}
			return nil
		}

		nextStatus := domain.OutboxStatusPending
		outcome := ""
		var fencedAt *time.Time
		if snap.Attempts+1 >= 5 {
			nextStatus = domain.OutboxStatusFailed
			outcome = "TERMINATED_BEFORE_SEND"
			fencedAt = &effectiveAt
		}
		if _, err := tx.Exec(ctx, `
			UPDATE email_outbox
			SET attempts = attempts + 1,
			    last_error = $2,
			    next_attempt_at = $3,
			    status = $4,
			    dispatch_outcome = $5,
			    dispatch_fenced_at = $6,
			    dispatch_fence_epoch = dispatch_fence_epoch + 1,
			    dispatch_lease_expires_at = NULL,
			    locked_at = NULL,
			    worker_token = NULL,
			    updated_at = NOW()
			WHERE id = $1
		`, id, reason, effectiveRetry, nextStatus, outcome, fencedAt); err != nil {
			return MapErr(err)
		}
		if snap.CommunicationID != nil {
			nextCommStatus := "QUEUED"
			evType := "RETRY_SCHEDULED"
			if nextStatus == domain.OutboxStatusFailed {
				nextCommStatus = "FAILED"
				evType = "FAILED"
			}
			if _, err := tx.Exec(ctx, `UPDATE guest_communications SET delivery_status = $2, updated_at = NOW() WHERE id = $1`, *snap.CommunicationID, nextCommStatus); err != nil {
				return MapErr(err)
			}
			evID, err := NewUUID()
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO communication_delivery_events (
					id, property_id, communication_id, email_outbox_id, event_type, provider, detail, occurred_at
				) VALUES ($1, $2, $3, $4, $5, 'smtp', 'Dispatch aborted before DATA submission', $6)
			`, evID, snap.PropertyID, *snap.CommunicationID, id, evType, effectiveAt); err != nil {
				return MapErr(err)
			}
		}
		return nil
	})
}

func (s *Store) MarkOutboxEmailRetry(ctx context.Context, id uuid.UUID, errMsg string, nextRetry time.Time) error {
	return s.MarkOutboxEmailRetryWithLease(ctx, id, nil, 0, errMsg, nextRetry)
}

func (s *Store) MarkOutboxEmailRetryWithToken(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, errMsg string, nextRetry time.Time) error {
	return s.MarkOutboxEmailRetryWithLease(ctx, id, workerToken, 0, errMsg, nextRetry)
}

func (s *Store) MarkOutboxEmailRetryWithLease(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, fenceEpoch int64, errMsg string, nextRetry time.Time) error {
	return s.MarkOutboxEmailRetryWithLeaseAt(ctx, id, workerToken, fenceEpoch, errMsg, nextRetry, time.Time{})
}

func (s *Store) MarkOutboxEmailRetryWithLeaseAt(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, fenceEpoch int64, errMsg string, nextRetry time.Time, nowUTC time.Time) error {
	return s.WithTx(ctx, func(tx pgx.Tx) error {
		snap, err := s.lockAndVerifyOutboxLeaseTx(ctx, tx, id, workerToken, fenceEpoch, nowUTC)
		if err != nil {
			return err
		}
		if snap.Status == domain.OutboxStatusDispatchUnknown {
			return fmt.Errorf("%w: outbox email %s is in DISPATCH_UNKNOWN; use reconciliation instead of retry", domain.ErrConflict, id)
		}
		if snap.Status == domain.OutboxStatusCancelled || snap.Status == domain.OutboxStatusSent {
			return nil
		}
		effectiveNow := snap.EvalNow.UTC()
		if snap.Status == domain.OutboxStatusCancelRequested || snap.GuestBlocked {
			reason := snap.CancelReason
			if reason == "" {
				reason = "Cancelled after send error during guest anonymisation"
			}
			safeErr := sanitizeOutboxErrorForAnon(errMsg, false)
			if _, err := tx.Exec(ctx, `
				UPDATE email_outbox
				SET status = 'CANCELLED',
				    attempts = attempts + 1,
				    dispatch_outcome = 'PRE_SUBMIT_FAILED',
				    dispatch_fenced_at = $2,
				    dispatch_fence_epoch = dispatch_fence_epoch + 1,
				    dispatch_lease_expires_at = NULL,
				    last_error = $3,
				    recipient_email = '[redacted]',
				    subject = '[Anonymised Email]',
				    body_text = '[redacted by GDPR erasure]',
				    body_html = '',
				    cancel_reason = $4,
				    cancelled_at = COALESCE(cancelled_at, $2),
				    locked_at = NULL,
				    worker_token = NULL,
				    updated_at = NOW()
				WHERE id = $1
			`, id, effectiveNow, safeErr, reason); err != nil {
				return MapErr(err)
			}
			if snap.CommunicationID != nil {
				return s.recordCancelledCommunicationTx(ctx, tx, snap.PropertyID, id, *snap.CommunicationID, reason, effectiveNow)
			}
			return nil
		}

		nextStatus := domain.OutboxStatusPending
		outcome := ""
		var fencedAt *time.Time
		if snap.Attempts+1 >= 5 {
			nextStatus = domain.OutboxStatusFailed
			outcome = "PRE_SUBMIT_FAILED"
			fencedAt = &effectiveNow
		}
		effectiveRetry := nextRetry.UTC()
		if nextRetry.IsZero() {
			effectiveRetry = effectiveNow.Add(2 * time.Minute)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE email_outbox
			SET attempts = attempts + 1,
			    last_error = $2,
			    next_attempt_at = $3,
			    locked_at = NULL,
			    worker_token = NULL,
			    status = $4,
			    dispatch_outcome = $5,
			    dispatch_fenced_at = $6,
			    dispatch_fence_epoch = dispatch_fence_epoch + 1,
			    dispatch_lease_expires_at = NULL,
			    updated_at = NOW()
			WHERE id = $1
		`, id, errMsg, effectiveRetry, nextStatus, outcome, fencedAt); err != nil {
			return MapErr(err)
		}
		if snap.CommunicationID != nil {
			nextCommStatus := "QUEUED"
			evType := "RETRY_SCHEDULED"
			if nextStatus == domain.OutboxStatusFailed {
				nextCommStatus = "FAILED"
				evType = "FAILED"
			}
			if _, err := tx.Exec(ctx, `
				UPDATE guest_communications SET delivery_status = $2, updated_at = NOW() WHERE id = $1
			`, *snap.CommunicationID, nextCommStatus); err != nil {
				return MapErr(err)
			}
			evID, err := NewUUID()
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO communication_delivery_events (
					id, property_id, communication_id, email_outbox_id, event_type, provider, detail, occurred_at
				) VALUES ($1, $2, $3, $4, $5, 'smtp', $6, $7)
			`, evID, snap.PropertyID, *snap.CommunicationID, id, evType, errMsg, effectiveNow); err != nil {
				return MapErr(err)
			}
		}
		return nil
	})
}

func (s *Store) MarkOutboxEmailPermanentFailure(ctx context.Context, id uuid.UUID, errMsg string) error {
	return s.MarkOutboxEmailPermanentFailureWithLease(ctx, id, nil, 0, errMsg)
}

func (s *Store) MarkOutboxEmailPermanentFailureWithToken(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, errMsg string) error {
	return s.MarkOutboxEmailPermanentFailureWithLease(ctx, id, workerToken, 0, errMsg)
}

func (s *Store) MarkOutboxEmailPermanentFailureWithLease(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, fenceEpoch int64, errMsg string) error {
	return s.MarkOutboxEmailPermanentFailureWithLeaseAt(ctx, id, workerToken, fenceEpoch, errMsg, time.Time{})
}

func (s *Store) MarkOutboxEmailPermanentFailureWithLeaseAt(ctx context.Context, id uuid.UUID, workerToken *uuid.UUID, fenceEpoch int64, errMsg string, nowUTC time.Time) error {
	return s.WithTx(ctx, func(tx pgx.Tx) error {
		snap, err := s.lockAndVerifyOutboxLeaseTx(ctx, tx, id, workerToken, fenceEpoch, nowUTC)
		if err != nil {
			return err
		}
		if snap.Status == domain.OutboxStatusDispatchUnknown {
			return fmt.Errorf("%w: outbox email %s is in DISPATCH_UNKNOWN; use reconciliation instead of permanent failure", domain.ErrConflict, id)
		}
		if snap.Status == domain.OutboxStatusCancelled || snap.Status == domain.OutboxStatusSent {
			return nil
		}
		effectiveNow := snap.EvalNow.UTC()
		if snap.Status == domain.OutboxStatusCancelRequested || snap.GuestBlocked {
			reason := snap.CancelReason
			if reason == "" {
				reason = "Cancelled after permanent failure during guest anonymisation"
			}
			safeErr := sanitizeOutboxErrorForAnon(errMsg, true)
			if _, err := tx.Exec(ctx, `
				UPDATE email_outbox
				SET status = 'CANCELLED',
				    attempts = attempts + 1,
				    dispatch_outcome = 'PRE_SUBMIT_FAILED',
				    dispatch_fenced_at = $2,
				    dispatch_fence_epoch = dispatch_fence_epoch + 1,
				    dispatch_lease_expires_at = NULL,
				    last_error = $3,
				    recipient_email = '[redacted]',
				    subject = '[Anonymised Email]',
				    body_text = '[redacted by GDPR erasure]',
				    body_html = '',
				    cancel_reason = $4,
				    cancelled_at = COALESCE(cancelled_at, $2),
				    locked_at = NULL,
				    worker_token = NULL,
				    updated_at = NOW()
				WHERE id = $1
			`, id, effectiveNow, safeErr, reason); err != nil {
				return MapErr(err)
			}
			if snap.CommunicationID != nil {
				return s.recordCancelledCommunicationTx(ctx, tx, snap.PropertyID, id, *snap.CommunicationID, reason, effectiveNow)
			}
			return nil
		}

		if _, err := tx.Exec(ctx, `
			UPDATE email_outbox
			SET attempts = attempts + 1,
			    last_error = $2,
			    locked_at = NULL,
			    worker_token = NULL,
			    status = 'FAILED',
			    dispatch_outcome = 'PRE_SUBMIT_FAILED',
			    dispatch_fenced_at = $3,
			    dispatch_fence_epoch = dispatch_fence_epoch + 1,
			    dispatch_lease_expires_at = NULL,
			    updated_at = NOW()
			WHERE id = $1
		`, id, errMsg, effectiveNow); err != nil {
			return MapErr(err)
		}
		if snap.CommunicationID != nil {
			if _, err := tx.Exec(ctx, `
				UPDATE guest_communications SET delivery_status = 'FAILED', updated_at = NOW() WHERE id = $1
			`, *snap.CommunicationID); err != nil {
				return MapErr(err)
			}
			evID, err := NewUUID()
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO communication_delivery_events (
					id, property_id, communication_id, email_outbox_id, event_type, provider, detail, occurred_at
				) VALUES ($1, $2, $3, $4, 'FAILED', 'smtp', $5, $6)
			`, evID, snap.PropertyID, *snap.CommunicationID, id, errMsg, effectiveNow); err != nil {
				return MapErr(err)
			}
		}
		return nil
	})
}

// ReconcileOutboxParams captures validated, evidence-backed operator reconciliation inputs.
type ReconcileOutboxParams struct {
	PropertyID   uuid.UUID
	OutboxID     uuid.UUID
	ActorID      uuid.UUID
	Resolution   string // CONFIRMED_SENT | CONFIRMED_FAILED | CANCEL
	ReasonCode   string
	EvidenceType string // MTA_REJECTED | PROVIDER_WEBHOOK_FAILED | PROVIDER_NOT_SUBMITTED | MTA_ACCEPTED | PROVIDER_WEBHOOK_ACCEPTED | CONSERVATIVE_ASSUME_SENT
	EvidenceRef  string
	Note         string
	At           time.Time
}

// ReconcileUnknownOutboxDispatch allows an authorised, active operator to resolve an outbox item
// left in DISPATCH_UNKNOWN only after concrete external evidence has been verified and no worker
// dispatch lease remains live. Refuses CANCEL/CONFIRMED_FAILED without failure-proving evidence.
func (s *Store) ReconcileUnknownOutboxDispatch(ctx context.Context, tx pgx.Tx, params ReconcileOutboxParams) (domain.EmailOutboxItem, error) {
	if params.ActorID == uuid.Nil {
		return domain.EmailOutboxItem{}, domain.ErrForbidden
	}
	var actorActive, actorMustRotate bool
	if err := tx.QueryRow(ctx, `SELECT is_active, must_rotate_password FROM admin_users WHERE id = $1`, params.ActorID).Scan(&actorActive, &actorMustRotate); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.EmailOutboxItem{}, domain.ErrForbidden
		}
		return domain.EmailOutboxItem{}, MapErr(err)
	}
	if !actorActive || actorMustRotate {
		return domain.EmailOutboxItem{}, domain.ErrForbidden
	}

	decision := strings.ToUpper(strings.TrimSpace(params.Resolution))
	evType := strings.ToUpper(strings.TrimSpace(params.EvidenceType))
	cleanReasonCode, err := domain.ValidateReasonCode("reason_code", params.ReasonCode)
	if err != nil {
		return domain.EmailOutboxItem{}, err
	}
	cleanEvRef, err := domain.ValidateNonPIIText("evidence_ref", params.EvidenceRef, 3, 160)
	if err != nil {
		return domain.EmailOutboxItem{}, err
	}
	cleanNote, err := domain.ValidateNonPIIText("note", params.Note, 5, 240)
	if err != nil {
		return domain.EmailOutboxItem{}, err
	}

	switch decision {
	case "CONFIRMED_SENT":
		switch evType {
		case "MTA_ACCEPTED", "PROVIDER_WEBHOOK_ACCEPTED", "CONSERVATIVE_ASSUME_SENT":
		default:
			return domain.EmailOutboxItem{}, domain.NewValidationError(map[string]string{
				"evidence_type": "CONFIRMED_SENT requires MTA_ACCEPTED, PROVIDER_WEBHOOK_ACCEPTED, or CONSERVATIVE_ASSUME_SENT",
			})
		}
	case "CONFIRMED_FAILED", "CANCEL":
		switch evType {
		case "MTA_REJECTED", "PROVIDER_WEBHOOK_FAILED", "PROVIDER_NOT_SUBMITTED":
		default:
			return domain.EmailOutboxItem{}, domain.NewValidationError(map[string]string{
				"evidence_type": "CONFIRMED_FAILED and CANCEL require external proof (MTA_REJECTED, PROVIDER_WEBHOOK_FAILED, or PROVIDER_NOT_SUBMITTED)",
			})
		}
	default:
		return domain.EmailOutboxItem{}, domain.NewValidationError(map[string]string{
			"resolution": "must be CONFIRMED_SENT, CONFIRMED_FAILED, or CANCEL",
		})
	}

	if _, lockErr := s.lockGuestForOutboxTx(ctx, tx, params.OutboxID); lockErr != nil {
		return domain.EmailOutboxItem{}, lockErr
	}
	var atPtr *time.Time
	if !params.At.IsZero() {
		u := params.At.UTC()
		atPtr = &u
	}
	var res domain.EmailOutboxItem
	var rowGuestBlocked bool
	var evalNow time.Time
	err = tx.QueryRow(ctx, `
		SELECT o.id, o.property_id, o.reservation_id, o.guest_id, o.communication_id,
		       o.recipient_email, o.template, o.subject, o.body_text, o.body_html,
		       o.status, o.attempts, o.last_error, o.cancel_reason, o.cancelled_at,
		       o.dispatched_at, o.dispatch_worker_id, o.dispatch_lease_expires_at,
		       o.dispatch_heartbeat_at, o.dispatch_fence_epoch, o.dispatch_fenced_at, o.dispatch_outcome,
		       o.next_attempt_at, o.sent_at, o.created_at,
		       COALESCE(g.status = 'ANONYMIZED' OR g.anonymized_at IS NOT NULL OR g.erasure_requested_at IS NOT NULL, FALSE),
		       COALESCE($3::timestamptz, clock_timestamp())
		FROM email_outbox o
		LEFT JOIN reservations r ON r.property_id = o.property_id AND r.id = o.reservation_id
		LEFT JOIN guests g ON g.property_id = o.property_id AND g.id = COALESCE(o.guest_id, r.guest_id)
		WHERE o.property_id = $1 AND o.id = $2
		FOR UPDATE OF o
	`, params.PropertyID, params.OutboxID, atPtr).Scan(
		&res.ID, &res.PropertyID, &res.ReservationID, &res.GuestID, &res.CommunicationID,
		&res.RecipientEmail, &res.Template, &res.Subject, &res.BodyText, &res.BodyHTML,
		&res.Status, &res.Attempts, &res.LastError, &res.CancelReason, &res.CancelledAt,
		&res.DispatchedAt, &res.DispatchWorkerID, &res.DispatchLeaseExpires,
		&res.DispatchHeartbeatAt, &res.DispatchFenceEpoch, &res.DispatchFencedAt, &res.DispatchOutcome,
		&res.NextAttemptAt, &res.SentAt, &res.CreatedAt,
		&rowGuestBlocked, &evalNow,
	)
	if err != nil {
		return domain.EmailOutboxItem{}, MapErr(err)
	}
	if res.Status == domain.OutboxStatusDispatching || (res.DispatchLeaseExpires != nil && res.DispatchLeaseExpires.After(evalNow)) {
		return domain.EmailOutboxItem{}, fmt.Errorf("%w: cannot reconcile active DISPATCHING item while worker lease is live", domain.ErrConflict)
	}
	if res.Status != domain.OutboxStatusDispatchUnknown {
		return domain.EmailOutboxItem{}, fmt.Errorf("%w: only DISPATCH_UNKNOWN outbox items can be reconciled (current status: %s)", domain.ErrConflict, res.Status)
	}

	at := evalNow.UTC()
	res.ReconciledBy = &params.ActorID
	res.ReconciledAt = &at
	res.ReconciledDecision = decision
	res.ReconciledEvidence = evType
	res.ReconciledEvidenceRef = cleanEvRef
	res.ReconciledReasonCode = cleanReasonCode

	switch decision {
	case "CONFIRMED_SENT":
		res.Status = domain.OutboxStatusSent
		sentAt := at
		res.SentAt = &sentAt
		lastErr := ""
		if rowGuestBlocked {
			lastErr = "Reconciled as SENT after concurrent anonymisation/erasure request; content redacted"
			res.RecipientEmail = "[redacted]"
			res.Subject = "[Anonymised Email]"
			res.BodyText = "[redacted by GDPR erasure]"
			res.BodyHTML = ""
		}
		res.LastError = lastErr
		if err := tx.QueryRow(ctx, `
			UPDATE email_outbox
			SET status = 'SENT',
			    attempts = attempts + 1,
			    sent_at = COALESCE(sent_at, $2),
			    last_error = $3,
			    recipient_email = $4,
			    subject = $5,
			    body_text = $6,
			    body_html = $7,
			    locked_at = NULL,
			    worker_token = NULL,
			    dispatch_lease_expires_at = NULL,
			    dispatch_fenced_at = COALESCE(dispatch_fenced_at, $2),
			    dispatch_fence_epoch = dispatch_fence_epoch + 1,
			    reconciled_by = $8,
			    reconciled_at = $2,
			    reconciled_decision = $9,
			    reconciled_evidence_type = $10,
			    reconciled_evidence_ref = $11,
			    reconciled_reason_code = $12,
			    updated_at = NOW()
			WHERE id = $1
			RETURNING dispatch_fence_epoch
		`, params.OutboxID, at, res.LastError, res.RecipientEmail, res.Subject, res.BodyText, res.BodyHTML,
			params.ActorID, decision, evType, cleanEvRef, cleanReasonCode).Scan(&res.DispatchFenceEpoch); err != nil {
			return domain.EmailOutboxItem{}, MapErr(err)
		}
		if res.CommunicationID != nil {
			if _, err := tx.Exec(ctx, `UPDATE guest_communications SET delivery_status = 'ACCEPTED', updated_at = NOW() WHERE id = $1`, *res.CommunicationID); err != nil {
				return domain.EmailOutboxItem{}, MapErr(err)
			}
			evID, err := NewUUID()
			if err != nil {
				return domain.EmailOutboxItem{}, err
			}
			evDetail := fmt.Sprintf("Operator reconciled unknown dispatch as SENT (evidence=%s ref=%s code=%s)", evType, cleanEvRef, cleanReasonCode)
			if _, err := tx.Exec(ctx, `
				INSERT INTO communication_delivery_events (id, property_id, communication_id, email_outbox_id, event_type, provider, detail, occurred_at)
				VALUES ($1, $2, $3, $4, 'ACCEPTED', 'operator-reconcile', $5, $6)
			`, evID, params.PropertyID, *res.CommunicationID, params.OutboxID, evDetail, at); err != nil {
				return domain.EmailOutboxItem{}, MapErr(err)
			}
		}
	case "CONFIRMED_FAILED", "CANCEL":
		targetStatus := domain.OutboxStatusFailed
		commStatus := "FAILED"
		evTypeLog := "FAILED"
		lastErr := sanitizeOutboxErrorForAnon(cleanNote, true)
		if !rowGuestBlocked {
			lastErr = fmt.Sprintf("[%s / %s:%s] %s", cleanReasonCode, evType, cleanEvRef, cleanNote)
		}
		if rowGuestBlocked || decision == "CANCEL" {
			targetStatus = domain.OutboxStatusCancelled
			commStatus = "CANCELLED"
			evTypeLog = "CANCELLED"
			res.RecipientEmail = "[redacted]"
			res.Subject = "[Anonymised Email]"
			res.BodyText = "[redacted by GDPR erasure]"
			res.BodyHTML = ""
			res.CancelledAt = &at
			res.CancelReason = "Reconciled after unknown dispatch: cancelled without re-send"
			lastErr = "[redacted on anonymisation: reconciled unknown dispatch]"
		}
		res.Status = targetStatus
		res.LastError = lastErr
		if err := tx.QueryRow(ctx, `
			UPDATE email_outbox
			SET status = $2,
			    attempts = attempts + 1,
			    last_error = $3,
			    recipient_email = $4,
			    subject = $5,
			    body_text = $6,
			    body_html = $7,
			    cancel_reason = CASE WHEN $8 <> '' THEN $8 ELSE cancel_reason END,
			    cancelled_at = COALESCE(cancelled_at, $9),
			    locked_at = NULL,
			    worker_token = NULL,
			    dispatch_lease_expires_at = NULL,
			    dispatch_fenced_at = COALESCE(dispatch_fenced_at, $15),
			    dispatch_fence_epoch = dispatch_fence_epoch + 1,
			    reconciled_by = $10,
			    reconciled_at = $15,
			    reconciled_decision = $11,
			    reconciled_evidence_type = $12,
			    reconciled_evidence_ref = $13,
			    reconciled_reason_code = $14,
			    updated_at = NOW()
			WHERE id = $1
			RETURNING dispatch_fence_epoch
		`, params.OutboxID, targetStatus, res.LastError, res.RecipientEmail, res.Subject, res.BodyText, res.BodyHTML, res.CancelReason, res.CancelledAt,
			params.ActorID, decision, evType, cleanEvRef, cleanReasonCode, at).Scan(&res.DispatchFenceEpoch); err != nil {
			return domain.EmailOutboxItem{}, MapErr(err)
		}
		if res.CommunicationID != nil {
			if _, err := tx.Exec(ctx, `UPDATE guest_communications SET delivery_status = $2, updated_at = NOW() WHERE id = $1`, *res.CommunicationID, commStatus); err != nil {
				return domain.EmailOutboxItem{}, MapErr(err)
			}
			evID, err := NewUUID()
			if err != nil {
				return domain.EmailOutboxItem{}, err
			}
			evDetail := fmt.Sprintf("Operator reconciled unknown dispatch as non-delivered (decision=%s evidence=%s ref=%s code=%s)", decision, evType, cleanEvRef, cleanReasonCode)
			if _, err := tx.Exec(ctx, `
				INSERT INTO communication_delivery_events (id, property_id, communication_id, email_outbox_id, event_type, provider, detail, occurred_at)
				VALUES ($1, $2, $3, $4, $5, 'operator-reconcile', $6, $7)
			`, evID, params.PropertyID, *res.CommunicationID, params.OutboxID, evTypeLog, evDetail, at); err != nil {
				return domain.EmailOutboxItem{}, MapErr(err)
			}
		}
	}
	return res, nil
}

type OutboxSummary struct {
	PendingCount    int `json:"pending_count"`
	ProcessingCount int `json:"processing_count"`
	FailedCount     int `json:"failed_count"`
}

func (s *Store) GetOutboxSummary(ctx context.Context) (OutboxSummary, error) {
	var out OutboxSummary
	err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'PENDING'),
			COUNT(*) FILTER (WHERE status = 'PROCESSING'),
			COUNT(*) FILTER (WHERE status = 'FAILED')
		FROM email_outbox
	`).Scan(&out.PendingCount, &out.ProcessingCount, &out.FailedCount)
	return out, MapErr(err)
}

func (s *Store) CountActiveStripeEnabledProperties(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM properties prop
		JOIN property_booking_policies pol ON pol.property_id = prop.id
		WHERE prop.public_enabled = TRUE AND pol.stripe_enabled = TRUE
	`).Scan(&count)
	return count, MapErr(err)
}

func (s *Store) ListPaymentExceptions(ctx context.Context, propertyID uuid.UUID) ([]domain.PaymentException, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, property_id, hold_id, payment_attempt_id, reservation_id,
		       exception_type, amount_pence, currency, details, status,
		       resolution_notes, resolved_by, resolved_at, created_at
		FROM payment_exceptions
		WHERE property_id = $1
		ORDER BY CASE WHEN status = 'OPEN' THEN 0 ELSE 1 END, created_at DESC
	`, propertyID)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()
	var out []domain.PaymentException
	for rows.Next() {
		var ex domain.PaymentException
		if err := rows.Scan(
			&ex.ID, &ex.PropertyID, &ex.HoldID, &ex.PaymentAttemptID, &ex.ReservationID,
			&ex.ExceptionType, &ex.AmountPence, &ex.Currency, &ex.Details, &ex.Status,
			&ex.ResolutionNotes, &ex.ResolvedBy, &ex.ResolvedAt, &ex.CreatedAt,
		); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, ex)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetPaymentException(ctx context.Context, db DBTX, propertyID, exceptionID uuid.UUID) (domain.PaymentException, error) {
	if db == nil {
		db = s.pool
	}
	var ex domain.PaymentException
	err := db.QueryRow(ctx, `
		SELECT id, property_id, hold_id, payment_attempt_id, reservation_id,
		       exception_type, amount_pence, currency, details, status,
		       resolution_notes, resolved_by, resolved_at, created_at
		FROM payment_exceptions
		WHERE property_id = $1 AND id = $2
	`, propertyID, exceptionID).Scan(
		&ex.ID, &ex.PropertyID, &ex.HoldID, &ex.PaymentAttemptID, &ex.ReservationID,
		&ex.ExceptionType, &ex.AmountPence, &ex.Currency, &ex.Details, &ex.Status,
		&ex.ResolutionNotes, &ex.ResolvedBy, &ex.ResolvedAt, &ex.CreatedAt,
	)
	return ex, MapErr(err)
}

func (s *Store) ResolvePaymentException(ctx context.Context, tx pgx.Tx, propertyID, exceptionID, actorID uuid.UUID, notes string) (domain.PaymentException, error) {
	var currentStatus string
	err := tx.QueryRow(ctx, `
		SELECT status
		FROM payment_exceptions
		WHERE property_id = $1 AND id = $2
		FOR UPDATE
	`, propertyID, exceptionID).Scan(&currentStatus)
	if err != nil {
		return domain.PaymentException{}, MapErr(err)
	}
	if currentStatus != "OPEN" {
		return domain.PaymentException{}, domain.ErrConflict
	}

	var actorPtr *uuid.UUID
	if actorID != uuid.Nil {
		actorPtr = &actorID
	}
	_, err = tx.Exec(ctx, `
		UPDATE payment_exceptions
		SET status = 'MANUALLY_RESOLVED',
		    resolution_notes = $3,
		    resolved_by = $4,
		    resolved_at = NOW(),
		    updated_at = NOW()
		WHERE property_id = $1 AND id = $2 AND status = 'OPEN'
	`, propertyID, exceptionID, strings.TrimSpace(notes), actorPtr)
	if err != nil {
		return domain.PaymentException{}, MapErr(err)
	}
	return s.GetPaymentException(ctx, tx, propertyID, exceptionID)
}

func IsErrOverlap(err error) bool {
	return errors.Is(err, domain.ErrOverlap)
}
