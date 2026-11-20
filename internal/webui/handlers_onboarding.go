package webui

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/branding"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func (h *Handler) registerOnboardingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /onboarding", h.requirePageAuth(false, h.handleOnboardingGet))
	mux.HandleFunc("POST /onboarding/step1", h.requirePageAuth(true, h.handleOnboardingStep1Post))
	mux.HandleFunc("POST /onboarding/step2/unit-batch", h.requirePageAuth(true, h.handleOnboardingStep2BatchPost))
	mux.HandleFunc("POST /onboarding/step2/next", h.requirePageAuth(true, h.handleOnboardingStep2NextPost))
	mux.HandleFunc("POST /onboarding/step3", h.requirePageAuth(true, h.handleOnboardingStep3Post))
	mux.HandleFunc("POST /onboarding/step4", h.requirePageAuth(true, h.handleOnboardingStep4Post))
	mux.HandleFunc("POST /onboarding/step5/user", h.requirePageAuth(true, h.handleOnboardingStep5UserPost))
	mux.HandleFunc("POST /onboarding/step5/next", h.requirePageAuth(true, h.handleOnboardingStep5NextPost))
	mux.HandleFunc("POST /onboarding/step6/backup-target", h.requirePageAuth(true, h.handleOnboardingStep6BackupPost))
	mux.HandleFunc("POST /onboarding/step6/next", h.requirePageAuth(true, h.handleOnboardingStep6NextPost))
	mux.HandleFunc("POST /onboarding/complete", h.requirePageAuth(true, h.handleOnboardingCompletePost))
	mux.HandleFunc("POST /property/switch", h.requirePageAuth(true, h.handlePropertySwitchPost))
}

type onboardingStepNav struct {
	Number int
	Label  string
	State  string // "active", "done", "upcoming"
	Href   string
}

type onboardingViewData struct {
	baseView
	Step               int
	Steps              []onboardingStepNav
	IsNewProperty      bool
	SelectedPreset     domain.OnboardingPreset
	PresetSpec         domain.OnboardingPresetSpec
	Presets            []domain.OnboardingPresetSpec
	TargetProperty     domain.Property
	TargetBranding     domain.PropertyBrandingView
	Policy             domain.PropertyBookingPolicy
	UnitTypes          []domain.UnitType
	Units              []domain.Unit
	UnitCountByType    map[uuid.UUID]int
	Extras             []domain.Extra
	ExistingExtraCodes map[string]bool
	DefaultPlan        domain.RatePlan
	RateRules          []domain.RateRule
	TeamUsers          []domain.AdminUser
	BackupTargets      []domain.BackupStorageTarget
	EmbedSnippets      WebsiteEmbedSnippets
	DefaultPeakStart   string
	DefaultPeakEnd     string
	OnboardingComplete bool
}

func onboardingURL(propertyID uuid.UUID, step int, isNew bool) string {
	q := url.Values{}
	if isNew {
		q.Set("new", "1")
	} else if propertyID != uuid.Nil {
		q.Set("property_id", propertyID.String())
	}
	if step >= 1 && step <= 7 {
		q.Set("step", fmt.Sprintf("%d", step))
	}
	if len(q) == 0 {
		return "/onboarding"
	}
	return "/onboarding?" + q.Encode()
}

func (h *Handler) handlePropertySwitchPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, err := uuid.Parse(strings.TrimSpace(r.FormValue("property_id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Property", "Please choose a valid property.")
		return
	}
	prop, err := h.svc.GetProperty(r.Context(), actor, propID)
	if err != nil {
		h.renderDomainError(w, err, "Property Unavailable", "Unable to switch to the selected property.")
		return
	}
	h.setActivePropertyCookie(w, prop.ID)
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/")
	redirectMsg(w, r, retPath, fmt.Sprintf("Switched active property to %s.", prop.Name))
}

