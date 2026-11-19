package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/branding"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

const MaxOnboardingBulkUnits = 50

// CanCreateProperty reports whether actor is an active global owner_admin permitted to create new properties.
func (s *Service) CanCreateProperty(ctx context.Context, actor Actor) bool {
	if actor.MustRotatePassword {
		return false
	}
	if !auth.HasPermission(actor.Role, auth.PermPropertyWrite) || !auth.HasPermission(actor.Role, auth.PermUsersManage) {
		return false
	}
	if s.store == nil {
		return actor.PropertyID == nil
	}
	return s.effectiveActorPropertyScope(ctx, nil, actor) == nil
}

func (s *Service) requireGlobalOwnerAdmin(ctx context.Context, actor Actor) error {
	if err := s.requirePerm(actor, auth.PermPropertyWrite); err != nil {
		return err
	}
	if err := s.requirePerm(actor, auth.PermUsersManage); err != nil {
		return err
	}
	if s.effectiveActorPropertyScope(ctx, nil, actor) != nil {
		return domain.ErrForbidden
	}
	return nil
}

// OnboardingStep1Input captures property archetype, identity, location, check-in/out, and console terminology.
type OnboardingStep1Input struct {
	PropertyID     *uuid.UUID
	Preset         domain.OnboardingPreset
	Name           string
	Slug           string
	Tagline        string
	CountryCode    string
	Region         string
	AddressLine1   string
	Town           string
	Postcode       string
	Timezone       string
	Currency       string
	CheckInTime    string
	CheckOutTime   string
	ContactEmail   string
	ContactPhone   string
	PrimaryColor   string
	SecondaryColor string
	Terminology    branding.AdminTerminology
}

