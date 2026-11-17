package webui

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/branding"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/httpx"
	"github.com/tirbach/tir-bach/internal/service"
)

type loginViewData struct {
	Email string
	Error string
	Brand branding.PlatformBrand
	Theme branding.ThemeSelection
}

func (h *Handler) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpx.ActorFromContext(r.Context()); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.loginTmpl.Execute(w, loginViewData{Brand: h.brand, Theme: h.theme})
}

func (h *Handler) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "Unable to read the sign-in request.")
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	meta := httpx.MetaFromContext(r.Context())

	res, err := h.svc.Login(r.Context(), email, password, meta.IPAddress, meta.UserAgent, meta.RequestID)
	if err != nil {
		msg := "Invalid email or password."
		status := http.StatusUnauthorized
		if errors.Is(err, domain.ErrRateLimited) {
			msg = "Too many failed sign-in attempts. Please wait a few minutes."
			status = http.StatusTooManyRequests
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = h.loginTmpl.Execute(w, loginViewData{
			Email: email,
			Error: msg,
			Brand: h.brand,
			Theme: h.theme,
		})
		return
	}
	httpx.SetSessionCookie(w, h.cfg, res.SessionToken)
	if res.User.MustRotatePassword {
		http.Redirect(w, r, "/account?rotate=1", http.StatusSeeOther)
		return
	}
	landing := "/"
	actor := service.Actor{
		UserID:     res.User.ID,
		PropertyID: res.User.PropertyID,
		Email:      res.User.Email,
		FullName:   res.User.FullName,
		Role:       res.User.Role,
	}
	var propPtr *uuid.UUID
	if res.User.PropertyID != nil {
		pid := *res.User.PropertyID
		propPtr = &pid
	} else if props, err := h.svc.ListProperties(r.Context(), actor); err == nil && len(props) > 0 {
		pid := props[0].ID
		propPtr = &pid
	}
	if ws, err := h.svc.ResolveWorkspace(r.Context(), actor, propPtr); err == nil && ws.DefaultRoute != "" {
		landing = safeAdminReturnPath(ws.DefaultRoute, "/")
	}
	http.Redirect(w, r, landing, http.StatusSeeOther)
}

func (h *Handler) handleLogoutPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	_ = h.svc.Logout(r.Context(), actor)
	httpx.ClearSessionCookie(w, h.cfg)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	h.handleOperationsCentre(w, r, actor)
}

func (h *Handler) handlePropertyGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Site Settings", "property")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	policy, _ := h.svc.GetBookingPolicy(r.Context(), actor, base.Property.ID)
	exceptions, _ := h.svc.ListPaymentExceptions(r.Context(), actor, base.Property.ID)
	h.renderPage(w, "property.html", struct {
		baseView
		Policy            domain.PropertyBookingPolicy
		PaymentExceptions []domain.PaymentException
	}{
		baseView:          base,
		Policy:            policy,
		PaymentExceptions: exceptions,
	})
}

func (h *Handler) handleBookingPolicyPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Site Settings", "property")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	existingPolicy, _ := h.svc.GetBookingPolicy(r.Context(), actor, base.Property.ID)
	guestPortalEnabled := existingPolicy.GuestPortalEnabled
	if h.cfg.GuestPortalEnabled {
		guestPortalEnabled = r.FormValue("guest_portal_enabled") == "true"
	}
	policy := domain.PropertyBookingPolicy{
		PropertyID:                  base.Property.ID,
		HoldDurationMinutes:         parseIntDefault(r.FormValue("hold_duration_minutes"), 15),
		DepositMode:                 r.FormValue("deposit_mode"),
		DepositPercentage:           parseIntDefault(r.FormValue("deposit_percentage"), 100),
		DepositFixedPence:           parseInt64Default(r.FormValue("deposit_fixed_pence"), 0),
		BalanceDueDaysBeforeArrival: parseIntDefault(r.FormValue("balance_due_days_before_arrival"), 28),
		ExtraAdultNightlyPence:      parseInt64Default(r.FormValue("extra_adult_nightly_pence"), 0),
		ChildNightlyPence:           parseInt64Default(r.FormValue("child_nightly_pence"), 0),
		InfantNightlyPence:          parseInt64Default(r.FormValue("infant_nightly_pence"), 0),
		BookingTermsVersion:         r.FormValue("booking_terms_version"),
		BookingTermsURL:             r.FormValue("booking_terms_url"),
		PrivacyPolicyURL:            r.FormValue("privacy_policy_url"),
		CancellationPolicy:          r.FormValue("cancellation_policy"),
		QuietHours:                  r.FormValue("quiet_hours"),
		FirePolicy:                  r.FormValue("fire_policy"),
		PetPolicy:                   r.FormValue("pet_policy"),
		PrivacyNotice:               r.FormValue("privacy_notice"),
		StripeEnabled:               r.FormValue("stripe_enabled") == "true",
		GuestPortalEnabled:          guestPortalEnabled,
		Version:                     parseIntDefault(r.FormValue("version"), 1),
	}
	if _, err := h.svc.UpdateBookingPolicy(r.Context(), actor, policy); err != nil {
		h.redirectErr(w, r, "/property", err)
		return
	}
	redirectMsg(w, r, "/property", "Direct booking policy updated.")
}