func (h *Handler) handleOnboardingGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Guided Property Setup", "onboarding")
	if err != nil {
		h.renderDomainError(w, err, "Setup Wizard Unavailable", "Unable to load the property setup wizard.")
		return
	}
	if !base.CanWriteProperty {
		h.renderError(w, http.StatusForbidden, "Access Denied", "Your administrative role does not have permission to configure property settings.")
		return
	}

	isNew := r.URL.Query().Get("new") == "1"
	var targetProp domain.Property

	if isNew {
		if !base.CanCreateProperty {
			h.renderError(w, http.StatusForbidden, "Access Denied", "Only global owner administrators can create a new property.")
			return
		}
	} else if qID := parseOptionalUUID(r.URL.Query().Get("property_id")); qID != nil {
		p, gerr := h.svc.GetProperty(r.Context(), actor, *qID)
		if gerr != nil {
			h.renderDomainError(w, gerr, "Property Not Found", "The requested property could not be loaded.")
			return
		}
		targetProp = p
		h.setActivePropertyCookie(w, targetProp.ID)
	} else if len(base.AllProperties) == 0 {
		if !base.CanCreateProperty {
			h.renderError(w, http.StatusForbidden, "Access Denied", "Only global owner administrators can create the initial property.")
			return
		}
		isNew = true
	} else {
		targetProp = base.Property
		if st, ok := domain.DecodeOnboardingState(targetProp.Settings); !ok || st.IsComplete() {
			for _, candidate := range base.AllProperties {
				if cst, cok := domain.DecodeOnboardingState(candidate.Settings); cok && !cst.IsComplete() {
					targetProp = candidate
					h.setActivePropertyCookie(w, targetProp.ID)
					break
				}
			}
		}
	}

	selectedPreset := domain.OnboardingPreset(strings.TrimSpace(r.URL.Query().Get("preset")))
	savedStep := 1
	onboardingComplete := false

	if !isNew && targetProp.ID != uuid.Nil {
		if st, ok := domain.DecodeOnboardingState(targetProp.Settings); ok {
			if !selectedPreset.Valid() {
				selectedPreset = st.Preset
			}
			savedStep = st.CurrentStep
			onboardingComplete = st.IsComplete()
		} else {
			savedStep = 7
			onboardingComplete = true
		}
	}
	if !selectedPreset.Valid() {
		selectedPreset = domain.OnboardingPresetCampsite
	}
	spec := domain.GetOnboardingPresetSpec(selectedPreset)

	step := savedStep
	if qStep := parseIntDefault(r.URL.Query().Get("step"), 0); qStep >= 1 && qStep <= 7 {
		step = qStep
	}
	if isNew || targetProp.ID == uuid.Nil {
		step = 1
	}

	if isNew {
		targetProp = domain.Property{
			CountryCode:  "GB",
			Timezone:     "Europe/London",
			Currency:     "GBP",
			CheckInTime:  spec.DefaultCheckIn,
			CheckOutTime: spec.DefaultCheckOut,
			ContactEmail: actor.Email,
		}
	}

	var (
		targetBranding domain.PropertyBrandingView
		policy         domain.PropertyBookingPolicy
		unitTypes      []domain.UnitType
		units          []domain.Unit
		extras         []domain.Extra
		defaultPlan    domain.RatePlan
		rateRules      []domain.RateRule
		teamUsers      []domain.AdminUser
		backupTargets  []domain.BackupStorageTarget
		embedSnippets  WebsiteEmbedSnippets
	)
	unitCountByType := make(map[uuid.UUID]int)
	existingExtraCodes := make(map[string]bool)

	if !isNew && targetProp.ID != uuid.Nil {
		if pb, berr := h.svc.GetPropertyBranding(r.Context(), actor, targetProp.ID); berr == nil {
			targetBranding = pb
			base.Property = targetProp
			base.PropertyBranding = pb
			base.PropertyTerms = pb.ResolvedTerms
		}
		policy, _ = h.svc.GetBookingPolicy(r.Context(), actor, targetProp.ID)
		unitTypes, _ = h.svc.ListUnitTypes(r.Context(), actor, targetProp.ID)
		units, _ = h.svc.ListUnits(r.Context(), actor, targetProp.ID, nil)
		for _, u := range units {
			unitCountByType[u.UnitTypeID]++
		}
		extras, _ = h.svc.ListExtras(r.Context(), actor, targetProp.ID)
		for _, ex := range extras {
			existingExtraCodes[strings.ToLower(ex.Code)] = true
		}
		if plans, perr := h.svc.ListRatePlans(r.Context(), actor, targetProp.ID); perr == nil && len(plans) > 0 {
			defaultPlan = plans[0]
			for _, pl := range plans {
				if pl.IsDefault {
					defaultPlan = pl
					break
				}
			}
			rateRules, _ = h.svc.ListRateRules(r.Context(), actor, targetProp.ID, &defaultPlan.ID, nil)
		}
		if base.CanManageUsers {
			if allUsers, uerr := h.svc.ListUsers(r.Context(), actor); uerr == nil {
				for _, u := range allUsers {
					if u.PropertyID == nil || *u.PropertyID == targetProp.ID {
						teamUsers = append(teamUsers, u)
					}
				}
			}
		}
		if base.CanCreateProperty {
			if ov, berr := h.svc.GetBackupOverview(r.Context(), actor); berr == nil {
				backupTargets = ov.Targets
			}
		}
		embedSnippets = buildWebsiteEmbedSnippets(h.cfg.PublicOrigin, targetProp, targetBranding)
	} else {
		targetBranding.Terminology = spec.Terminology
		targetBranding.ResolvedTerms = branding.ResolveTerminology(spec.Terminology)
	}

	labels := []string{
		"1. Property & Style",
		"2. Accommodation",
		"3. Pricing & Extras",
		"4. Rules & Deposits",
		"5. Team Access",
		"6. Cloud Backup",
		"7. Review & Website",
	}
	steps := make([]onboardingStepNav, 0, len(labels))
	for i, lbl := range labels {
		num := i + 1
		state := "upcoming"
		switch {
		case num == step:
			state = "active"
		case !isNew && (onboardingComplete || num < savedStep || num < step):
			state = "done"
		}
		href := ""
		if !isNew && targetProp.ID != uuid.Nil {
			href = onboardingURL(targetProp.ID, num, false)
		}
		steps = append(steps, onboardingStepNav{
			Number: num,
			Label:  lbl,
			State:  state,
			Href:   href,
		})
	}

	nowUTC := time.Now().UTC()
	peakYear := nowUTC.Year()
	peakStart := time.Date(peakYear, 7, 1, 0, 0, 0, 0, time.UTC)
	peakEnd := time.Date(peakYear, 9, 1, 0, 0, 0, 0, time.UTC)
	if nowUTC.After(peakEnd) {
		peakStart = peakStart.AddDate(1, 0, 0)
		peakEnd = peakEnd.AddDate(1, 0, 0)
	}

	h.renderPage(w, "onboarding.html", onboardingViewData{
		baseView:           base,
		Step:               step,
		Steps:              steps,
		IsNewProperty:      isNew,
		SelectedPreset:     selectedPreset,
		PresetSpec:         spec,
		Presets:            domain.AllOnboardingPresets(),
		TargetProperty:     targetProp,
		TargetBranding:     targetBranding,
		Policy:             policy,
		UnitTypes:          unitTypes,
		Units:              units,
		UnitCountByType:    unitCountByType,
		Extras:             extras,
		ExistingExtraCodes: existingExtraCodes,
		DefaultPlan:        defaultPlan,
		RateRules:          rateRules,
		TeamUsers:          teamUsers,
		BackupTargets:      backupTargets,
		EmbedSnippets:      embedSnippets,
		DefaultPeakStart:   domain.FormatDate(peakStart),
		DefaultPeakEnd:     domain.FormatDate(peakEnd),
		OnboardingComplete: onboardingComplete,
	})
}