// SaveOnboardingStep1 creates a new dark-by-default property shell (when PropertyID is nil) or updates an existing property's Step 1 identity.
func (s *Service) SaveOnboardingStep1(ctx context.Context, actor Actor, in OnboardingStep1Input) (domain.Property, error) {
	preset := in.Preset
	if !preset.Valid() {
		preset = domain.OnboardingPresetCampsite
	}
	spec := domain.GetOnboardingPresetSpec(preset)

	name := strings.TrimSpace(in.Name)
	countryCode := strings.TrimSpace(strings.ToUpper(in.CountryCode))
	if countryCode == "" {
		countryCode = "GB"
	}
	currency := strings.TrimSpace(strings.ToUpper(in.Currency))
	if currency == "" {
		currency = "GBP"
	}
	tz := strings.TrimSpace(in.Timezone)
	if tz == "" {
		tz = "Europe/London"
	}
	checkIn := strings.TrimSpace(in.CheckInTime)
	if checkIn == "" {
		checkIn = spec.DefaultCheckIn
	}
	checkOut := strings.TrimSpace(in.CheckOutTime)
	if checkOut == "" {
		checkOut = spec.DefaultCheckOut
	}

	terms := in.Terminology
	if !terms.HasCustomTerms() {
		terms = spec.Terminology
	}
	cleanTerms, err := branding.SanitizeAndValidateTerminology(terms)
	if err != nil {
		return domain.Property{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	primaryHex, err := branding.NormalizeOptionalHex("primary colour", in.PrimaryColor)
	if err != nil {
		return domain.Property{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	secondaryHex, err := branding.NormalizeOptionalHex("secondary colour", in.SecondaryColor)
	if err != nil {
		return domain.Property{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	if _, err := branding.DeriveAndValidatePropertyPalette(primaryHex, secondaryHex); err != nil {
		return domain.Property{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}

	displayName, err := branding.SanitizeAndValidatePublicText("display name", name, 80)
	if err != nil {
		return domain.Property{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	cleanTagline, err := branding.SanitizeAndValidatePublicText("tagline", in.Tagline, 140)
	if err != nil {
		return domain.Property{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	cleanRegion, err := branding.SanitizeAndValidatePublicText("region", in.Region, 80)
	if err != nil {
		return domain.Property{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}

	pubJSON, err := domain.EncodePublicBrandingJSON(branding.PropertyBrandingInput{
		DisplayName:    displayName,
		Tagline:        cleanTagline,
		RegionLabel:    cleanRegion,
		PrimaryColor:   primaryHex,
		SecondaryColor: secondaryHex,
		Terminology:    cleanTerms,
	})
	if err != nil {
		return domain.Property{}, err
	}
	adminJSON, err := domain.EncodeAdminBrandingJSON("", cleanTerms)
	if err != nil {
		return domain.Property{}, err
	}
	legacyProj, err := json.Marshal(branding.PropertyBranding{
		DisplayTagline: cleanTagline,
		PrimaryColor:   primaryHex,
		AccentColor:    secondaryHex,
	})
	if err != nil {
		return domain.Property{}, err
	}

	now := s.nowUTC()

	// Case A: Creating a brand-new property shell
	if in.PropertyID == nil || *in.PropertyID == uuid.Nil {
		if err := s.requireGlobalOwnerAdmin(ctx, actor); err != nil {
			return domain.Property{}, err
		}

		explicitSlug := strings.TrimSpace(strings.ToLower(in.Slug))
		slug := explicitSlug
		if slug == "" {
			slug = domain.DeriveSlug(name, "my-property")
		}

		propID, err := postgres.NewUUID()
		if err != nil {
			return domain.Property{}, err
		}
		settingsJSON, err := domain.MergeOnboardingState(nil, domain.OnboardingState{
			Preset:      preset,
			CurrentStep: 2,
			StartedAt:   &now,
			UpdatedAt:   &now,
		})
		if err != nil {
			return domain.Property{}, err
		}

		prop := domain.Property{
			ID:           propID,
			Slug:         slug,
			Name:         name,
			Tagline:      cleanTagline,
			CountryCode:  countryCode,
			Region:       cleanRegion,
			AddressLine1: strings.TrimSpace(in.AddressLine1),
			Town:         strings.TrimSpace(in.Town),
			Postcode:     strings.TrimSpace(in.Postcode),
			Timezone:     tz,
			Currency:     currency,
			CheckInTime:  checkIn,
			CheckOutTime: checkOut,
			ContactEmail: strings.TrimSpace(in.ContactEmail),
			ContactPhone: strings.TrimSpace(in.ContactPhone),
			Settings:     settingsJSON,
		}
		if err := prop.Validate(); err != nil {
			return domain.Property{}, err
		}

		var created domain.Property
		err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
			// Ensure unique slug (auto-disambiguating if slug was derived automatically).
			candidateSlug := prop.Slug
			for attempt := 1; attempt <= 20; attempt++ {
				_, gerr := s.store.GetPropertyBySlug(ctx, tx, candidateSlug)
				if errors.Is(gerr, domain.ErrNotFound) {
					prop.Slug = candidateSlug
					break
				}
				if gerr != nil {
					return gerr
				}
				if explicitSlug != "" {
					return domain.NewValidationError(map[string]string{
						"slug": "this URL slug is already used by another property",
					})
				}
				candidateSlug = fmt.Sprintf("%s-%d", prop.Slug, attempt+1)
			}

			var txErr error
			created, txErr = s.store.CreateProperty(ctx, tx, prop)
			if txErr != nil {
				return txErr
			}
			if err := s.recordAudit(ctx, tx, actor, &created.ID, "property.created", "property", created.ID.String(), nil, created); err != nil {
				return err
			}

			// Seed dark-by-default booking policy with helpful preset house rule defaults.
			beforePol, err := s.store.GetBookingPolicy(ctx, tx, created.ID)
			if err != nil {
				return err
			}
			nextPol := beforePol
			nextPol.CancellationPolicy = spec.DefaultCancellation
			nextPol.QuietHours = spec.DefaultQuietHours
			nextPol.FirePolicy = spec.DefaultFirePolicy
			nextPol.PetPolicy = spec.DefaultPetPolicy
			nextPol.PrivacyNotice = spec.DefaultPrivacyNotice
			nextPol.StripeEnabled = false
			nextPol.GuestPortalEnabled = false
			updatedPol, err := s.store.UpdateBookingPolicy(ctx, tx, nextPol)
			if err != nil {
				return err
			}
			if err := s.recordAudit(ctx, tx, actor, &created.ID, "booking_policy.updated", "property_booking_policy", created.ID.String(), beforePol, updatedPol); err != nil {
				return err
			}

			// Create default Standard Flexible Tariff rate plan so pricing & quotes work immediately.
			planID, err := postgres.NewUUID()
			if err != nil {
				return err
			}
			createdPlan, err := s.store.CreateRatePlan(ctx, tx, domain.RatePlan{
				ID:          planID,
				PropertyID:  created.ID,
				Code:        "standard",
				Name:        "Standard Flexible Tariff",
				Description: "Default flexible nightly tariff.",
				IsDefault:   true,
				IsActive:    true,
				Priority:    100,
			})
			if err != nil {
				return err
			}
			if err := s.recordAudit(ctx, tx, actor, &created.ID, "rate_plan.created", "rate_plan", createdPlan.ID.String(), nil, createdPlan); err != nil {
				return err
			}

			// Seed built-in maintenance categories.
			if err := s.store.EnsureDefaultMaintenanceData(ctx, tx, created.ID); err != nil {
				return err
			}

			// Apply PWE-4 branding & preset terminology.
			prevBrand, err := s.store.GetPropertyBrandingByPropertyID(ctx, tx, created.ID)
			if err != nil {
				return err
			}
			updatedBrand, err := s.store.UpsertPropertyBrandingSettings(
				ctx, tx, created.ID, prevBrand.Version, pubJSON, adminJSON, legacyProj, actor.UserID,
			)
			if err != nil {
				return err
			}
			return s.recordAudit(
				ctx, tx, actor, &created.ID,
				"property.branding_updated", "property_branding", created.ID.String(),
				buildPropertyBrandingAuditPayload(prevBrand, "updated"),
				buildPropertyBrandingAuditPayload(updatedBrand, "updated"),
			)
		})
		if err != nil {
			return domain.Property{}, err
		}

		// Seed default service email templates & rules outside WithTx (uses pool directly).
		if err := s.store.EnsureDefaultCommsTemplatesAndRules(ctx, created.ID); err != nil {
			return domain.Property{}, err
		}
		return created, nil
	}

	// Case B: Updating Step 1 of an existing property
	propID := *in.PropertyID
	if err := s.requirePerm(actor, auth.PermPropertyWrite); err != nil {
		return domain.Property{}, err
	}
	if err := s.requirePropertyScope(actor, propID); err != nil {
		return domain.Property{}, err
	}

	var updated domain.Property
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetPropertyByID(ctx, tx, propID)
		if err != nil {
			return err
		}
		st, ok := domain.DecodeOnboardingState(before.Settings)
		if !ok {
			st = domain.OnboardingState{StartedAt: &now}
		}
		st.Preset = preset
		if st.CurrentStep < 2 {
			st.CurrentStep = 2
		}
		st.UpdatedAt = &now

		settingsJSON, err := domain.MergeOnboardingState(before.Settings, st)
		if err != nil {
			return err
		}

		next := before
		next.Name = name
		next.Tagline = cleanTagline
		next.CountryCode = countryCode
		next.Region = cleanRegion
		next.AddressLine1 = strings.TrimSpace(in.AddressLine1)
		next.Town = strings.TrimSpace(in.Town)
		next.Postcode = strings.TrimSpace(in.Postcode)
		next.Timezone = tz
		next.Currency = currency
		next.CheckInTime = checkIn
		next.CheckOutTime = checkOut
		next.ContactEmail = strings.TrimSpace(in.ContactEmail)
		next.ContactPhone = strings.TrimSpace(in.ContactPhone)
		next.Settings = settingsJSON
		if err := next.Validate(); err != nil {
			return err
		}

		updated, err = s.store.UpdateProperty(ctx, tx, next)
		if err != nil {
			return err
		}
		if err := s.recordAudit(ctx, tx, actor, &updated.ID, "property.updated", "property", updated.ID.String(), before, updated); err != nil {
			return err
		}

		if _, rperr := s.store.GetDefaultRatePlan(ctx, tx, updated.ID); errors.Is(rperr, domain.ErrNotFound) {
			planID, err := postgres.NewUUID()
			if err != nil {
				return err
			}
			createdPlan, err := s.store.CreateRatePlan(ctx, tx, domain.RatePlan{
				ID:          planID,
				PropertyID:  updated.ID,
				Code:        "standard",
				Name:        "Standard Flexible Tariff",
				Description: "Default flexible nightly tariff.",
				IsDefault:   true,
				IsActive:    true,
				Priority:    100,
			})
			if err != nil {
				return err
			}
			if err := s.recordAudit(ctx, tx, actor, &updated.ID, "rate_plan.created", "rate_plan", createdPlan.ID.String(), nil, createdPlan); err != nil {
				return err
			}
		}

		if err := s.store.EnsureDefaultMaintenanceData(ctx, tx, updated.ID); err != nil {
			return err
		}

		prevBrand, err := s.store.GetPropertyBrandingByPropertyID(ctx, tx, updated.ID)
		if err != nil {
			return err
		}
		updatedBrand, err := s.store.UpsertPropertyBrandingSettings(
			ctx, tx, updated.ID, prevBrand.Version, pubJSON, adminJSON, legacyProj, actor.UserID,
		)
		if err != nil {
			return err
		}
		return s.recordAudit(
			ctx, tx, actor, &updated.ID,
			"property.branding_updated", "property_branding", updated.ID.String(),
			buildPropertyBrandingAuditPayload(prevBrand, "updated"),
			buildPropertyBrandingAuditPayload(updatedBrand, "updated"),
		)
	})
	if err != nil {
		return domain.Property{}, err
	}
	_ = s.store.EnsureDefaultCommsTemplatesAndRules(ctx, updated.ID)
	return updated, nil
}

// OnboardingAccommodationBatchInput defines a unit category and N physical units to generate in one step.
type OnboardingAccommodationBatchInput struct {
	PropertyID           uuid.UUID
	Name                 string
	Code                 string
	Category             domain.UnitCategory
	Description          string
	Quantity             int
	UnitPrefix           string
	Zone                 string
	BaseCapacityAdults   int
	MaxAdults            int
	MaxChildren          int
	MaxInfants           int
	MaxGuests            int
	PetsAllowed          bool
	MaxPets              int
	BaseNightlyRatePence int64
	MinNights            int
	MaxNights            int
	Amenities            []string
}

// AddOnboardingAccommodationBatch creates a UnitType and auto-generates 1..50 physical Units atomically.
func (s *Service) AddOnboardingAccommodationBatch(ctx context.Context, actor Actor, in OnboardingAccommodationBatchInput) (domain.UnitType, []domain.Unit, error) {
	if err := s.requirePerm(actor, auth.PermInventoryWrite); err != nil {
		return domain.UnitType{}, nil, err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return domain.UnitType{}, nil, err
	}
	if in.Quantity < 1 || in.Quantity > MaxOnboardingBulkUnits {
		return domain.UnitType{}, nil, domain.NewValidationError(map[string]string{
			"quantity": fmt.Sprintf("must be between 1 and %d", MaxOnboardingBulkUnits),
		})
	}

	name := strings.TrimSpace(in.Name)
	explicitCode := strings.TrimSpace(strings.ToLower(in.Code))
	code := explicitCode
	if code == "" {
		code = domain.DeriveSlug(name, "unit-type")
	}

	existingUTs, err := s.store.ListUnitTypes(ctx, in.PropertyID, true)
	if err != nil {
		return domain.UnitType{}, nil, err
	}
	usedUTCode := make(map[string]bool, len(existingUTs))
	for _, ut := range existingUTs {
		usedUTCode[strings.ToLower(ut.Code)] = true
	}
	if usedUTCode[code] {
		if explicitCode != "" {
			return domain.UnitType{}, nil, domain.NewValidationError(map[string]string{
				"code": "an accommodation type with this code already exists",
			})
		}
		baseCode := code
		for n := 2; n <= 50; n++ {
			cand := fmt.Sprintf("%s-%d", baseCode, n)
			if !usedUTCode[cand] {
				code = cand
				break
			}
		}
	}

	existingUnits, err := s.store.ListUnits(ctx, in.PropertyID, nil, true)
	if err != nil {
		return domain.UnitType{}, nil, err
	}
	usedUnitCode := make(map[string]bool, len(existingUnits))
	for _, u := range existingUnits {
		usedUnitCode[strings.ToUpper(u.Code)] = true
	}

	prefix := strings.TrimSpace(strings.ToUpper(in.UnitPrefix))
	if prefix == "" {
		prefix = domain.DeriveUnitPrefix(name)
	}
	var cleanedPrefix strings.Builder
	for _, r := range prefix {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			cleanedPrefix.WriteRune(r)
		}
	}
	prefix = strings.Trim(cleanedPrefix.String(), "-")
	if prefix == "" {
		prefix = "UNIT"
	}

	utID, err := postgres.NewUUID()
	if err != nil {
		return domain.UnitType{}, nil, err
	}
	maxAdults := in.MaxAdults
	if maxAdults < 1 {
		maxAdults = 2
	}
	baseAdults := in.BaseCapacityAdults
	if baseAdults < 1 {
		baseAdults = maxAdults
	}
	if baseAdults > maxAdults {
		baseAdults = maxAdults
	}
	maxGuests := in.MaxGuests
	if maxGuests < maxAdults {
		maxGuests = maxAdults + in.MaxChildren
		if maxGuests < maxAdults {
			maxGuests = maxAdults
		}
	}
	minNights := in.MinNights
	if minNights < 1 {
		minNights = 1
	}
	maxNights := in.MaxNights
	if maxNights < minNights {
		maxNights = 28
		if maxNights < minNights {
			maxNights = minNights
		}
	}

	ut := domain.UnitType{
		ID:                   utID,
		PropertyID:           in.PropertyID,
		Code:                 code,
		Name:                 name,
		Category:             in.Category,
		Description:          strings.TrimSpace(in.Description),
		BaseCapacityAdults:   baseAdults,
		MaxAdults:            maxAdults,
		MaxChildren:          in.MaxChildren,
		MaxInfants:           in.MaxInfants,
		MaxGuests:            maxGuests,
		PetsAllowed:          in.PetsAllowed,
		MaxPets:              in.MaxPets,
		BaseNightlyRatePence: in.BaseNightlyRatePence,
		MinNights:            minNights,
		MaxNights:            maxNights,
		Amenities:            in.Amenities,
		IsActive:             true,
		SortOrder:            (len(existingUTs) + 1) * 10,
	}
	if ut.PetsAllowed && ut.MaxPets < 1 {
		ut.MaxPets = 1
	}
	if err := ut.Validate(); err != nil {
		return domain.UnitType{}, nil, err
	}

	var createdUT domain.UnitType
	createdUnits := make([]domain.Unit, 0, in.Quantity)
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		createdUT, txErr = s.store.CreateUnitType(ctx, tx, ut)
		if txErr != nil {
			return txErr
		}
		if err := s.recordAudit(ctx, tx, actor, &in.PropertyID, "unit_type.created", "unit_type", createdUT.ID.String(), nil, createdUT); err != nil {
			return err
		}

		seq := 1
		for i := 0; i < in.Quantity; i++ {
			var unitCode, unitName string
			if in.Quantity == 1 && !usedUnitCode[prefix] {
				unitCode = prefix
				unitName = name
			} else {
				for {
					cand := fmt.Sprintf("%s-%02d", prefix, seq)
					seq++
					if !usedUnitCode[cand] {
						unitCode = cand
						unitName = fmt.Sprintf("%s %02d", name, seq-1)
						break
					}
				}
			}
			usedUnitCode[unitCode] = true

			uid, err := postgres.NewUUID()
			if err != nil {
				return err
			}
			u := domain.Unit{
				ID:         uid,
				PropertyID: in.PropertyID,
				UnitTypeID: createdUT.ID,
				Code:       unitCode,
				Name:       unitName,
				Zone:       strings.TrimSpace(in.Zone),
				Status:     domain.UnitStatusActive,
				SortOrder:  (len(existingUnits) + i + 1) * 10,
			}
			if err := u.Validate(); err != nil {
				return err
			}
			cu, err := s.store.CreateUnit(ctx, tx, u)
			if err != nil {
				return err
			}
			if err := s.recordAudit(ctx, tx, actor, &in.PropertyID, "unit.created", "unit", cu.ID.String(), nil, cu); err != nil {
				return err
			}
			createdUnits = append(createdUnits, cu)
		}
		return nil
	})
	return createdUT, createdUnits, err
}

// OnboardingStep3Input captures VAT status, extra-guest surcharges, optional seasonal/weekend rate rule, and extras.
type OnboardingStep3Input struct {
	PropertyID             uuid.UUID
	VATRegistered          bool
	VATNumber              string
	ExtraAdultNightlyPence int64
	ChildNightlyPence      int64
	InfantNightlyPence     int64
	AddPeakRule            bool
	PeakUnitTypeID         *uuid.UUID // nil applies rule across all active unit types
	PeakRuleName           string
	PeakStartDate          time.Time
	PeakEndDate            time.Time
	PeakDaysOfWeek         domain.DaysOfWeekMask
	PeakNightlyRatePence   int64
	PeakMinNights          *int
	ExtrasToCreate         []domain.Extra
}

// SaveOnboardingPricingAndExtras saves Step 3 pricing, VAT, optional seasonal uplift rule, and optional add-on extras.
func (s *Service) SaveOnboardingPricingAndExtras(ctx context.Context, actor Actor, in OnboardingStep3Input) error {
	if err := s.requirePerm(actor, auth.PermPropertyWrite); err != nil {
		return err
	}
	if err := s.requirePerm(actor, auth.PermRatesWrite); err != nil {
		return err
	}
	if err := s.requirePerm(actor, auth.PermInventoryWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return err
	}

	existingExtras, err := s.store.ListExtras(ctx, in.PropertyID, true)
	if err != nil {
		return err
	}
	existingExtraCode := make(map[string]bool, len(existingExtras))
	for _, ex := range existingExtras {
		existingExtraCode[strings.ToLower(ex.Code)] = true
	}

	activeUTs, err := s.store.ListUnitTypes(ctx, in.PropertyID, false)
	if err != nil {
		return err
	}

	now := s.nowUTC()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		beforeProp, err := s.store.GetPropertyByID(ctx, tx, in.PropertyID)
		if err != nil {
			return err
		}
		st, ok := domain.DecodeOnboardingState(beforeProp.Settings)
		if !ok {
			st = domain.OnboardingState{Preset: domain.OnboardingPresetCampsite, StartedAt: &now}
		}
		if st.CurrentStep < 4 {
			st.CurrentStep = 4
		}
		st.UpdatedAt = &now
		settingsJSON, err := domain.MergeOnboardingState(beforeProp.Settings, st)
		if err != nil {
			return err
		}

		nextProp := beforeProp
		nextProp.VATRegistered = in.VATRegistered
		if in.VATRegistered {
			nextProp.VATNumber = strings.TrimSpace(in.VATNumber)
		} else {
			nextProp.VATNumber = ""
		}
		nextProp.Settings = settingsJSON
		if err := nextProp.Validate(); err != nil {
			return err
		}
		updatedProp, err := s.store.UpdateProperty(ctx, tx, nextProp)
		if err != nil {
			return err
		}
		if err := s.recordAudit(ctx, tx, actor, &in.PropertyID, "property.updated", "property", updatedProp.ID.String(), beforeProp, updatedProp); err != nil {
			return err
		}

		beforePol, err := s.store.GetBookingPolicy(ctx, tx, in.PropertyID)
		if err != nil {
			return err
		}
		nextPol := beforePol
		nextPol.ExtraAdultNightlyPence = in.ExtraAdultNightlyPence
		nextPol.ChildNightlyPence = in.ChildNightlyPence
		nextPol.InfantNightlyPence = in.InfantNightlyPence
		if err := nextPol.Validate(); err != nil {
			return err
		}
		updatedPol, err := s.store.UpdateBookingPolicy(ctx, tx, nextPol)
		if err != nil {
			return err
		}
		if err := s.recordAudit(ctx, tx, actor, &in.PropertyID, "booking_policy.updated", "property_booking_policy", in.PropertyID.String(), beforePol, updatedPol); err != nil {
			return err
		}

		if in.AddPeakRule {
			plan, err := s.store.GetDefaultRatePlan(ctx, tx, in.PropertyID)
			if err != nil {
				return err
			}
			var targetUTs []domain.UnitType
			if in.PeakUnitTypeID != nil && *in.PeakUnitTypeID != uuid.Nil {
				ut, err := s.store.GetUnitType(ctx, tx, in.PropertyID, *in.PeakUnitTypeID)
				if err != nil {
					return err
				}
				targetUTs = []domain.UnitType{ut}
			} else {
				targetUTs = activeUTs
			}
			if len(targetUTs) == 0 {
				return domain.NewValidationError(map[string]string{
					"unit_type_id": "add at least one accommodation type in Step 2 before creating a seasonal rate rule",
				})
			}
			ruleName := strings.TrimSpace(in.PeakRuleName)
			if ruleName == "" {
				ruleName = "Weekend / Peak Season Tariff"
			}
			dow := in.PeakDaysOfWeek
			if dow < 1 || dow > 127 {
				dow = domain.DayFri | domain.DaySat
			}
			for _, ut := range targetUTs {
				rid, err := postgres.NewUUID()
				if err != nil {
					return err
				}
				rr := domain.RateRule{
					ID:                rid,
					PropertyID:        in.PropertyID,
					RatePlanID:        plan.ID,
					UnitTypeID:        ut.ID,
					Name:              ruleName,
					StartDate:         in.PeakStartDate,
					EndDate:           in.PeakEndDate,
					DaysOfWeek:        dow,
					NightlyRatePence:  in.PeakNightlyRatePence,
					MinNightsOverride: in.PeakMinNights,
					Priority:          200,
					IsActive:          true,
				}
				if err := rr.Validate(); err != nil {
					return err
				}
				createdRR, err := s.store.CreateRateRule(ctx, tx, rr)
				if err != nil {
					return err
				}
				if err := s.recordAudit(ctx, tx, actor, &in.PropertyID, "rate_rule.created", "rate_rule", createdRR.ID.String(), nil, createdRR); err != nil {
					return err
				}
			}
		}

		for i, ex := range in.ExtrasToCreate {
			code := strings.TrimSpace(strings.ToLower(ex.Code))
			if code == "" {
				code = domain.DeriveSlug(ex.Name, "extra-addon")
			}
			if existingExtraCode[code] {
				continue // Idempotent if re-saving Step 3
			}
			existingExtraCode[code] = true
			eid, err := postgres.NewUUID()
			if err != nil {
				return err
			}
			ex.ID = eid
			ex.PropertyID = in.PropertyID
			ex.Code = code
			ex.IsActive = true
			if ex.MaxQuantity < 1 {
				ex.MaxQuantity = 1
			}
			if ex.SortOrder == 0 {
				ex.SortOrder = (len(existingExtras) + i + 1) * 10
			}
			if err := ex.Validate(); err != nil {
				return err
			}
			createdEx, err := s.store.CreateExtra(ctx, tx, ex)
			if err != nil {
				return err
			}
			if err := s.recordAudit(ctx, tx, actor, &in.PropertyID, "extra.created", "extra", createdEx.ID.String(), nil, createdEx); err != nil {
				return err
			}
		}
		return nil
	})
}

// OnboardingStep4Input captures deposits, hold timer, check-in/out times, and guest house rules.
type OnboardingStep4Input struct {
	PropertyID                  uuid.UUID
	CheckInTime                 string
	CheckOutTime                string
	HoldDurationMinutes         int
	DepositMode                 string
	DepositPercentage           int
	DepositFixedPence           int64
	BalanceDueDaysBeforeArrival int
	BookingTermsVersion         string
	BookingTermsURL             string
	PrivacyPolicyURL            string
	CancellationPolicy          string
	QuietHours                  string
	FirePolicy                  string
	PetPolicy                   string
	PrivacyNotice               string
}

// SaveOnboardingRulesAndPolicies persists Step 4 check-in/out times, deposit rules, and house rules while keeping dark launch flags untouched.
func (s *Service) SaveOnboardingRulesAndPolicies(ctx context.Context, actor Actor, in OnboardingStep4Input) error {
	if err := s.requirePerm(actor, auth.PermPropertyWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return err
	}

	now := s.nowUTC()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		beforeProp, err := s.store.GetPropertyByID(ctx, tx, in.PropertyID)
		if err != nil {
			return err
		}
		st, ok := domain.DecodeOnboardingState(beforeProp.Settings)
		if !ok {
			st = domain.OnboardingState{Preset: domain.OnboardingPresetCampsite, StartedAt: &now}
		}
		if st.CurrentStep < 5 {
			st.CurrentStep = 5
		}
		st.UpdatedAt = &now
		settingsJSON, err := domain.MergeOnboardingState(beforeProp.Settings, st)
		if err != nil {
			return err
		}

		nextProp := beforeProp
		if strings.TrimSpace(in.CheckInTime) != "" {
			nextProp.CheckInTime = strings.TrimSpace(in.CheckInTime)
		}
		if strings.TrimSpace(in.CheckOutTime) != "" {
			nextProp.CheckOutTime = strings.TrimSpace(in.CheckOutTime)
		}
		nextProp.Settings = settingsJSON
		if err := nextProp.Validate(); err != nil {
			return err
		}
		updatedProp, err := s.store.UpdateProperty(ctx, tx, nextProp)
		if err != nil {
			return err
		}
		if err := s.recordAudit(ctx, tx, actor, &in.PropertyID, "property.updated", "property", updatedProp.ID.String(), beforeProp, updatedProp); err != nil {
			return err
		}

		beforePol, err := s.store.GetBookingPolicy(ctx, tx, in.PropertyID)
		if err != nil {
			return err
		}
		nextPol := beforePol
		nextPol.HoldDurationMinutes = in.HoldDurationMinutes
		nextPol.DepositMode = in.DepositMode
		nextPol.DepositPercentage = in.DepositPercentage
		nextPol.DepositFixedPence = in.DepositFixedPence
		nextPol.BalanceDueDaysBeforeArrival = in.BalanceDueDaysBeforeArrival
		if strings.TrimSpace(in.BookingTermsVersion) != "" {
			nextPol.BookingTermsVersion = strings.TrimSpace(in.BookingTermsVersion)
		}
		nextPol.BookingTermsURL = strings.TrimSpace(in.BookingTermsURL)
		nextPol.PrivacyPolicyURL = strings.TrimSpace(in.PrivacyPolicyURL)
		nextPol.CancellationPolicy = strings.TrimSpace(in.CancellationPolicy)
		nextPol.QuietHours = strings.TrimSpace(in.QuietHours)
		nextPol.FirePolicy = strings.TrimSpace(in.FirePolicy)
		nextPol.PetPolicy = strings.TrimSpace(in.PetPolicy)
		nextPol.PrivacyNotice = strings.TrimSpace(in.PrivacyNotice)
		if err := nextPol.Validate(); err != nil {
			return err
		}
		updatedPol, err := s.store.UpdateBookingPolicy(ctx, tx, nextPol)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &in.PropertyID, "booking_policy.updated", "property_booking_policy", in.PropertyID.String(), beforePol, updatedPol)
	})
}