func (h *Handler) handlePropertyPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Site Settings", "property")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	p := base.Property
	p.Name = r.FormValue("name")
	p.Tagline = r.FormValue("tagline")
	p.Region = r.FormValue("region")
	p.CountryCode = r.FormValue("country_code")
	p.AddressLine1 = r.FormValue("address_line1")
	p.Town = r.FormValue("town")
	p.Postcode = r.FormValue("postcode")
	p.Timezone = r.FormValue("timezone")
	p.Currency = r.FormValue("currency")
	p.CheckInTime = r.FormValue("check_in_time")
	p.CheckOutTime = r.FormValue("check_out_time")
	p.ContactEmail = r.FormValue("contact_email")
	p.ContactPhone = r.FormValue("contact_phone")
	p.VATRegistered = r.FormValue("vat_registered") == "true"
	p.VATNumber = r.FormValue("vat_number")
	p.Version = parseIntDefault(r.FormValue("version"), p.Version)

	if _, err := h.svc.UpdateProperty(r.Context(), actor, p); err != nil {
		h.redirectErr(w, r, "/property", err)
		return
	}
	redirectMsg(w, r, "/property", "Site settings saved.")
}

// --- Unit Types ---

func (h *Handler) handleUnitTypesGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Unit Types", "unit-types")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	uts, err := h.svc.ListUnitTypes(r.Context(), actor, base.Property.ID)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load unit types")
		return
	}
	var editing *domain.UnitType
	var editingAmenities string
	if editID := parseOptionalUUID(r.URL.Query().Get("edit")); editID != nil {
		if ut, err := h.svc.GetUnitType(r.Context(), actor, base.Property.ID, *editID); err == nil {
			editing = &ut
			editingAmenities = strings.Join(ut.Amenities, ", ")
		}
	}
	h.renderPage(w, "unit_types.html", struct {
		baseView
		UnitTypes        []domain.UnitType
		Editing          *domain.UnitType
		EditingAmenities string
	}{
		baseView:         base,
		UnitTypes:        uts,
		Editing:          editing,
		EditingAmenities: editingAmenities,
	})
}

func (h *Handler) handleUnitTypesCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Unit Types", "unit-types")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	ut := unitTypeFromForm(r, base.Property.ID, uuid.Nil)
	if _, err := h.svc.CreateUnitType(r.Context(), actor, ut); err != nil {
		h.redirectErr(w, r, "/unit-types", err)
		return
	}
	redirectMsg(w, r, "/unit-types", "Unit type created.")
}

func (h *Handler) handleUnitTypesUpdate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Unit Types", "unit-types")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	ut := unitTypeFromForm(r, base.Property.ID, id)
	ut.Version = parseIntDefault(r.FormValue("version"), 1)
	if _, err := h.svc.UpdateUnitType(r.Context(), actor, ut); err != nil {
		h.redirectErr(w, r, "/unit-types", err)
		return
	}
	redirectMsg(w, r, "/unit-types", "Unit type updated.")
}