func (h *Handler) handleOnboardingStep1Post(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID := parseOptionalUUID(r.FormValue("property_id"))
	isNew := propID == nil || *propID == uuid.Nil
	preset := domain.OnboardingPreset(strings.TrimSpace(r.FormValue("preset")))
	if !preset.Valid() {
		preset = domain.OnboardingPresetCampsite
	}

	in := service.OnboardingStep1Input{
		PropertyID:     propID,
		Preset:         preset,
		Name:           r.FormValue("name"),
		Slug:           r.FormValue("slug"),
		Tagline:        r.FormValue("tagline"),
		CountryCode:    r.FormValue("country_code"),
		Region:         r.FormValue("region"),
		AddressLine1:   r.FormValue("address_line1"),
		Town:           r.FormValue("town"),
		Postcode:       r.FormValue("postcode"),
		Timezone:       r.FormValue("timezone"),
		Currency:       r.FormValue("currency"),
		CheckInTime:    r.FormValue("check_in_time"),
		CheckOutTime:   r.FormValue("check_out_time"),
		ContactEmail:   r.FormValue("contact_email"),
		ContactPhone:   r.FormValue("contact_phone"),
		PrimaryColor:   r.FormValue("primary_color"),
		SecondaryColor: r.FormValue("secondary_color"),
		Terminology: branding.AdminTerminology{
			UnitSingular:  r.FormValue("unit_singular"),
			UnitPlural:    r.FormValue("unit_plural"),
			GuestSingular: r.FormValue("guest_singular"),
			GuestPlural:   r.FormValue("guest_plural"),
			StayLabel:     r.FormValue("stay_label"),
		},
	}

	saved, err := h.svc.SaveOnboardingStep1(r.Context(), actor, in)
	if err != nil {
		errTarget := "/onboarding"
		if isNew {
			errTarget = "/onboarding?new=1&preset=" + url.QueryEscape(string(preset))
		} else if propID != nil {
			errTarget = onboardingURL(*propID, 1, false)
		}
		h.redirectErr(w, r, errTarget, err)
		return
	}
	h.setActivePropertyCookie(w, saved.ID)
	redirectMsg(w, r, onboardingURL(saved.ID, 2, false), "Property profile saved. Next: add your accommodation.")
}

