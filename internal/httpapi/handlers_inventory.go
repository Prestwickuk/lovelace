package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func (h *Handler) handleListProperties(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	props, err := h.svc.ListProperties(r.Context(), actor)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(props)})
}

func (h *Handler) handleGetProperty(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	p, err := h.svc.GetProperty(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) handleUpdateProperty(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		Name          string          `json:"name"`
		Tagline       string          `json:"tagline"`
		CountryCode   string          `json:"country_code"`
		Region        string          `json:"region"`
		AddressLine1  string          `json:"address_line1"`
		AddressLine2  string          `json:"address_line2"`
		Town          string          `json:"town"`
		Postcode      string          `json:"postcode"`
		Timezone      string          `json:"timezone"`
		Currency      string          `json:"currency"`
		CheckInTime   string          `json:"check_in_time"`
		CheckOutTime  string          `json:"check_out_time"`
		VATRegistered bool            `json:"vat_registered"`
		VATNumber     string          `json:"vat_number"`
		ContactEmail  string          `json:"contact_email"`
		ContactPhone  string          `json:"contact_phone"`
		Settings      json.RawMessage `json:"settings"`
		Version       int             `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	existing, err := h.svc.GetProperty(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	p := domain.Property{
		ID:            propID,
		Slug:          existing.Slug,
		Name:          body.Name,
		Tagline:       body.Tagline,
		CountryCode:   body.CountryCode,
		Region:        body.Region,
		AddressLine1:  body.AddressLine1,
		AddressLine2:  body.AddressLine2,
		Town:          body.Town,
		Postcode:      body.Postcode,
		Timezone:      body.Timezone,
		Currency:      body.Currency,
		CheckInTime:   body.CheckInTime,
		CheckOutTime:  body.CheckOutTime,
		VATRegistered: body.VATRegistered,
		VATNumber:     body.VATNumber,
		ContactEmail:  body.ContactEmail,
		ContactPhone:  body.ContactPhone,
		Settings:      body.Settings,
		Version:       body.Version,
	}
	updated, err := h.svc.UpdateProperty(r.Context(), actor, p)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// --- Unit Types ---

func (h *Handler) handleListUnitTypes(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListUnitTypes(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

type unitTypeReq struct {
	Code                 string              `json:"code"`
	Name                 string              `json:"name"`
	Category             domain.UnitCategory `json:"category"`
	Description          string              `json:"description"`
	BaseCapacityAdults   int                 `json:"base_capacity_adults"`
	MaxAdults            int                 `json:"max_adults"`
	MaxChildren          int                 `json:"max_children"`
	MaxInfants           int                 `json:"max_infants"`
	MaxGuests            int                 `json:"max_guests"`
	PetsAllowed          bool                `json:"pets_allowed"`
	MaxPets              int                 `json:"max_pets"`
	BaseNightlyRatePence int64               `json:"base_nightly_rate_pence"`
	TaxRateBPS           int                 `json:"tax_rate_bps"`
	MinNights            int                 `json:"min_nights"`
	MaxNights            int                 `json:"max_nights"`
	Amenities            []string            `json:"amenities"`
	Metadata             json.RawMessage     `json:"metadata"`
	IsActive             *bool               `json:"is_active"`
	SortOrder            int                 `json:"sort_order"`
	Version              int                 `json:"version"`
}

func (h *Handler) handleCreateUnitType(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body unitTypeReq
	if !decodeJSON(w, r, &body) {
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	created, err := h.svc.CreateUnitType(r.Context(), actor, domain.UnitType{
		PropertyID:           propID,
		Code:                 body.Code,
		Name:                 body.Name,
		Category:             body.Category,
		Description:          body.Description,
		BaseCapacityAdults:   body.BaseCapacityAdults,
		MaxAdults:            body.MaxAdults,
		MaxChildren:          body.MaxChildren,
		MaxInfants:           body.MaxInfants,
		MaxGuests:            body.MaxGuests,
		PetsAllowed:          body.PetsAllowed,
		MaxPets:              body.MaxPets,
		BaseNightlyRatePence: body.BaseNightlyRatePence,
		TaxRateBPS:           body.TaxRateBPS,
		MinNights:            body.MinNights,
		MaxNights:            body.MaxNights,
		Amenities:            body.Amenities,
		Metadata:             body.Metadata,
		IsActive:             active,
		SortOrder:            body.SortOrder,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleUpdateUnitType(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body unitTypeReq
	if !decodeJSON(w, r, &body) {
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	updated, err := h.svc.UpdateUnitType(r.Context(), actor, domain.UnitType{
		ID:                   id,
		PropertyID:           propID,
		Code:                 body.Code,
		Name:                 body.Name,
		Category:             body.Category,
		Description:          body.Description,
		BaseCapacityAdults:   body.BaseCapacityAdults,
		MaxAdults:            body.MaxAdults,
		MaxChildren:          body.MaxChildren,
		MaxInfants:           body.MaxInfants,
		MaxGuests:            body.MaxGuests,
		PetsAllowed:          body.PetsAllowed,
		MaxPets:              body.MaxPets,
		BaseNightlyRatePence: body.BaseNightlyRatePence,
		TaxRateBPS:           body.TaxRateBPS,
		MinNights:            body.MinNights,
		MaxNights:            body.MaxNights,
		Amenities:            body.Amenities,
		Metadata:             body.Metadata,
		IsActive:             active,
		SortOrder:            body.SortOrder,
		Version:              body.Version,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleArchiveUnitType(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.ArchiveUnitType(r.Context(), actor, propID, id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "archived"})
}

// --- Physical Units ---

func (h *Handler) handleListUnits(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var utID *uuid.UUID
	if raw := r.URL.Query().Get("unit_type_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_param", "invalid unit_type_id")
			return
		}
		utID = &id
	}
	items, err := h.svc.ListUnits(r.Context(), actor, propID, utID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

type unitReq struct {
	UnitTypeID       uuid.UUID         `json:"unit_type_id"`
	Code             string            `json:"code"`
	Name             string            `json:"name"`
	Zone             string            `json:"zone"`
	Status           domain.UnitStatus `json:"status"`
	AccessNotes      string            `json:"access_notes"`
	OperationalNotes string            `json:"operational_notes"`
	Attributes       json.RawMessage   `json:"attributes"`
	SortOrder        int               `json:"sort_order"`
	Version          int               `json:"version"`
}

func (h *Handler) handleCreateUnit(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body unitReq
	if !decodeJSON(w, r, &body) {
		return
	}
	created, err := h.svc.CreateUnit(r.Context(), actor, domain.Unit{
		PropertyID:       propID,
		UnitTypeID:       body.UnitTypeID,
		Code:             body.Code,
		Name:             body.Name,
		Zone:             body.Zone,
		Status:           body.Status,
		AccessNotes:      body.AccessNotes,
		OperationalNotes: body.OperationalNotes,
		Attributes:       body.Attributes,
		SortOrder:        body.SortOrder,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleUpdateUnit(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body unitReq
	if !decodeJSON(w, r, &body) {
		return
	}
	updated, err := h.svc.UpdateUnit(r.Context(), actor, domain.Unit{
		ID:               id,
		PropertyID:       propID,
		UnitTypeID:       body.UnitTypeID,
		Code:             body.Code,
		Name:             body.Name,
		Zone:             body.Zone,
		Status:           body.Status,
		AccessNotes:      body.AccessNotes,
		OperationalNotes: body.OperationalNotes,
		Attributes:       body.Attributes,
		SortOrder:        body.SortOrder,
		Version:          body.Version,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleArchiveUnit(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.ArchiveUnit(r.Context(), actor, propID, id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "archived"})
}

// --- Extras ---

func (h *Handler) handleListExtras(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListExtras(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

type extraReq struct {
	Code         string              `json:"code"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	PricingModel domain.PricingModel `json:"pricing_model"`
	PricePence   int64               `json:"price_pence"`
	TaxRateBPS   int                 `json:"tax_rate_bps"`
	MaxQuantity  int                 `json:"max_quantity"`
	IsActive     *bool               `json:"is_active"`
	SortOrder    int                 `json:"sort_order"`
	Metadata     json.RawMessage     `json:"metadata"`
	Version      int                 `json:"version"`
}

func (h *Handler) handleCreateExtra(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body extraReq
	if !decodeJSON(w, r, &body) {
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	created, err := h.svc.CreateExtra(r.Context(), actor, domain.Extra{
		PropertyID:   propID,
		Code:         body.Code,
		Name:         body.Name,
		Description:  body.Description,
		PricingModel: body.PricingModel,
		PricePence:   body.PricePence,
		TaxRateBPS:   body.TaxRateBPS,
		MaxQuantity:  body.MaxQuantity,
		IsActive:     active,
		SortOrder:    body.SortOrder,
		Metadata:     body.Metadata,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleUpdateExtra(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body extraReq
	if !decodeJSON(w, r, &body) {
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	updated, err := h.svc.UpdateExtra(r.Context(), actor, domain.Extra{
		ID:           id,
		PropertyID:   propID,
		Code:         body.Code,
		Name:         body.Name,
		Description:  body.Description,
		PricingModel: body.PricingModel,
		PricePence:   body.PricePence,
		TaxRateBPS:   body.TaxRateBPS,
		MaxQuantity:  body.MaxQuantity,
		IsActive:     active,
		SortOrder:    body.SortOrder,
		Metadata:     body.Metadata,
		Version:      body.Version,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleArchiveExtra(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.ArchiveExtra(r.Context(), actor, propID, id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "archived"})
}
