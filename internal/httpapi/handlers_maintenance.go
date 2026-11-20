package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
	"github.com/Prestwickuk/lovelace/internal/storage"
)

func (h *Handler) registerMaintenanceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/operations/maintenance-summary", h.requireAuth(false, h.handleGetOpsMaintenanceSummary))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/maintenance", h.requireAuth(false, h.handleListMaintenanceRequests))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance", h.requireAuth(true, h.handleCreateMaintenanceRequest))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/maintenance/{requestID}", h.requireAuth(false, h.handleGetMaintenanceRequest))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/maintenance/{requestID}", h.requireAuth(true, h.handleUpdateMaintenanceRequest))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/{requestID}/assign", h.requireAuth(true, h.handleAssignMaintenanceRequest))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/{requestID}/transition", h.requireAuth(true, h.handleTransitionMaintenanceRequest))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/{requestID}/work-notes", h.requireAuth(true, h.handleAddMaintenanceWorkNote))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/{requestID}/inspection", h.requireAuth(true, h.handleRecordMaintenanceInspection))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/{requestID}/attachments", h.requireAuth(true, h.handleUploadMaintenanceAttachment))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/maintenance/attachments/{attachmentID}", h.requireAuth(false, h.handleDownloadMaintenanceAttachment))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/{requestID}/inventory-restriction", h.requireAuth(true, h.handleCreateMaintenanceRestriction))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/maintenance/{requestID}/inventory-restriction/{linkID}", h.requireAuth(true, h.handleUpdateMaintenanceRestriction))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/{requestID}/release-restriction", h.requireAuth(true, h.handleReleaseMaintenanceRestriction))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/maintenance/categories", h.requireAuth(false, h.handleListMaintenanceCategories))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/categories", h.requireAuth(true, h.handleCreateMaintenanceCategory))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/maintenance/categories/{id}", h.requireAuth(true, h.handleUpdateMaintenanceCategory))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/maintenance/locations", h.requireAuth(false, h.handleListMaintenanceLocations))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/locations", h.requireAuth(true, h.handleCreateMaintenanceLocation))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/maintenance/locations/{id}", h.requireAuth(true, h.handleUpdateMaintenanceLocation))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/maintenance/assets", h.requireAuth(false, h.handleListMaintenanceAssets))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/assets", h.requireAuth(true, h.handleCreateMaintenanceAsset))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/maintenance/assets/{id}", h.requireAuth(false, h.handleGetMaintenanceAsset))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/maintenance/assets/{id}", h.requireAuth(true, h.handleUpdateMaintenanceAsset))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/maintenance/contractors", h.requireAuth(false, h.handleListMaintenanceContractors))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/contractors", h.requireAuth(true, h.handleCreateMaintenanceContractor))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/maintenance/contractors/{id}", h.requireAuth(true, h.handleUpdateMaintenanceContractor))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/maintenance/schedules", h.requireAuth(false, h.handleListMaintenanceSchedules))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/schedules", h.requireAuth(true, h.handleCreateMaintenanceSchedule))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/maintenance/schedules/{id}", h.requireAuth(true, h.handleUpdateMaintenanceSchedule))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/maintenance/schedules/run", h.requireAuth(true, h.handleRunMaintenanceSchedules))
}

func parseOptionalQueryUUID(r *http.Request, name string) (*uuid.UUID, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, false
	}
	return &id, true
}

