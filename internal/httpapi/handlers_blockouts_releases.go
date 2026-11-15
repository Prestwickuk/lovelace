package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func (h *Handler) registerBlockoutReleaseRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/blockouts", h.requireAuth(false, h.handleListBlockouts))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/blockouts/preview", h.requireAuth(true, h.handlePreviewBlockout))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/blockouts", h.requireAuth(true, h.handleCreateBlockout))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/blockouts/{id}", h.requireAuth(false, h.handleGetBlockout))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/blockouts/{id}/release", h.requireAuth(true, h.handleReleaseBlockout))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/blockouts/{id}/reinstate", h.requireAuth(true, h.handleReinstateBlockout))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/blockouts/{id}/dates", h.requireAuth(true, h.handleUpdateBlockoutDates))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/releases", h.requireAuth(false, h.handleListReleases))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/releases", h.requireAuth(true, h.handleCreateRelease))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/releases/{id}", h.requireAuth(true, h.handleUpdateRelease))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/releases/{id}/release-now", h.requireAuth(true, h.handleManualRelease))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/releases/{id}/cancel", h.requireAuth(true, h.handleCancelRelease))
}

type blockoutRequest struct {
	ScopeType          string   `json:"scope_type"`
	UnitIDs            []string `json:"unit_ids"`
	UnitTypeID         string   `json:"unit_type_id"`
	Category           string   `json:"category"`
	ChannelScope       string   `json:"channel_scope"`
	StartDate          string   `json:"start_date"`
	EndDate            string   `json:"end_date"`
	Reason             string   `json:"reason"`
	ApplyToFutureUnits bool     `json:"apply_to_future_units"`
}

func (b blockoutRequest) toDomain(w http.ResponseWriter, propID uuid.UUID) (domain.BulkBlockoutInput, bool) {
	sDate, err := domain.ParseDate(b.StartDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "start_date must be YYYY-MM-DD")
		return domain.BulkBlockoutInput{}, false
	}
	eDate, err := domain.ParseDate(b.EndDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
		return domain.BulkBlockoutInput{}, false
	}
	unitIDs, ok := parseUUIDSlice(w, "unit_ids", b.UnitIDs)
	if !ok {
		return domain.BulkBlockoutInput{}, false
	}
	var unitTypePtr *uuid.UUID
	if b.UnitTypeID != "" {
		utID, err := uuid.Parse(b.UnitTypeID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_id", "invalid unit_type_id")
			return domain.BulkBlockoutInput{}, false
		}
		unitTypePtr = &utID
	}
	return domain.BulkBlockoutInput{
		PropertyID:         propID,
		ScopeType:          domain.BlockoutScope(b.ScopeType),
		UnitIDs:            unitIDs,
		UnitTypeID:         unitTypePtr,
		Category:           domain.ClaimType(b.Category),
		ChannelScope:       b.ChannelScope,
		StartDate:          sDate,
		EndDate:            eDate,
		Reason:             b.Reason,
		ApplyToFutureUnits: b.ApplyToFutureUnits,
	}, true
}

func (h *Handler) handleListBlockouts(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	includeReleased := r.URL.Query().Get("include_released") == "true"
	items, err := h.svc.ListBlockoutGroups(r.Context(), actor, propID, includeReleased)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

func (h *Handler) handlePreviewBlockout(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body blockoutRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	in, ok := body.toDomain(w, propID)
	if !ok {
		return
	}
	preview, err := h.svc.PreviewBulkBlockout(r.Context(), actor, in)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *Handler) handleCreateBlockout(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body blockoutRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	in, ok := body.toDomain(w, propID)
	if !ok {
		return
	}
	grp, claims, err := h.svc.CreateBulkBlockout(r.Context(), actor, in)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"group":  grp,
		"claims": nonNil(claims),
	})
}

func (h *Handler) handleGetBlockout(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	grp, claims, err := h.svc.GetBlockoutGroup(r.Context(), actor, propID, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"group":  grp,
		"claims": nonNil(claims),
	})
}