func (h *Handler) handleOnboardingStep2BatchPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, err := uuid.Parse(strings.TrimSpace(r.FormValue("property_id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Property", "Missing property identifier.")
		return
	}
	step2URL := onboardingURL(propID, 2, false)

	ratePence, err := domain.ParseCurrencyMajorToMinor(r.FormValue("base_nightly_rate"))
	if err != nil {
		h.redirectErr(w, r, step2URL, fmt.Errorf("nightly price: %w", err))
		return
	}

	var amenities []string
	for _, part := range strings.Split(r.FormValue("amenities"), ",") {
		if s := strings.TrimSpace(part); s != "" {
			amenities = append(amenities, s)
		}
	}

	ut, units, err := h.svc.AddOnboardingAccommodationBatch(r.Context(), actor, service.OnboardingAccommodationBatchInput{
		PropertyID:           propID,
		Name:                 r.FormValue("name"),
		Code:                 r.FormValue("code"),
		Category:             domain.UnitCategory(r.FormValue("category")),
		Description:          r.FormValue("description"),
		Quantity:             parseIntDefault(r.FormValue("quantity"), 1),
		UnitPrefix:           r.FormValue("unit_prefix"),
		Zone:                 r.FormValue("zone"),
		BaseCapacityAdults:   parseIntDefault(r.FormValue("base_capacity_adults"), 2),
		MaxAdults:            parseIntDefault(r.FormValue("max_adults"), 2),
		MaxChildren:          parseIntDefault(r.FormValue("max_children"), 0),
		MaxInfants:           parseIntDefault(r.FormValue("max_infants"), 0),
		MaxGuests:            parseIntDefault(r.FormValue("max_guests"), 2),
		PetsAllowed:          r.FormValue("pets_allowed") == "true",
		MaxPets:              parseIntDefault(r.FormValue("max_pets"), 0),
		BaseNightlyRatePence: ratePence,
		MinNights:            parseIntDefault(r.FormValue("min_nights"), 1),
		MaxNights:            parseIntDefault(r.FormValue("max_nights"), 28),
		Amenities:            amenities,
	})
	if err != nil {
		h.redirectErr(w, r, step2URL, err)
		return
	}

	if r.FormValue("after_action") == "continue" {
		_, _ = h.svc.SetOnboardingStep(r.Context(), actor, propID, 3)
		redirectMsg(w, r, onboardingURL(propID, 3, false), fmt.Sprintf("Added %s (%d bookable unit(s)).", ut.Name, len(units)))
		return
	}
	redirectMsg(w, r, step2URL, fmt.Sprintf("Added %s (%d bookable unit(s)). Add another type below or continue to Step 3.", ut.Name, len(units)))
}