func parseOptionalBodyDate(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	d, err := domain.ParseDate(raw)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (h *Handler) handleGetOpsMaintenanceSummary(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	opDate, err := parseOptionalBodyDate(r.URL.Query().Get("date"))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "date must be YYYY-MM-DD")
		return
	}
	sum, err := h.svc.GetOpsMaintenanceSummary(r.Context(), actor, propID, opDate)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (h *Handler) handleListMaintenanceRequests(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	catID, ok := parseOptionalQueryUUID(r, "category_id")
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid_uuid", "invalid category_id")
		return
	}
	locID, ok := parseOptionalQueryUUID(r, "location_id")
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid_uuid", "invalid location_id")
		return
	}
	unitID, ok := parseOptionalQueryUUID(r, "unit_id")
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid_uuid", "invalid unit_id")
		return
	}
	assetID, ok := parseOptionalQueryUUID(r, "asset_id")
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid_uuid", "invalid asset_id")
		return
	}
	assignedTo, ok := parseOptionalQueryUUID(r, "assigned_to")
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "invalid_uuid", "invalid assigned_to")
		return
	}
	dateFrom, err := parseOptionalBodyDate(q.Get("date_from"))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "date_from must be YYYY-MM-DD")
		return
	}
	dateTo, err := parseOptionalBodyDate(q.Get("date_to"))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "date_to must be YYYY-MM-DD")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))

	items, err := h.svc.ListMaintenanceRequests(r.Context(), actor, propID, domain.MaintenanceRequestFilter{
		Status:      q.Get("status"),
		Priority:    q.Get("priority"),
		CategoryID:  catID,
		LocationID:  locID,
		UnitID:      unitID,
		AssetID:     assetID,
		AssignedTo:  assignedTo,
		WorkType:    q.Get("work_type"),
		OverdueOnly: q.Get("overdue") == "true" || q.Get("overdue") == "1",
		Restricted:  q.Get("restricted") == "true" || q.Get("restricted") == "1",
		DateFrom:    dateFrom,
		DateTo:      dateTo,
		Search:      q.Get("q"),
		Limit:       limit,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": nonNil(items)})
}

func (h *Handler) handleCreateMaintenanceRequest(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		Title                string                               `json:"title"`
		Description          string                               `json:"description"`
		CategoryID           uuid.UUID                            `json:"category_id"`
		Priority             domain.MaintenancePriority           `json:"priority"`
		WorkType             domain.MaintenanceWorkType           `json:"work_type"`
		LocationID           *uuid.UUID                           `json:"location_id"`
		UnitID               *uuid.UUID                           `json:"unit_id"`
		AssetID              *uuid.UUID                           `json:"asset_id"`
		AssignedUserID       *uuid.UUID                           `json:"assigned_user_id"`
		AssignedContractorID *uuid.UUID                           `json:"assigned_contractor_id"`
		TargetDate           string                               `json:"target_date"`
		ScheduledFor         string                               `json:"scheduled_for"`
		GuestImpact          bool                                 `json:"guest_impact"`
		SafetyImpact         bool                                 `json:"safety_impact"`
		AvailabilityImpact   domain.MaintenanceAvailabilityImpact `json:"availability_impact"`
		InspectionRequired   *bool                                `json:"inspection_required"`
		InternalNotes        string                               `json:"internal_notes"`
		CreateRestriction    bool                                 `json:"create_restriction"`
		RestrictionStartDate string                               `json:"restriction_start_date"`
		RestrictionEndDate   string                               `json:"restriction_end_date"`
		RestrictionNotes     string                               `json:"restriction_notes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	targetDate, err := parseOptionalBodyDate(body.TargetDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "target_date must be YYYY-MM-DD")
		return
	}
	scheduledFor, err := parseOptionalBodyDate(body.ScheduledFor)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "scheduled_for must be YYYY-MM-DD")
		return
	}
	resStart, err := parseOptionalBodyDate(body.RestrictionStartDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "restriction_start_date must be YYYY-MM-DD")
		return
	}
	resEnd, err := parseOptionalBodyDate(body.RestrictionEndDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "restriction_end_date must be YYYY-MM-DD")
		return
	}
	inspReq := true
	if body.InspectionRequired != nil {
		inspReq = *body.InspectionRequired
	}

	detail, err := h.svc.CreateMaintenanceRequest(r.Context(), actor, service.CreateMaintenanceRequestInput{
		PropertyID:           propID,
		Title:                body.Title,
		Description:          body.Description,
		CategoryID:           body.CategoryID,
		Priority:             body.Priority,
		WorkType:             body.WorkType,
		LocationID:           body.LocationID,
		UnitID:               body.UnitID,
		AssetID:              body.AssetID,
		AssignedUserID:       body.AssignedUserID,
		AssignedContractorID: body.AssignedContractorID,
		TargetDate:           targetDate,
		ScheduledFor:         scheduledFor,
		GuestImpact:          body.GuestImpact,
		SafetyImpact:         body.SafetyImpact,
		AvailabilityImpact:   body.AvailabilityImpact,
		InspectionRequired:   inspReq,
		InternalNotes:        body.InternalNotes,
		CreateRestriction:    body.CreateRestriction,
		RestrictionStartDate: resStart,
		RestrictionEndDate:   resEnd,
		RestrictionNotes:     body.RestrictionNotes,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, detail)
}

func (h *Handler) handleGetMaintenanceRequest(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	reqID, ok := parseUUIDParam(w, r, "requestID")
	if !ok {
		return
	}
	detail, err := h.svc.GetMaintenanceRequestDetail(r.Context(), actor, propID, reqID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) handleUpdateMaintenanceRequest(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	reqID, ok := parseUUIDParam(w, r, "requestID")
	if !ok {
		return
	}
	var body struct {
		Title              string                               `json:"title"`
		Description        string                               `json:"description"`
		CategoryID         uuid.UUID                            `json:"category_id"`
		Priority           domain.MaintenancePriority           `json:"priority"`
		LocationID         *uuid.UUID                           `json:"location_id"`
		UnitID             *uuid.UUID                           `json:"unit_id"`
		AssetID            *uuid.UUID                           `json:"asset_id"`
		TargetDate         string                               `json:"target_date"`
		ScheduledFor       string                               `json:"scheduled_for"`
		GuestImpact        bool                                 `json:"guest_impact"`
		SafetyImpact       bool                                 `json:"safety_impact"`
		AvailabilityImpact domain.MaintenanceAvailabilityImpact `json:"availability_impact"`
		InspectionRequired bool                                 `json:"inspection_required"`
		InternalNotes      string                               `json:"internal_notes"`
		Version            int                                  `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	targetDate, err := parseOptionalBodyDate(body.TargetDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "target_date must be YYYY-MM-DD")
		return
	}
	scheduledFor, err := parseOptionalBodyDate(body.ScheduledFor)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "scheduled_for must be YYYY-MM-DD")
		return
	}
	updated, err := h.svc.UpdateMaintenanceRequest(r.Context(), actor, domain.MaintenanceRequest{
		ID:                 reqID,
		PropertyID:         propID,
		Title:              body.Title,
		Description:        body.Description,
		CategoryID:         body.CategoryID,
		Priority:           body.Priority,
		LocationID:         body.LocationID,
		UnitID:             body.UnitID,
		AssetID:            body.AssetID,
		TargetDate:         targetDate,
		ScheduledFor:       scheduledFor,
		GuestImpact:        body.GuestImpact,
		SafetyImpact:       body.SafetyImpact,
		AvailabilityImpact: body.AvailabilityImpact,
		InspectionRequired: body.InspectionRequired,
		InternalNotes:      body.InternalNotes,
		Version:            body.Version,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleAssignMaintenanceRequest(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	reqID, ok := parseUUIDParam(w, r, "requestID")
	if !ok {
		return
	}
	var body struct {
		AssignedUserID       *uuid.UUID `json:"assigned_user_id"`
		AssignedContractorID *uuid.UUID `json:"assigned_contractor_id"`
		DueDate              string     `json:"due_date"`
		Notes                string     `json:"notes"`
		Version              int        `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	dueDate, err := parseOptionalBodyDate(body.DueDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "due_date must be YYYY-MM-DD")
		return
	}
	detail, err := h.svc.AssignMaintenanceRequest(r.Context(), actor, propID, reqID, body.AssignedUserID, body.AssignedContractorID, dueDate, body.Notes, body.Version)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) handleTransitionMaintenanceRequest(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	reqID, ok := parseUUIDParam(w, r, "requestID")
	if !ok {
		return
	}
	var body struct {
		Status       domain.MaintenanceStatus `json:"status"`
		Reason       string                   `json:"reason"`
		ScheduledFor string                   `json:"scheduled_for"`
		Version      int                      `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	scheduledFor, err := parseOptionalBodyDate(body.ScheduledFor)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "scheduled_for must be YYYY-MM-DD")
		return
	}
	detail, err := h.svc.TransitionMaintenanceStatus(r.Context(), actor, propID, reqID, body.Status, body.Reason, scheduledFor, body.Version)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) handleAddMaintenanceWorkNote(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	reqID, ok := parseUUIDParam(w, r, "requestID")
	if !ok {
		return
	}
	var body struct {
		EntryType domain.MaintenanceWorkEntryType `json:"entry_type"`
		Body      string                          `json:"body"`
		PartsUsed string                          `json:"parts_used"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	entry, err := h.svc.AddMaintenanceWorkEntry(r.Context(), actor, propID, reqID, body.EntryType, body.Body, body.PartsUsed)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

func (h *Handler) handleRecordMaintenanceInspection(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	reqID, ok := parseUUIDParam(w, r, "requestID")
	if !ok {
		return
	}
	var body struct {
		Result        domain.MaintenanceInspectionResult `json:"result"`
		Notes         string                             `json:"notes"`
		CloseIfPassed bool                               `json:"close_if_passed"`
		Version       int                                `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	detail, insp, err := h.svc.RecordMaintenanceInspection(r.Context(), actor, propID, reqID, body.Result, body.Notes, body.CloseIfPassed, body.Version)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"inspection": insp,
		"detail":     detail,
	})
}