// AddOnboardingTeamMember creates a property-scoped staff member with MustRotatePassword=true during Step 5.
func (s *Service) AddOnboardingTeamMember(ctx context.Context, actor Actor, propertyID uuid.UUID, fullName, email string, role domain.Role, tempPassword string) (domain.AdminUser, error) {
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.AdminUser{}, err
	}
	switch role {
	case domain.RoleManager, domain.RoleStaff, domain.RoleHousekeepingStaff, domain.RoleMaintenanceStaff, domain.RoleViewer:
	default:
		return domain.AdminUser{}, domain.NewValidationError(map[string]string{
			"role": "choose manager, staff, housekeeping_staff, maintenance_staff, or viewer",
		})
	}
	pid := propertyID
	return s.CreateUser(ctx, actor, CreateUserInput{
		PropertyID:         &pid,
		Email:              strings.TrimSpace(email),
		FullName:           strings.TrimSpace(fullName),
		Role:               role,
		Password:           tempPassword,
		MustRotatePassword: true,
	})
}

// SetOnboardingStep advances or updates the saved onboarding step (1..6) for resumable navigation.
func (s *Service) SetOnboardingStep(ctx context.Context, actor Actor, propertyID uuid.UUID, step int) (domain.Property, error) {
	if err := s.requirePerm(actor, auth.PermPropertyWrite); err != nil {
		return domain.Property{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.Property{}, err
	}
	if step < 1 {
		step = 1
	}
	if step > 6 {
		step = 6
	}
	now := s.nowUTC()
	var updated domain.Property
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetPropertyByID(ctx, tx, propertyID)
		if err != nil {
			return err
		}
		st, ok := domain.DecodeOnboardingState(before.Settings)
		if !ok {
			st = domain.OnboardingState{Preset: domain.OnboardingPresetCampsite, StartedAt: &now}
		}
		st.CurrentStep = step
		st.UpdatedAt = &now
		settingsJSON, err := domain.MergeOnboardingState(before.Settings, st)
		if err != nil {
			return err
		}
		next := before
		next.Settings = settingsJSON
		updated, err = s.store.UpdateProperty(ctx, tx, next)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "property.updated", "property", updated.ID.String(), before, updated)
	})
	return updated, err
}

// CompleteOnboarding marks guided setup finished while keeping Stripe live charges and Guest Portal safely dark by default.
func (s *Service) CompleteOnboarding(ctx context.Context, actor Actor, propertyID uuid.UUID) (domain.Property, error) {
	if err := s.requirePerm(actor, auth.PermPropertyWrite); err != nil {
		return domain.Property{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.Property{}, err
	}
	now := s.nowUTC()
	var updated domain.Property
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetPropertyByID(ctx, tx, propertyID)
		if err != nil {
			return err
		}
		st, ok := domain.DecodeOnboardingState(before.Settings)
		if !ok {
			st = domain.OnboardingState{Preset: domain.OnboardingPresetCampsite, StartedAt: &now}
		}
		st.CurrentStep = 6
		st.UpdatedAt = &now
		st.CompletedAt = &now
		settingsJSON, err := domain.MergeOnboardingState(before.Settings, st)
		if err != nil {
			return err
		}
		next := before
		next.Settings = settingsJSON
		updated, err = s.store.UpdateProperty(ctx, tx, next)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "property.updated", "property", updated.ID.String(), before, updated)
	})
	return updated, err
}