func (h *Handler) handleOnboardingStep2NextPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, err := uuid.Parse(strings.TrimSpace(r.FormValue("property_id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Property", "Missing property identifier.")
		return
	}
	if _, err := h.svc.SetOnboardingStep(r.Context(), actor, propID, 3); err != nil {
		h.redirectErr(w, r, onboardingURL(propID, 2, false), err)
		return
	}
	redirectMsg(w, r, onboardingURL(propID, 3, false), "Saved Step 2 progress.")
}

func (h *Handler) handleOnboardingStep3Post(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, err := uuid.Parse(strings.TrimSpace(r.FormValue("property_id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Property", "Missing property identifier.")
		return
	}
	step3URL := onboardingURL(propID, 3, false)

	extraAdultPence, err := domain.ParseCurrencyMajorToMinor(r.FormValue("extra_adult_nightly"))
	if err != nil {
		h.redirectErr(w, r, step3URL, fmt.Errorf("extra adult nightly charge: %w", err))
		return
	}
	childPence, err := domain.ParseCurrencyMajorToMinor(r.FormValue("child_nightly"))
	if err != nil {
		h.redirectErr(w, r, step3URL, fmt.Errorf("child nightly charge: %w", err))
		return
	}
	infantPence, err := domain.ParseCurrencyMajorToMinor(r.FormValue("infant_nightly"))
	if err != nil {
		h.redirectErr(w, r, step3URL, fmt.Errorf("infant nightly charge: %w", err))
		return
	}

	in := service.OnboardingStep3Input{
		PropertyID:             propID,
		VATRegistered:          r.FormValue("vat_registered") == "true",
		VATNumber:              r.FormValue("vat_number"),
		ExtraAdultNightlyPence: extraAdultPence,
		ChildNightlyPence:      childPence,
		InfantNightlyPence:     infantPence,
	}

	if r.FormValue("add_peak_rule") == "true" {
		in.AddPeakRule = true
		in.PeakUnitTypeID = parseOptionalUUID(r.FormValue("peak_unit_type_id"))
		in.PeakRuleName = r.FormValue("peak_rule_name")
		sd, serr := domain.ParseDate(r.FormValue("peak_start_date"))
		ed, eerr := domain.ParseDate(r.FormValue("peak_end_date"))
		if serr != nil || eerr != nil {
			h.redirectErr(w, r, step3URL, fmt.Errorf("seasonal rule start and end dates must be valid YYYY-MM-DD dates"))
			return
		}
		in.PeakStartDate = sd
		in.PeakEndDate = ed
		peakPence, perr := domain.ParseCurrencyMajorToMinor(r.FormValue("peak_nightly_rate"))
		if perr != nil {
			h.redirectErr(w, r, step3URL, fmt.Errorf("seasonal nightly price: %w", perr))
			return
		}
		in.PeakNightlyRatePence = peakPence
		switch r.FormValue("peak_days_preset") {
		case "all":
			in.PeakDaysOfWeek = domain.DayAll
		default:
			in.PeakDaysOfWeek = domain.DayFri | domain.DaySat
		}
		if minOvr := parseIntDefault(r.FormValue("peak_min_nights"), 0); minOvr > 0 {
			in.PeakMinNights = &minOvr
		}
	}

	prop, err := h.svc.GetProperty(r.Context(), actor, propID)
	if err != nil {
		h.redirectErr(w, r, step3URL, err)
		return
	}
	preset := domain.OnboardingPresetCampsite
	if st, ok := domain.DecodeOnboardingState(prop.Settings); ok {
		preset = st.Preset
	}
	spec := domain.GetOnboardingPresetSpec(preset)

	selectedCodes := make(map[string]bool)
	for _, c := range r.Form["suggested_extras"] {
		selectedCodes[strings.ToLower(strings.TrimSpace(c))] = true
	}
	for _, sug := range spec.SuggestedExtras {
		if selectedCodes[strings.ToLower(sug.Code)] {
			pricePence := sug.PricePence
			if customRaw := strings.TrimSpace(r.FormValue("extra_price_" + sug.Code)); customRaw != "" {
				if parsed, perr := domain.ParseCurrencyMajorToMinor(customRaw); perr == nil {
					pricePence = parsed
				}
			}
			in.ExtrasToCreate = append(in.ExtrasToCreate, domain.Extra{
				Code:         sug.Code,
				Name:         sug.Name,
				Description:  sug.Description,
				PricingModel: sug.PricingModel,
				PricePence:   pricePence,
				MaxQuantity:  sug.MaxQuantity,
				IsActive:     true,
			})
		}
	}

	if customName := strings.TrimSpace(r.FormValue("custom_extra_name")); customName != "" {
		customPence, cerr := domain.ParseCurrencyMajorToMinor(r.FormValue("custom_extra_price"))
		if cerr != nil {
			h.redirectErr(w, r, step3URL, fmt.Errorf("custom extra price: %w", cerr))
			return
		}
		pm := domain.PricingModel(r.FormValue("custom_extra_pricing_model"))
		if !pm.Valid() {
			pm = domain.PricingPerStay
		}
		in.ExtrasToCreate = append(in.ExtrasToCreate, domain.Extra{
			Name:         customName,
			Description:  strings.TrimSpace(r.FormValue("custom_extra_description")),
			PricingModel: pm,
			PricePence:   customPence,
			MaxQuantity:  parseIntDefault(r.FormValue("custom_extra_max_quantity"), 2),
			IsActive:     true,
		})
	}

	if err := h.svc.SaveOnboardingPricingAndExtras(r.Context(), actor, in); err != nil {
		h.redirectErr(w, r, step3URL, err)
		return
	}
	redirectMsg(w, r, onboardingURL(propID, 4, false), "Pricing and add-on extras saved.")
}

func (h *Handler) handleOnboardingStep4Post(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, err := uuid.Parse(strings.TrimSpace(r.FormValue("property_id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Property", "Missing property identifier.")
		return
	}
	step4URL := onboardingURL(propID, 4, false)

	fixedDepositPence, err := domain.ParseCurrencyMajorToMinor(r.FormValue("deposit_fixed"))
	if err != nil {
		h.redirectErr(w, r, step4URL, fmt.Errorf("fixed deposit amount: %w", err))
		return
	}

	in := service.OnboardingStep4Input{
		PropertyID:                  propID,
		CheckInTime:                 r.FormValue("check_in_time"),
		CheckOutTime:                r.FormValue("check_out_time"),
		HoldDurationMinutes:         parseIntDefault(r.FormValue("hold_duration_minutes"), 15),
		DepositMode:                 r.FormValue("deposit_mode"),
		DepositPercentage:           parseIntDefault(r.FormValue("deposit_percentage"), 100),
		DepositFixedPence:           fixedDepositPence,
		BalanceDueDaysBeforeArrival: parseIntDefault(r.FormValue("balance_due_days_before_arrival"), 28),
		BookingTermsVersion:         r.FormValue("booking_terms_version"),
		BookingTermsURL:             r.FormValue("booking_terms_url"),
		PrivacyPolicyURL:            r.FormValue("privacy_policy_url"),
		CancellationPolicy:          r.FormValue("cancellation_policy"),
		QuietHours:                  r.FormValue("quiet_hours"),
		FirePolicy:                  r.FormValue("fire_policy"),
		PetPolicy:                   r.FormValue("pet_policy"),
		PrivacyNotice:               r.FormValue("privacy_notice"),
	}

	if err := h.svc.SaveOnboardingRulesAndPolicies(r.Context(), actor, in); err != nil {
		h.redirectErr(w, r, step4URL, err)
		return
	}
	redirectMsg(w, r, onboardingURL(propID, 5, false), "House rules and deposit settings saved.")
}

func (h *Handler) handleOnboardingStep5UserPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, err := uuid.Parse(strings.TrimSpace(r.FormValue("property_id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Property", "Missing property identifier.")
		return
	}
	step5URL := onboardingURL(propID, 5, false)

	created, err := h.svc.AddOnboardingTeamMember(
		r.Context(),
		actor,
		propID,
		r.FormValue("full_name"),
		r.FormValue("email"),
		domain.Role(r.FormValue("role")),
		r.FormValue("password"),
	)
	if err != nil {
		h.redirectErr(w, r, step5URL, err)
		return
	}
	redirectMsg(w, r, step5URL, fmt.Sprintf("Added %s (%s). They will be prompted to set their own password and save recovery codes on first sign-in.", created.FullName, created.Role))
}

func (h *Handler) handleOnboardingStep5NextPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, err := uuid.Parse(strings.TrimSpace(r.FormValue("property_id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Property", "Missing property identifier.")
		return
	}
	if _, err := h.svc.SetOnboardingStep(r.Context(), actor, propID, 6); err != nil {
		h.redirectErr(w, r, onboardingURL(propID, 5, false), err)
		return
	}
	redirectMsg(w, r, onboardingURL(propID, 6, false), "Saved team settings. Optional next step: connect an off-site cloud backup bucket, or skip for now.")
}

func (h *Handler) handleOnboardingStep6BackupPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, err := uuid.Parse(strings.TrimSpace(r.FormValue("property_id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Property", "Missing property identifier.")
		return
	}
	step6URL := onboardingURL(propID, 6, false)

	creds := map[string]string{
		"access_key_id":        r.FormValue("access_key_id"),
		"secret_access_key":    r.FormValue("secret_access_key"),
		"service_account_json": r.FormValue("service_account_json"),
		"account_name":         r.FormValue("account_name"),
		"account_key":          r.FormValue("account_key"),
		"sas_token":            r.FormValue("sas_token"),
		"archive_passphrase":   r.FormValue("archive_passphrase"),
	}
	saved, err := h.svc.SaveBackupTarget(r.Context(), actor, service.SaveBackupTargetInput{
		Version:               1,
		CurrentPassword:       r.FormValue("current_password"),
		Name:                  r.FormValue("name"),
		Provider:              domain.BackupProvider(r.FormValue("provider")),
		BucketOrContainer:     r.FormValue("bucket_or_container"),
		Region:                r.FormValue("region"),
		EndpointURL:           r.FormValue("endpoint_url"),
		KeyPrefix:             r.FormValue("key_prefix"),
		EncryptArchive:        r.FormValue("encrypt_archive") == "true",
		ScheduleEnabled:       r.FormValue("schedule_enabled") == "true",
		ScheduleIntervalHours: parseIntDefault(r.FormValue("schedule_interval_hours"), 24),
		RetentionCount:        parseIntDefault(r.FormValue("retention_count"), 14),
		IsActive:              true,
		Credentials:           creds,
	})
	if err != nil {
		h.redirectErr(w, r, step6URL, err)
		return
	}

	verifiedNote := ""
	if r.FormValue("verify_now") == "true" {
		if _, verr := h.svc.VerifyBackupTarget(r.Context(), actor, saved.ID); verr != nil {
			h.redirectErr(w, r, step6URL, fmt.Errorf("saved %q, but live bucket check failed: %w", saved.Name, verr))
			return
		}
		verifiedNote = " and verified bucket connectivity"
	}

	if r.FormValue("after_action") == "add_more" {
		redirectMsg(w, r, step6URL, fmt.Sprintf("Saved cloud backup destination %q (%s)%s.", saved.Name, saved.Provider.Label(), verifiedNote))
		return
	}
	_, _ = h.svc.SetOnboardingStep(r.Context(), actor, propID, 7)
	redirectMsg(w, r, onboardingURL(propID, 7, false), fmt.Sprintf("Saved cloud backup destination %q (%s)%s.", saved.Name, saved.Provider.Label(), verifiedNote))
}

func (h *Handler) handleOnboardingStep6NextPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, err := uuid.Parse(strings.TrimSpace(r.FormValue("property_id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Property", "Missing property identifier.")
		return
	}
	if _, err := h.svc.SetOnboardingStep(r.Context(), actor, propID, 7); err != nil {
		h.redirectErr(w, r, onboardingURL(propID, 6, false), err)
		return
	}
	redirectMsg(w, r, onboardingURL(propID, 7, false), "Ready to review your property setup and website booking code.")
}

func (h *Handler) handleOnboardingCompletePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, err := uuid.Parse(strings.TrimSpace(r.FormValue("property_id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Property", "Missing property identifier.")
		return
	}
	completed, err := h.svc.CompleteOnboarding(r.Context(), actor, propID)
	if err != nil {
		h.redirectErr(w, r, onboardingURL(propID, 7, false), err)
		return
	}
	h.setActivePropertyCookie(w, completed.ID)
	redirectMsg(w, r, "/", fmt.Sprintf("Setup complete! %s is ready in the Operations Centre.", completed.Name))
}