func (h *Handler) handleUploadMaintenanceAttachment(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	reqID, ok := parseUUIDParam(w, r, "requestID")
	if !ok {
		return
	}
	maxBytes := h.cfg.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+128*1024)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "upload exceeds limit or malformed multipart form")
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "missing_file", "form field 'file' is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read_failed", "unable to read uploaded file")
		return
	}
	if int64(len(data)) > maxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", "attachment exceeds maximum allowed size")
		return
	}
	var workEntryID *uuid.UUID
	if rawWE := strings.TrimSpace(r.FormValue("work_entry_id")); rawWE != "" {
		id, err := uuid.Parse(rawWE)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_uuid", "invalid work_entry_id")
			return
		}
		workEntryID = &id
	}
	allowOnReport := r.FormValue("on_report") == "1" || r.FormValue("on_report") == "true"
	att, err := h.svc.UploadMaintenanceAttachment(r.Context(), actor, propID, reqID, workEntryID, header.Filename, data, r.FormValue("caption"), allowOnReport)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, att)
}

func (h *Handler) handleDownloadMaintenanceAttachment(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	attID, ok := parseUUIDParam(w, r, "attachmentID")
	if !ok {
		return
	}
	att, rc, err := h.svc.OpenMaintenanceAttachment(r.Context(), actor, propID, attID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	defer rc.Close()

	ServeMaintenanceAttachment(w, att, rc, r.URL.Query().Get("inline") == "1")
}

// ServeMaintenanceAttachment safely streams a private attachment for authenticated callers.
func ServeMaintenanceAttachment(w http.ResponseWriter, att domain.MaintenanceAttachment, rc io.ReadCloser, inlineRequested bool) {
	disposition := "attachment"
	if inlineRequested && storage.IsInlinePreviewMIME(att.ContentType) {
		disposition = "inline"
	}
	safeName := storage.SanitizeDownloadFilename(att.OriginalFilename)
	w.Header().Set("Content-Type", att.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, safeName))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store, private")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func (h *Handler) handleCreateMaintenanceRestriction(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	reqID, ok := parseUUIDParam(w, r, "requestID")
	if !ok {
		return
	}
	var body struct {
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		Notes     string `json:"notes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	start, err := domain.ParseDate(body.StartDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "start_date must be YYYY-MM-DD")
		return
	}
	end, err := domain.ParseDate(body.EndDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "end_date must be YYYY-MM-DD")
		return
	}
	link, err := h.svc.CreateMaintenanceRestriction(r.Context(), actor, propID, reqID, start, end, body.Notes)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, link)
}

func (h *Handler) handleUpdateMaintenanceRestriction(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	reqID, ok := parseUUIDParam(w, r, "requestID")
	if !ok {
		return
	}
	linkID, ok := parseUUIDParam(w, r, "linkID")
	if !ok {
		return
	}
	var body struct {
		StartDate    string `json:"start_date"`
		EndDate      string `json:"end_date"`
		GroupVersion int    `json:"group_version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	start, err := domain.ParseDate(body.StartDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "start_date must be YYYY-MM-DD")
		return
	}
	end, err := domain.ParseDate(body.EndDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "end_date must be YYYY-MM-DD")
		return
	}
	link, err := h.svc.UpdateMaintenanceRestrictionDates(r.Context(), actor, propID, reqID, linkID, start, end, body.GroupVersion)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, link)
}