func (h *Handler) handleUnitTypesArchive(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Unit Types", "unit-types")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	if err := h.svc.ArchiveUnitType(r.Context(), actor, base.Property.ID, id); err != nil {
		h.redirectErr(w, r, "/unit-types", err)
		return
	}
	redirectMsg(w, r, "/unit-types", "Unit type archived.")
}

func unitTypeFromForm(r *http.Request, propertyID, id uuid.UUID) domain.UnitType {
	var amenities []string
	for _, part := range strings.Split(r.FormValue("amenities"), ",") {
		if s := strings.TrimSpace(part); s != "" {
			amenities = append(amenities, s)
		}
	}
	return domain.UnitType{
		ID:                   id,
		PropertyID:           propertyID,
		Code:                 r.FormValue("code"),
		Name:                 r.FormValue("name"),
		Category:             domain.UnitCategory(r.FormValue("category")),
		Description:          r.FormValue("description"),
		BaseCapacityAdults:   parseIntDefault(r.FormValue("base_capacity_adults"), 2),
		MaxAdults:            parseIntDefault(r.FormValue("max_adults"), 2),
		MaxChildren:          parseIntDefault(r.FormValue("max_children"), 0),
		MaxInfants:           parseIntDefault(r.FormValue("max_infants"), 0),
		MaxGuests:            parseIntDefault(r.FormValue("max_guests"), 2),
		PetsAllowed:          r.FormValue("pets_allowed") == "true",
		MaxPets:              parseIntDefault(r.FormValue("max_pets"), 0),
		BaseNightlyRatePence: parseInt64Default(r.FormValue("base_nightly_rate_pence"), 0),
		TaxRateBPS:           parseIntDefault(r.FormValue("tax_rate_bps"), 0),
		MinNights:            parseIntDefault(r.FormValue("min_nights"), 1),
		MaxNights:            parseIntDefault(r.FormValue("max_nights"), 28),
		Amenities:            amenities,
		IsActive:             r.FormValue("is_active") == "true",
		SortOrder:            parseIntDefault(r.FormValue("sort_order"), 0),
	}
}

// --- Physical Units ---

func (h *Handler) handleUnitsGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Units & Pitches", "units")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	uts, _ := h.svc.ListUnitTypes(r.Context(), actor, base.Property.ID)
	units, err := h.svc.ListUnits(r.Context(), actor, base.Property.ID, nil)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load units")
		return
	}
	var editing *domain.Unit
	if editID := parseOptionalUUID(r.URL.Query().Get("edit")); editID != nil {
		if u, err := h.svc.GetUnit(r.Context(), actor, base.Property.ID, *editID); err == nil {
			editing = &u
		}
	}
	h.renderPage(w, "units.html", struct {
		baseView
		UnitTypes []domain.UnitType
		Units     []domain.Unit
		Editing   *domain.Unit
	}{
		baseView:  base,
		UnitTypes: uts,
		Units:     units,
		Editing:   editing,
	})
}

func (h *Handler) handleUnitsCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Units & Pitches", "units")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	utID, _ := uuid.Parse(r.FormValue("unit_type_id"))
	u := domain.Unit{
		PropertyID:       base.Property.ID,
		UnitTypeID:       utID,
		Code:             r.FormValue("code"),
		Name:             r.FormValue("name"),
		Zone:             r.FormValue("zone"),
		Status:           domain.UnitStatus(r.FormValue("status")),
		AccessNotes:      r.FormValue("access_notes"),
		OperationalNotes: r.FormValue("operational_notes"),
		SortOrder:        parseIntDefault(r.FormValue("sort_order"), 0),
	}
	if _, err := h.svc.CreateUnit(r.Context(), actor, u); err != nil {
		h.redirectErr(w, r, "/units", err)
		return
	}
	redirectMsg(w, r, "/units", "Unit created.")
}