func (h *Handler) handleReleaseBlockout(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		UnitIDs []string `json:"unit_ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	unitIDs, ok := parseUUIDSlice(w, "unit_ids", body.UnitIDs)
	if !ok {
		return
	}
	grp, claims, err := h.svc.ReleaseBlockout(r.Context(), actor, propID, id, unitIDs)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"group":  grp,
		"claims": nonNil(claims),
	})
}

func (h *Handler) handleReinstateBlockout(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		ClaimIDs []string `json:"claim_ids"`
		UnitIDs  []string `json:"unit_ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	claimIDs, ok := parseUUIDSlice(w, "claim_ids", body.ClaimIDs)
	if !ok {
		return
	}
	unitIDs, ok := parseUUIDSlice(w, "unit_ids", body.UnitIDs)
	if !ok {
		return
	}
	grp, claims, err := h.svc.ReinstateBlockout(r.Context(), actor, propID, id, claimIDs, unitIDs)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"group":  grp,
		"claims": nonNil(claims),
	})
}

func (h *Handler) handleUpdateBlockoutDates(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		Version   int    `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sDate, err := domain.ParseDate(body.StartDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "start_date must be YYYY-MM-DD")
		return
	}
	eDate, err := domain.ParseDate(body.EndDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
		return
	}
	grp, claims, err := h.svc.UpdateBlockoutDates(r.Context(), actor, propID, id, sDate, eDate, body.Version)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"group":  grp,
		"claims": nonNil(claims),
	})
}

type releaseRuleRequest struct {
	ScopeType   string `json:"scope_type"`
	UnitID      string `json:"unit_id"`
	UnitTypeID  string `json:"unit_type_id"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	ChannelCode string `json:"channel_code"`
	ReleaseAt   string `json:"release_at"`
	Notes       string `json:"notes"`
	Version     int    `json:"version"`
}

func (req releaseRuleRequest) toDomain(w http.ResponseWriter, propID, ruleID uuid.UUID, tz string) (domain.AvailabilityReleaseRule, bool) {
	sDate, err := domain.ParseDate(req.StartDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "start_date must be YYYY-MM-DD")
		return domain.AvailabilityReleaseRule{}, false
	}
	eDate, err := domain.ParseDate(req.EndDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
		return domain.AvailabilityReleaseRule{}, false
	}
	relAt, err := parseTimestamp(req.ReleaseAt, tz)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_timestamp", "release_at must be RFC3339 or YYYY-MM-DDTHH:MM")
		return domain.AvailabilityReleaseRule{}, false
	}
	var unitPtr, utPtr *uuid.UUID
	if req.UnitID != "" {
		uid, err := uuid.Parse(req.UnitID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_id", "invalid unit_id")
			return domain.AvailabilityReleaseRule{}, false
		}
		unitPtr = &uid
	}
	if req.UnitTypeID != "" {
		utid, err := uuid.Parse(req.UnitTypeID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_id", "invalid unit_type_id")
			return domain.AvailabilityReleaseRule{}, false
		}
		utPtr = &utid
	}
	return domain.AvailabilityReleaseRule{
		ID:          ruleID,
		PropertyID:  propID,
		ScopeType:   domain.ReleaseScope(req.ScopeType),
		UnitID:      unitPtr,
		UnitTypeID:  utPtr,
		StartDate:   sDate,
		EndDate:     eDate,
		ChannelCode: req.ChannelCode,
		ReleaseAt:   relAt.UTC(),
		Notes:       req.Notes,
		Version:     req.Version,
	}, true
}

func (h *Handler) handleListReleases(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	includeCancelled := r.URL.Query().Get("include_cancelled") == "true"
	items, err := h.svc.ListReleaseRules(r.Context(), actor, propID, includeCancelled)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

func (h *Handler) handleCreateRelease(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body releaseRuleRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	tz := "Europe/London"
	if prop, err := h.svc.GetProperty(r.Context(), actor, propID); err == nil && prop.Timezone != "" {
		tz = prop.Timezone
	}
	rule, ok := body.toDomain(w, propID, uuid.Nil, tz)
	if !ok {
		return
	}
	created, err := h.svc.CreateReleaseRule(r.Context(), actor, rule)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleUpdateRelease(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body releaseRuleRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	tz := "Europe/London"
	if prop, err := h.svc.GetProperty(r.Context(), actor, propID); err == nil && prop.Timezone != "" {
		tz = prop.Timezone
	}
	rule, ok := body.toDomain(w, propID, id, tz)
	if !ok {
		return
	}
	updated, err := h.svc.UpdateReleaseRule(r.Context(), actor, rule)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleManualRelease(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	updated, err := h.svc.ManualReleaseRule(r.Context(), actor, propID, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleCancelRelease(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	updated, err := h.svc.CancelReleaseRule(r.Context(), actor, propID, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func parseTimestamp(raw, tz string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04", raw, loc); err == nil {
		return t.UTC(), nil
	}
	return time.ParseInLocation("2006-01-02 15:04", raw, loc)
}