func (h *Handler) handleReleaseMaintenanceRestriction(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	reqID, ok := parseUUIDParam(w, r, "requestID")
	if !ok {
		return
	}
	var body struct {
		LinkID uuid.UUID `json:"link_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	link, err := h.svc.ReleaseMaintenanceRestriction(r.Context(), actor, propID, reqID, body.LinkID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, link)
}

// ---------------- Config & Facilities Endpoints ----------------

func (h *Handler) handleListMaintenanceCategories(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListMaintenanceCategories(r.Context(), actor, propID, r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": nonNil(items)})
}

func (h *Handler) handleCreateMaintenanceCategory(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		Description string `json:"description"`
		IsActive    *bool  `json:"is_active"`
		SortOrder   int    `json:"sort_order"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	out, err := h.svc.CreateMaintenanceCategory(r.Context(), actor, domain.MaintenanceCategory{
		PropertyID:  propID,
		Code:        body.Code,
		Name:        body.Name,
		Description: body.Description,
		IsActive:    active,
		SortOrder:   body.SortOrder,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleUpdateMaintenanceCategory(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		Description string `json:"description"`
		IsActive    bool   `json:"is_active"`
		SortOrder   int    `json:"sort_order"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	out, err := h.svc.UpdateMaintenanceCategory(r.Context(), actor, domain.MaintenanceCategory{
		ID:          id,
		PropertyID:  propID,
		Code:        body.Code,
		Name:        body.Name,
		Description: body.Description,
		IsActive:    body.IsActive,
		SortOrder:   body.SortOrder,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handleListMaintenanceLocations(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListMaintenanceLocations(r.Context(), actor, propID, r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"locations": nonNil(items)})
}

func (h *Handler) handleCreateMaintenanceLocation(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		Code         string     `json:"code"`
		Name         string     `json:"name"`
		LocationType string     `json:"location_type"`
		Description  string     `json:"description"`
		UnitID       *uuid.UUID `json:"unit_id"`
		IsActive     *bool      `json:"is_active"`
		SortOrder    int        `json:"sort_order"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	out, err := h.svc.CreateMaintenanceLocation(r.Context(), actor, domain.MaintenanceLocation{
		PropertyID:   propID,
		Code:         body.Code,
		Name:         body.Name,
		LocationType: body.LocationType,
		Description:  body.Description,
		UnitID:       body.UnitID,
		IsActive:     active,
		SortOrder:    body.SortOrder,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleUpdateMaintenanceLocation(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Code         string     `json:"code"`
		Name         string     `json:"name"`
		LocationType string     `json:"location_type"`
		Description  string     `json:"description"`
		UnitID       *uuid.UUID `json:"unit_id"`
		IsActive     bool       `json:"is_active"`
		SortOrder    int        `json:"sort_order"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	out, err := h.svc.UpdateMaintenanceLocation(r.Context(), actor, domain.MaintenanceLocation{
		ID:           id,
		PropertyID:   propID,
		Code:         body.Code,
		Name:         body.Name,
		LocationType: body.LocationType,
		Description:  body.Description,
		UnitID:       body.UnitID,
		IsActive:     body.IsActive,
		SortOrder:    body.SortOrder,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handleListMaintenanceAssets(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListMaintenanceAssets(r.Context(), actor, propID, r.URL.Query().Get("include_retired") == "true")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"assets": nonNil(items)})
}

func (h *Handler) handleGetMaintenanceAsset(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	asset, history, err := h.svc.GetMaintenanceAssetWithHistory(r.Context(), actor, propID, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"asset":               asset,
		"maintenance_history": nonNil(history),
	})
}

func (h *Handler) handleCreateMaintenanceAsset(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		AssetCode         string                        `json:"asset_code"`
		Name              string                        `json:"name"`
		Description       string                        `json:"description"`
		LocationID        *uuid.UUID                    `json:"location_id"`
		UnitID            *uuid.UUID                    `json:"unit_id"`
		AssetCategory     string                        `json:"asset_category"`
		Manufacturer      string                        `json:"manufacturer"`
		Model             string                        `json:"model"`
		SerialNumber      string                        `json:"serial_number"`
		InstalledOn       string                        `json:"installed_on"`
		WarrantyExpiresOn string                        `json:"warranty_expires_on"`
		WarrantyNotes     string                        `json:"warranty_notes"`
		Status            domain.MaintenanceAssetStatus `json:"status"`
		Notes             string                        `json:"notes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	installed, err := parseOptionalBodyDate(body.InstalledOn)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "installed_on must be YYYY-MM-DD")
		return
	}
	warranty, err := parseOptionalBodyDate(body.WarrantyExpiresOn)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "warranty_expires_on must be YYYY-MM-DD")
		return
	}
	out, err := h.svc.CreateMaintenanceAsset(r.Context(), actor, domain.MaintenanceAsset{
		PropertyID:        propID,
		AssetCode:         body.AssetCode,
		Name:              body.Name,
		Description:       body.Description,
		LocationID:        body.LocationID,
		UnitID:            body.UnitID,
		AssetCategory:     body.AssetCategory,
		Manufacturer:      body.Manufacturer,
		Model:             body.Model,
		SerialNumber:      body.SerialNumber,
		InstalledOn:       installed,
		WarrantyExpiresOn: warranty,
		WarrantyNotes:     body.WarrantyNotes,
		Status:            body.Status,
		Notes:             body.Notes,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleUpdateMaintenanceAsset(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		AssetCode         string                        `json:"asset_code"`
		Name              string                        `json:"name"`
		Description       string                        `json:"description"`
		LocationID        *uuid.UUID                    `json:"location_id"`
		UnitID            *uuid.UUID                    `json:"unit_id"`
		AssetCategory     string                        `json:"asset_category"`
		Manufacturer      string                        `json:"manufacturer"`
		Model             string                        `json:"model"`
		SerialNumber      string                        `json:"serial_number"`
		InstalledOn       string                        `json:"installed_on"`
		WarrantyExpiresOn string                        `json:"warranty_expires_on"`
		WarrantyNotes     string                        `json:"warranty_notes"`
		Status            domain.MaintenanceAssetStatus `json:"status"`
		Notes             string                        `json:"notes"`
		Version           int                           `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	installed, err := parseOptionalBodyDate(body.InstalledOn)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "installed_on must be YYYY-MM-DD")
		return
	}
	warranty, err := parseOptionalBodyDate(body.WarrantyExpiresOn)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "warranty_expires_on must be YYYY-MM-DD")
		return
	}
	out, err := h.svc.UpdateMaintenanceAsset(r.Context(), actor, domain.MaintenanceAsset{
		ID:                id,
		PropertyID:        propID,
		AssetCode:         body.AssetCode,
		Name:              body.Name,
		Description:       body.Description,
		LocationID:        body.LocationID,
		UnitID:            body.UnitID,
		AssetCategory:     body.AssetCategory,
		Manufacturer:      body.Manufacturer,
		Model:             body.Model,
		SerialNumber:      body.SerialNumber,
		InstalledOn:       installed,
		WarrantyExpiresOn: warranty,
		WarrantyNotes:     body.WarrantyNotes,
		Status:            body.Status,
		Notes:             body.Notes,
		Version:           body.Version,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handleListMaintenanceContractors(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListMaintenanceContractors(r.Context(), actor, propID, r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contractors": nonNil(items)})
}

func (h *Handler) handleCreateMaintenanceContractor(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		Name     string `json:"name"`
		Company  string `json:"company"`
		Trade    string `json:"trade"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		Notes    string `json:"notes"`
		IsActive *bool  `json:"is_active"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	out, err := h.svc.CreateMaintenanceContractor(r.Context(), actor, domain.MaintenanceContractor{
		PropertyID: propID,
		Name:       body.Name,
		Company:    body.Company,
		Trade:      body.Trade,
		Email:      body.Email,
		Phone:      body.Phone,
		Notes:      body.Notes,
		IsActive:   active,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleUpdateMaintenanceContractor(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Name     string `json:"name"`
		Company  string `json:"company"`
		Trade    string `json:"trade"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		Notes    string `json:"notes"`
		IsActive bool   `json:"is_active"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	out, err := h.svc.UpdateMaintenanceContractor(r.Context(), actor, domain.MaintenanceContractor{
		ID:         id,
		PropertyID: propID,
		Name:       body.Name,
		Company:    body.Company,
		Trade:      body.Trade,
		Email:      body.Email,
		Phone:      body.Phone,
		Notes:      body.Notes,
		IsActive:   body.IsActive,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handleListMaintenanceSchedules(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListMaintenanceSchedules(r.Context(), actor, propID, r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": nonNil(items)})
}

func (h *Handler) handleCreateMaintenanceSchedule(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		Title                string                      `json:"title"`
		Description          string                      `json:"description"`
		CategoryID           uuid.UUID                   `json:"category_id"`
		LocationID           *uuid.UUID                  `json:"location_id"`
		UnitID               *uuid.UUID                  `json:"unit_id"`
		AssetID              *uuid.UUID                  `json:"asset_id"`
		Priority             domain.MaintenancePriority  `json:"priority"`
		Frequency            domain.MaintenanceFrequency `json:"frequency"`
		NextDueDate          string                      `json:"next_due_date"`
		AssignedUserID       *uuid.UUID                  `json:"assigned_user_id"`
		AssignedContractorID *uuid.UUID                  `json:"assigned_contractor_id"`
		InspectionRequired   *bool                       `json:"inspection_required"`
		Notes                string                      `json:"notes"`
		IsActive             *bool                       `json:"is_active"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	due, err := domain.ParseDate(body.NextDueDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "next_due_date must be YYYY-MM-DD")
		return
	}
	inspReq := true
	if body.InspectionRequired != nil {
		inspReq = *body.InspectionRequired
	}
	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}
	out, err := h.svc.CreateMaintenanceSchedule(r.Context(), actor, domain.MaintenanceSchedule{
		PropertyID:           propID,
		Title:                body.Title,
		Description:          body.Description,
		CategoryID:           body.CategoryID,
		LocationID:           body.LocationID,
		UnitID:               body.UnitID,
		AssetID:              body.AssetID,
		Priority:             body.Priority,
		Frequency:            body.Frequency,
		NextDueDate:          due,
		AssignedUserID:       body.AssignedUserID,
		AssignedContractorID: body.AssignedContractorID,
		InspectionRequired:   inspReq,
		Notes:                body.Notes,
		IsActive:             active,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleUpdateMaintenanceSchedule(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Title                string                      `json:"title"`
		Description          string                      `json:"description"`
		CategoryID           uuid.UUID                   `json:"category_id"`
		LocationID           *uuid.UUID                  `json:"location_id"`
		UnitID               *uuid.UUID                  `json:"unit_id"`
		AssetID              *uuid.UUID                  `json:"asset_id"`
		Priority             domain.MaintenancePriority  `json:"priority"`
		Frequency            domain.MaintenanceFrequency `json:"frequency"`
		NextDueDate          string                      `json:"next_due_date"`
		AssignedUserID       *uuid.UUID                  `json:"assigned_user_id"`
		AssignedContractorID *uuid.UUID                  `json:"assigned_contractor_id"`
		InspectionRequired   bool                        `json:"inspection_required"`
		Notes                string                      `json:"notes"`
		IsActive             bool                        `json:"is_active"`
		Version              int                         `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	due, err := domain.ParseDate(body.NextDueDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "next_due_date must be YYYY-MM-DD")
		return
	}
	out, err := h.svc.UpdateMaintenanceSchedule(r.Context(), actor, domain.MaintenanceSchedule{
		ID:                   id,
		PropertyID:           propID,
		Title:                body.Title,
		Description:          body.Description,
		CategoryID:           body.CategoryID,
		LocationID:           body.LocationID,
		UnitID:               body.UnitID,
		AssetID:              body.AssetID,
		Priority:             body.Priority,
		Frequency:            body.Frequency,
		NextDueDate:          due,
		AssignedUserID:       body.AssignedUserID,
		AssignedContractorID: body.AssignedContractorID,
		InspectionRequired:   body.InspectionRequired,
		Notes:                body.Notes,
		IsActive:             body.IsActive,
		Version:              body.Version,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handleRunMaintenanceSchedules(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		AsOfDate string `json:"as_of_date"`
	}
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	asOf, err := parseOptionalBodyDate(body.AsOfDate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_date", "as_of_date must be YYYY-MM-DD")
		return
	}
	created, err := h.svc.RunDuePreventativeSchedules(r.Context(), actor, propID, asOf)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_count": len(created),
		"requests":        nonNil(created),
	})
}