func (h *Handler) handleUnitsUpdate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Units & Pitches", "units")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	utID, _ := uuid.Parse(r.FormValue("unit_type_id"))
	u := domain.Unit{
		ID:               id,
		PropertyID:       base.Property.ID,
		UnitTypeID:       utID,
		Code:             r.FormValue("code"),
		Name:             r.FormValue("name"),
		Zone:             r.FormValue("zone"),
		Status:           domain.UnitStatus(r.FormValue("status")),
		AccessNotes:      r.FormValue("access_notes"),
		OperationalNotes: r.FormValue("operational_notes"),
		SortOrder:        parseIntDefault(r.FormValue("sort_order"), 0),
		Version:          parseIntDefault(r.FormValue("version"), 1),
	}
	if _, err := h.svc.UpdateUnit(r.Context(), actor, u); err != nil {
		h.redirectErr(w, r, "/units", err)
		return
	}
	redirectMsg(w, r, "/units", "Unit updated.")
}

func (h *Handler) handleUnitsArchive(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Units & Pitches", "units")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	if err := h.svc.ArchiveUnit(r.Context(), actor, base.Property.ID, id); err != nil {
		h.redirectErr(w, r, "/units", err)
		return
	}
	redirectMsg(w, r, "/units", "Unit archived.")
}

// --- Extras ---

func (h *Handler) handleExtrasGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Extras & Add-ons", "extras")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	extras, err := h.svc.ListExtras(r.Context(), actor, base.Property.ID)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load extras")
		return
	}
	var editing *domain.Extra
	if editID := parseOptionalUUID(r.URL.Query().Get("edit")); editID != nil {
		if ex, err := h.svc.GetExtra(r.Context(), actor, base.Property.ID, *editID); err == nil {
			editing = &ex
		}
	}
	h.renderPage(w, "extras.html", struct {
		baseView
		Extras  []domain.Extra
		Editing *domain.Extra
	}{
		baseView: base,
		Extras:   extras,
		Editing:  editing,
	})
}

func (h *Handler) handleExtrasCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Extras & Add-ons", "extras")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	ex := domain.Extra{
		PropertyID:   base.Property.ID,
		Code:         r.FormValue("code"),
		Name:         r.FormValue("name"),
		Description:  r.FormValue("description"),
		PricingModel: domain.PricingModel(r.FormValue("pricing_model")),
		PricePence:   parseInt64Default(r.FormValue("price_pence"), 0),
		TaxRateBPS:   parseIntDefault(r.FormValue("tax_rate_bps"), 0),
		MaxQuantity:  parseIntDefault(r.FormValue("max_quantity"), 1),
		IsActive:     r.FormValue("is_active") == "true",
		SortOrder:    parseIntDefault(r.FormValue("sort_order"), 0),
	}
	if _, err := h.svc.CreateExtra(r.Context(), actor, ex); err != nil {
		h.redirectErr(w, r, "/extras", err)
		return
	}
	redirectMsg(w, r, "/extras", "Extra created.")
}

func (h *Handler) handleExtrasUpdate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Extras & Add-ons", "extras")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	ex := domain.Extra{
		ID:           id,
		PropertyID:   base.Property.ID,
		Code:         r.FormValue("code"),
		Name:         r.FormValue("name"),
		Description:  r.FormValue("description"),
		PricingModel: domain.PricingModel(r.FormValue("pricing_model")),
		PricePence:   parseInt64Default(r.FormValue("price_pence"), 0),
		TaxRateBPS:   parseIntDefault(r.FormValue("tax_rate_bps"), 0),
		MaxQuantity:  parseIntDefault(r.FormValue("max_quantity"), 1),
		IsActive:     r.FormValue("is_active") == "true",
		SortOrder:    parseIntDefault(r.FormValue("sort_order"), 0),
		Version:      parseIntDefault(r.FormValue("version"), 1),
	}
	if _, err := h.svc.UpdateExtra(r.Context(), actor, ex); err != nil {
		h.redirectErr(w, r, "/extras", err)
		return
	}
	redirectMsg(w, r, "/extras", "Extra updated.")
}

func (h *Handler) handleExtrasArchive(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Extras & Add-ons", "extras")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	if err := h.svc.ArchiveExtra(r.Context(), actor, base.Property.ID, id); err != nil {
		h.redirectErr(w, r, "/extras", err)
		return
	}
	redirectMsg(w, r, "/extras", "Extra archived.")
}
