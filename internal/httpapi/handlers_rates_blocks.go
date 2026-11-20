package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func (h *Handler) handleListRatePlans(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListRatePlans(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

type ratePlanReq struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
	IsActive    *bool  `json:"is_active"`
	Priority    int    `json:"priority"`
	Version     int    `json:"version"`
}

func (h *Handler) handleCreateRatePlan(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body ratePlanReq
	if !decodeJSON(w, r, &body) {
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	created, err := h.svc.CreateRatePlan(r.Context(), actor, domain.RatePlan{
		PropertyID:  propID,
		Code:        body.Code,
		Name:        body.Name,
		Description: body.Description,
		IsDefault:   body.IsDefault,
		IsActive:    active,
		Priority:    body.Priority,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleUpdateRatePlan(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body ratePlanReq
	if !decodeJSON(w, r, &body) {
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	updated, err := h.svc.UpdateRatePlan(r.Context(), actor, domain.RatePlan{
		ID:          id,
		PropertyID:  propID,
		Code:        body.Code,
		Name:        body.Name,
		Description: body.Description,
		IsDefault:   body.IsDefault,
		IsActive:    active,
		Priority:    body.Priority,
		Version:     body.Version,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleListRateRules(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var rpID, utID *uuid.UUID
	if raw := r.URL.Query().Get("rate_plan_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_param", "invalid rate_plan_id")
			return
		}
		rpID = &id
	}
	if raw := r.URL.Query().Get("unit_type_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_param", "invalid unit_type_id")
			return
		}
		utID = &id
	}
	items, err := h.svc.ListRateRules(r.Context(), actor, propID, rpID, utID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

type rateRuleReq struct {
	RatePlanID        uuid.UUID             `json:"rate_plan_id"`
	UnitTypeID        uuid.UUID             `json:"unit_type_id"`
	Name              string                `json:"name"`
	StartDate         string                `json:"start_date"`
	EndDate           string                `json:"end_date"`
	DaysOfWeek        domain.DaysOfWeekMask `json:"days_of_week"`
	NightlyRatePence  int64                 `json:"nightly_rate_pence"`
	MinNightsOverride *int                  `json:"min_nights_override"`
	Priority          int                   `json:"priority"`
	IsActive          *bool                 `json:"is_active"`
	Version           int                   `json:"version"`
}

func (h *Handler) handleCreateRateRule(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body rateRuleReq
	if !decodeJSON(w, r, &body) {
		return
	}
	sd, err := domain.ParseDate(body.StartDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"start_date": err.Error()}))
		return
	}
	ed, err := domain.ParseDate(body.EndDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"end_date": err.Error()}))
		return
	}
	dow := body.DaysOfWeek
	if dow == 0 {
		dow = domain.DayAll
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	created, err := h.svc.CreateRateRule(r.Context(), actor, domain.RateRule{
		PropertyID:        propID,
		RatePlanID:        body.RatePlanID,
		UnitTypeID:        body.UnitTypeID,
		Name:              body.Name,
		StartDate:         sd,
		EndDate:           ed,
		DaysOfWeek:        dow,
		NightlyRatePence:  body.NightlyRatePence,
		MinNightsOverride: body.MinNightsOverride,
		Priority:          body.Priority,
		IsActive:          active,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleUpdateRateRule(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body rateRuleReq
	if !decodeJSON(w, r, &body) {
		return
	}
	sd, err := domain.ParseDate(body.StartDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"start_date": err.Error()}))
		return
	}
	ed, err := domain.ParseDate(body.EndDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"end_date": err.Error()}))
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	updated, err := h.svc.UpdateRateRule(r.Context(), actor, domain.RateRule{
		ID:                id,
		PropertyID:        propID,
		RatePlanID:        body.RatePlanID,
		UnitTypeID:        body.UnitTypeID,
		Name:              body.Name,
		StartDate:         sd,
		EndDate:           ed,
		DaysOfWeek:        body.DaysOfWeek,
		NightlyRatePence:  body.NightlyRatePence,
		MinNightsOverride: body.MinNightsOverride,
		Priority:          body.Priority,
		IsActive:          active,
		Version:           body.Version,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleArchiveRateRule(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.ArchiveRateRule(r.Context(), actor, propID, id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "archived"})
}

func (h *Handler) handleRatePreview(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	utID, err := uuid.Parse(q.Get("unit_type_id"))
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"unit_type_id": "must be valid UUID"}))
		return
	}
	rpID, err := uuid.Parse(q.Get("rate_plan_id"))
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"rate_plan_id": "must be valid UUID"}))
		return
	}
	sd, err := domain.ParseDate(q.Get("start_date"))
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"start_date": err.Error()}))
		return
	}
	ed, err := domain.ParseDate(q.Get("end_date"))
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"end_date": err.Error()}))
		return
	}
	preview, err := h.svc.PreviewRates(r.Context(), actor, propID, utID, rpID, sd, ed)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

// --- Availability Blocks ---

func (h *Handler) handleListBlocks(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var unitID *uuid.UUID
	if raw := r.URL.Query().Get("unit_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_param", "invalid unit_id")
			return
		}
		unitID = &id
	}
	includeReleased := r.URL.Query().Get("include_released") == "true"
	items, err := h.svc.ListAvailabilityBlocks(r.Context(), actor, propID, unitID, includeReleased)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

func (h *Handler) handleCreateBlock(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		UnitID    uuid.UUID        `json:"unit_id"`
		StartDate string           `json:"start_date"`
		EndDate   string           `json:"end_date"`
		BlockType domain.BlockType `json:"block_type"`
		Reason    string           `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sd, err := domain.ParseDate(body.StartDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"start_date": err.Error()}))
		return
	}
	ed, err := domain.ParseDate(body.EndDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"end_date": err.Error()}))
		return
	}
	created, err := h.svc.CreateAvailabilityBlock(r.Context(), actor, domain.AvailabilityBlock{
		PropertyID: propID,
		UnitID:     body.UnitID,
		StartDate:  sd,
		EndDate:    ed,
		BlockType:  body.BlockType,
		Reason:     body.Reason,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleReleaseBlock(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	released, err := h.svc.ReleaseAvailabilityBlock(r.Context(), actor, propID, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, released)
}
