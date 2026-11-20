package webui

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/httpapi"
	"github.com/Prestwickuk/lovelace/internal/httpx"
	"github.com/Prestwickuk/lovelace/internal/service"
	"github.com/Prestwickuk/lovelace/internal/storage"
)

func (h *Handler) registerMaintenanceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /maintenance", h.requirePageAuth(false, h.handleMaintenanceListGet))
	mux.HandleFunc("GET /maintenance/new", h.requirePageAuth(false, h.handleMaintenanceNewGet))
	mux.HandleFunc("POST /maintenance/new", h.requirePageAuth(false, h.handleMaintenanceNewPost))

	mux.HandleFunc("GET /maintenance/schedule", h.requirePageAuth(false, h.handleMaintenanceScheduleGet))
	mux.HandleFunc("POST /maintenance/schedules", h.requirePageAuth(true, h.handleMaintenanceScheduleCreate))
	mux.HandleFunc("POST /maintenance/schedules/{id}/update", h.requirePageAuth(true, h.handleMaintenanceScheduleUpdate))
	mux.HandleFunc("POST /maintenance/schedules/run", h.requirePageAuth(true, h.handleMaintenanceScheduleRun))

	mux.HandleFunc("GET /maintenance/facilities", h.requirePageAuth(false, h.handleMaintenanceFacilitiesGet))
	mux.HandleFunc("POST /maintenance/categories", h.requirePageAuth(true, h.handleMaintenanceCategoryCreate))
	mux.HandleFunc("POST /maintenance/categories/{id}/update", h.requirePageAuth(true, h.handleMaintenanceCategoryUpdate))
	mux.HandleFunc("POST /maintenance/locations", h.requirePageAuth(true, h.handleMaintenanceLocationCreate))
	mux.HandleFunc("POST /maintenance/locations/{id}/update", h.requirePageAuth(true, h.handleMaintenanceLocationUpdate))
	mux.HandleFunc("POST /maintenance/assets", h.requirePageAuth(true, h.handleMaintenanceAssetCreate))
	mux.HandleFunc("POST /maintenance/assets/{id}/update", h.requirePageAuth(true, h.handleMaintenanceAssetUpdate))
	mux.HandleFunc("POST /maintenance/contractors", h.requirePageAuth(true, h.handleMaintenanceContractorCreate))
	mux.HandleFunc("POST /maintenance/contractors/{id}/update", h.requirePageAuth(true, h.handleMaintenanceContractorUpdate))

	mux.HandleFunc("GET /maintenance/{id}", h.requirePageAuth(false, h.handleMaintenanceDetailGet))
	mux.HandleFunc("POST /maintenance/{id}/update", h.requirePageAuth(true, h.handleMaintenanceDetailUpdate))
	mux.HandleFunc("POST /maintenance/{id}/assign", h.requirePageAuth(true, h.handleMaintenanceAssign))
	mux.HandleFunc("POST /maintenance/{id}/transition", h.requirePageAuth(true, h.handleMaintenanceTransition))
	mux.HandleFunc("POST /maintenance/{id}/work-notes", h.requirePageAuth(true, h.handleMaintenanceWorkNoteAdd))
	mux.HandleFunc("POST /maintenance/{id}/inspection", h.requirePageAuth(true, h.handleMaintenanceInspectionPost))
	mux.HandleFunc("POST /maintenance/{id}/attachments", h.requirePageAuth(false, h.handleMaintenanceAttachmentUpload))
	mux.HandleFunc("GET /maintenance/{id}/attachments/{attachmentID}", h.requirePageAuth(false, h.handleMaintenanceAttachmentDownload))
	mux.HandleFunc("POST /maintenance/{id}/restrictions", h.requirePageAuth(true, h.handleMaintenanceRestrictionCreate))
	mux.HandleFunc("POST /maintenance/{id}/restrictions/{linkID}/update", h.requirePageAuth(true, h.handleMaintenanceRestrictionUpdate))
	mux.HandleFunc("POST /maintenance/{id}/restrictions/{linkID}/release", h.requirePageAuth(true, h.handleMaintenanceRestrictionRelease))
}

type maintFilterView struct {
	Status     string
	Priority   string
	CategoryID string
	LocationID string
	UnitID     string
	AssetID    string
	AssignedTo string
	WorkType   string
	Overdue    bool
	Restricted bool
	DateFrom   string
	DateTo     string
	Search     string
}

func (h *Handler) handleMaintenanceListGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance & Facilities", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load maintenance view.")
		return
	}
	if !base.CanReadMaintenance {
		h.renderError(w, http.StatusForbidden, "Access Denied", "Your administrative role cannot view maintenance records.")
		return
	}
	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	if status == "" && q.Get("overdue") != "1" && q.Get("restricted") != "1" && q.Get("q") == "" && q.Get("priority") == "" && q.Get("date_from") == "" {
		status = "OPEN"
	}
	var dateFrom, dateTo *time.Time
	if raw := strings.TrimSpace(q.Get("date_from")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			dateFrom = &d
		}
	}
	if raw := strings.TrimSpace(q.Get("date_to")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			dateTo = &d
		}
	}
	filter := domain.MaintenanceRequestFilter{
		Status:      status,
		Priority:    q.Get("priority"),
		CategoryID:  parseOptionalUUID(q.Get("category_id")),
		LocationID:  parseOptionalUUID(q.Get("location_id")),
		UnitID:      parseOptionalUUID(q.Get("unit_id")),
		AssetID:     parseOptionalUUID(q.Get("asset_id")),
		AssignedTo:  parseOptionalUUID(q.Get("assigned_to")),
		WorkType:    q.Get("work_type"),
		OverdueOnly: q.Get("overdue") == "1" || q.Get("overdue") == "true",
		Restricted:  q.Get("restricted") == "1" || q.Get("restricted") == "true",
		DateFrom:    dateFrom,
		DateTo:      dateTo,
		Search:      q.Get("q"),
		Limit:       200,
	}

	requests, err := h.svc.ListMaintenanceRequests(r.Context(), actor, base.Property.ID, filter)
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Error", "Unable to list maintenance requests.")
		return
	}
	summary, err := h.svc.GetOpsMaintenanceSummary(r.Context(), actor, base.Property.ID, nil)
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Error", "Unable to compute maintenance summary.")
		return
	}
	cats, _ := h.svc.ListMaintenanceCategories(r.Context(), actor, base.Property.ID, false)
	locs, _ := h.svc.ListMaintenanceLocations(r.Context(), actor, base.Property.ID, false)
	assets, _ := h.svc.ListMaintenanceAssets(r.Context(), actor, base.Property.ID, false)
	units, _ := h.svc.ListUnits(r.Context(), actor, base.Property.ID, nil)
	staff, _ := h.svc.ListMaintenanceAssignableStaff(r.Context(), actor, base.Property.ID)

	h.renderPage(w, "maintenance_list.html", struct {
		baseView
		Requests   []domain.MaintenanceRequest
		Summary    domain.OpsMaintenanceSummary
		Categories []domain.MaintenanceCategory
		Locations  []domain.MaintenanceLocation
		Assets     []domain.MaintenanceAsset
		Units      []domain.Unit
		Staff      []domain.OpsStaffMember
		Filter     maintFilterView
		TodayStr   string
	}{
		baseView:   base,
		Requests:   requests,
		Summary:    summary,
		Categories: cats,
		Locations:  locs,
		Assets:     assets,
		Units:      units,
		Staff:      staff,
		TodayStr:   domain.FormatDate(time.Now().UTC()),
		Filter: maintFilterView{
			Status:     status,
			Priority:   q.Get("priority"),
			CategoryID: q.Get("category_id"),
			LocationID: q.Get("location_id"),
			UnitID:     q.Get("unit_id"),
			AssetID:    q.Get("asset_id"),
			AssignedTo: q.Get("assigned_to"),
			WorkType:   q.Get("work_type"),
			Overdue:    filter.OverdueOnly,
			Restricted: filter.Restricted,
			DateFrom:   q.Get("date_from"),
			DateTo:     q.Get("date_to"),
			Search:     q.Get("q"),
		},
	})
}

func (h *Handler) handleMaintenanceNewGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Report Maintenance Issue", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load form.")
		return
	}
	if !base.CanReportMaintenance {
		h.renderError(w, http.StatusForbidden, "Access Denied", "Your administrative role cannot report maintenance issues.")
		return
	}
	cats, err := h.svc.ListMaintenanceCategories(r.Context(), actor, base.Property.ID, false)
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Error", "Unable to load categories.")
		return
	}
	locs, _ := h.svc.ListMaintenanceLocations(r.Context(), actor, base.Property.ID, false)
	assets, _ := h.svc.ListMaintenanceAssets(r.Context(), actor, base.Property.ID, false)
	units, _ := h.svc.ListUnits(r.Context(), actor, base.Property.ID, nil)
	staff, _ := h.svc.ListMaintenanceAssignableStaff(r.Context(), actor, base.Property.ID)
	contractors, _ := h.svc.ListMaintenanceContractors(r.Context(), actor, base.Property.ID, false)

	today := domain.FormatDate(time.Now().UTC())
	tomorrow := domain.FormatDate(time.Now().UTC().AddDate(0, 0, 1))
	h.renderPage(w, "maintenance_new.html", struct {
		baseView
		Categories    []domain.MaintenanceCategory
		Locations     []domain.MaintenanceLocation
		Assets        []domain.MaintenanceAsset
		Units         []domain.Unit
		Staff         []domain.OpsStaffMember
		Contractors   []domain.MaintenanceContractor
		PreUnitID     string
		PreLocationID string
		PreAssetID    string
		TodayStr      string
		TomorrowStr   string
	}{
		baseView:      base,
		Categories:    cats,
		Locations:     locs,
		Assets:        assets,
		Units:         units,
		Staff:         staff,
		Contractors:   contractors,
		PreUnitID:     r.URL.Query().Get("unit_id"),
		PreLocationID: r.URL.Query().Get("location_id"),
		PreAssetID:    r.URL.Query().Get("asset_id"),
		TodayStr:      today,
		TomorrowStr:   tomorrow,
	})
}

func (h *Handler) handleMaintenanceNewPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	maxBytes := h.cfg.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+httpx.MaxFormBodyBytes)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Submission", "The submitted maintenance form or attachment exceeded upload limits or was malformed.")
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	if !httpx.VerifyCSRFToken(actor, r.FormValue("csrf_token")) {
		h.renderError(w, http.StatusForbidden, "Security Token Invalid", "Missing or invalid CSRF token. Please reload the page and try again.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Report Maintenance Issue", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load property.")
		return
	}

	var attachmentBytes []byte
	var attachmentName string
	if file, header, err := r.FormFile("attachment"); err == nil {
		defer file.Close()
		attachmentBytes, err = io.ReadAll(io.LimitReader(file, maxBytes+1))
		if err != nil {
			h.redirectErr(w, r, "/maintenance/new", fmt.Errorf("failed to read uploaded file"))
			return
		}
		if len(attachmentBytes) > 0 {
			attachmentName = header.Filename
			if _, _, err := storage.ValidateAndSniffAttachment(attachmentName, attachmentBytes, maxBytes); err != nil {
				h.redirectErr(w, r, "/maintenance/new", err)
				return
			}
		}
	}

	catID, err := uuid.Parse(strings.TrimSpace(r.FormValue("category_id")))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/new", fmt.Errorf("please choose a valid category"))
		return
	}
	var targetDate, scheduledFor, resStart, resEnd *time.Time
	if raw := strings.TrimSpace(r.FormValue("target_date")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			targetDate = &d
		}
	}
	if raw := strings.TrimSpace(r.FormValue("scheduled_for")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			scheduledFor = &d
		}
	}
	createRestriction := r.FormValue("create_restriction") == "true"
	if createRestriction {
		if d, err := domain.ParseDate(strings.TrimSpace(r.FormValue("restriction_start_date"))); err == nil {
			resStart = &d
		}
		if d, err := domain.ParseDate(strings.TrimSpace(r.FormValue("restriction_end_date"))); err == nil {
			resEnd = &d
		}
	}
	workType := domain.MaintenanceWorkType(strings.TrimSpace(r.FormValue("work_type")))
	if workType == "" {
		workType = domain.MaintWorkReactive
	}
	impact := domain.MaintenanceAvailabilityImpact(strings.TrimSpace(r.FormValue("availability_impact")))
	if impact == "" {
		impact = domain.MaintImpactNone
	}

	detail, err := h.svc.CreateMaintenanceRequest(r.Context(), actor, service.CreateMaintenanceRequestInput{
		PropertyID:           base.Property.ID,
		Title:                r.FormValue("title"),
		Description:          r.FormValue("description"),
		CategoryID:           catID,
		Priority:             domain.MaintenancePriority(strings.TrimSpace(r.FormValue("priority"))),
		WorkType:             workType,
		LocationID:           parseOptionalUUID(r.FormValue("location_id")),
		UnitID:               parseOptionalUUID(r.FormValue("unit_id")),
		AssetID:              parseOptionalUUID(r.FormValue("asset_id")),
		AssignedUserID:       parseOptionalUUID(r.FormValue("assigned_user_id")),
		AssignedContractorID: parseOptionalUUID(r.FormValue("assigned_contractor_id")),
		TargetDate:           targetDate,
		ScheduledFor:         scheduledFor,
		GuestImpact:          r.FormValue("guest_impact") == "true",
		SafetyImpact:         r.FormValue("safety_impact") == "true",
		AvailabilityImpact:   impact,
		InspectionRequired:   r.FormValue("inspection_required") == "true",
		InternalNotes:        r.FormValue("internal_notes"),
		CreateRestriction:    createRestriction,
		RestrictionStartDate: resStart,
		RestrictionEndDate:   resEnd,
		RestrictionNotes:     r.FormValue("restriction_notes"),
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/new", err)
		return
	}

	if len(attachmentBytes) > 0 {
		if _, err := h.svc.UploadMaintenanceAttachment(
			r.Context(), actor, base.Property.ID, detail.Request.ID, nil,
			attachmentName, attachmentBytes, r.FormValue("attachment_caption"), true,
		); err != nil {
			redirectMsg(w, r, "/maintenance/"+detail.Request.ID.String(), "Maintenance issue "+detail.Request.Reference+" reported (attachment skipped: "+err.Error()+")")
			return
		}
	}
	redirectMsg(w, r, "/maintenance/"+detail.Request.ID.String(), "Maintenance request "+detail.Request.Reference+" created")
}

func (h *Handler) handleMaintenanceDetailGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	reqID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Invalid Request", "Invalid maintenance request ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance Request", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load maintenance view.")
		return
	}
	if !base.CanReadMaintenance {
		h.renderError(w, http.StatusForbidden, "Access Denied", "Your administrative role cannot view maintenance records.")
		return
	}
	detail, err := h.svc.GetMaintenanceRequestDetail(r.Context(), actor, base.Property.ID, reqID)
	if err != nil {
		h.renderDomainError(w, err, "Not Found", "Maintenance request could not be found.")
		return
	}
	base.Title = detail.Request.Reference + " · " + detail.Request.Title
	cats, _ := h.svc.ListMaintenanceCategories(r.Context(), actor, base.Property.ID, false)
	locs, _ := h.svc.ListMaintenanceLocations(r.Context(), actor, base.Property.ID, false)
	assets, _ := h.svc.ListMaintenanceAssets(r.Context(), actor, base.Property.ID, false)
	units, _ := h.svc.ListUnits(r.Context(), actor, base.Property.ID, nil)
	staff, _ := h.svc.ListMaintenanceAssignableStaff(r.Context(), actor, base.Property.ID)
	contractors, _ := h.svc.ListMaintenanceContractors(r.Context(), actor, base.Property.ID, false)

	today := domain.FormatDate(time.Now().UTC())
	tomorrow := domain.FormatDate(time.Now().UTC().AddDate(0, 0, 1))
	h.renderPage(w, "maintenance_detail.html", struct {
		baseView
		Detail          domain.MaintenanceRequestDetail
		AllowedNext     []domain.MaintenanceStatus
		Categories      []domain.MaintenanceCategory
		Locations       []domain.MaintenanceLocation
		Assets          []domain.MaintenanceAsset
		Units           []domain.Unit
		Staff           []domain.OpsStaffMember
		Contractors     []domain.MaintenanceContractor
		TodayStr        string
		TomorrowStr     string
		CanRestrictUnit bool
	}{
		baseView:        base,
		Detail:          detail,
		AllowedNext:     detail.Request.Status.AllowedNextStatuses(),
		Categories:      cats,
		Locations:       locs,
		Assets:          assets,
		Units:           units,
		Staff:           staff,
		Contractors:     contractors,
		TodayStr:        today,
		TomorrowStr:     tomorrow,
		CanRestrictUnit: base.CanWriteMaintenance && base.CanWriteBlocks,
	})
}

func (h *Handler) handleMaintenanceDetailUpdate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	reqID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid maintenance ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance Request", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load property.")
		return
	}
	catID, err := uuid.Parse(strings.TrimSpace(r.FormValue("category_id")))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), fmt.Errorf("category is required"))
		return
	}
	var targetDate, scheduledFor *time.Time
	if raw := strings.TrimSpace(r.FormValue("target_date")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			targetDate = &d
		}
	}
	if raw := strings.TrimSpace(r.FormValue("scheduled_for")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			scheduledFor = &d
		}
	}
	_, err = h.svc.UpdateMaintenanceRequest(r.Context(), actor, domain.MaintenanceRequest{
		ID:                 reqID,
		PropertyID:         base.Property.ID,
		Title:              r.FormValue("title"),
		Description:        r.FormValue("description"),
		CategoryID:         catID,
		Priority:           domain.MaintenancePriority(strings.TrimSpace(r.FormValue("priority"))),
		LocationID:         parseOptionalUUID(r.FormValue("location_id")),
		UnitID:             parseOptionalUUID(r.FormValue("unit_id")),
		AssetID:            parseOptionalUUID(r.FormValue("asset_id")),
		TargetDate:         targetDate,
		ScheduledFor:       scheduledFor,
		GuestImpact:        r.FormValue("guest_impact") == "true",
		SafetyImpact:       r.FormValue("safety_impact") == "true",
		AvailabilityImpact: domain.MaintenanceAvailabilityImpact(strings.TrimSpace(r.FormValue("availability_impact"))),
		InspectionRequired: r.FormValue("inspection_required") == "true",
		InternalNotes:      r.FormValue("internal_notes"),
		Version:            parseIntDefault(r.FormValue("version"), 1),
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), err)
		return
	}
	redirectMsg(w, r, "/maintenance/"+reqID.String(), "Maintenance details updated")
}

func (h *Handler) handleMaintenanceAssign(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	reqID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid maintenance ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance Request", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load property.")
		return
	}
	var due *time.Time
	if raw := strings.TrimSpace(r.FormValue("due_date")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			due = &d
		}
	}
	_, err = h.svc.AssignMaintenanceRequest(
		r.Context(), actor, base.Property.ID, reqID,
		parseOptionalUUID(r.FormValue("assigned_user_id")),
		parseOptionalUUID(r.FormValue("assigned_contractor_id")),
		due,
		r.FormValue("notes"),
		parseIntDefault(r.FormValue("version"), 1),
	)
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), err)
		return
	}
	redirectMsg(w, r, "/maintenance/"+reqID.String(), "Maintenance assignment updated")
}

func (h *Handler) handleMaintenanceTransition(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	reqID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid maintenance ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance Request", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load property.")
		return
	}
	var scheduledFor *time.Time
	if raw := strings.TrimSpace(r.FormValue("scheduled_for")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			scheduledFor = &d
		}
	}
	status := domain.MaintenanceStatus(strings.TrimSpace(r.FormValue("status")))
	_, err = h.svc.TransitionMaintenanceStatus(
		r.Context(), actor, base.Property.ID, reqID,
		status, r.FormValue("reason"), scheduledFor,
		parseIntDefault(r.FormValue("version"), 1),
	)
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), err)
		return
	}
	redirectMsg(w, r, "/maintenance/"+reqID.String(), "Maintenance status moved to "+status.Label())
}

func (h *Handler) handleMaintenanceWorkNoteAdd(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	reqID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid maintenance ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance Request", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load property.")
		return
	}
	_, err = h.svc.AddMaintenanceWorkEntry(
		r.Context(), actor, base.Property.ID, reqID,
		domain.MaintenanceWorkEntryType(strings.TrimSpace(r.FormValue("entry_type"))),
		r.FormValue("body"),
		r.FormValue("parts_used"),
	)
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), err)
		return
	}
	redirectMsg(w, r, "/maintenance/"+reqID.String(), "Work log entry added")
}

func (h *Handler) handleMaintenanceInspectionPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	reqID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid maintenance ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance Request", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load property.")
		return
	}
	res := domain.MaintenanceInspectionResult(strings.TrimSpace(r.FormValue("result")))
	closeIfPassed := r.FormValue("close_if_passed") == "true"
	_, _, err = h.svc.RecordMaintenanceInspection(
		r.Context(), actor, base.Property.ID, reqID,
		res, r.FormValue("notes"), closeIfPassed,
		parseIntDefault(r.FormValue("version"), 1),
	)
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), err)
		return
	}
	msg := "Inspection recorded: " + string(res)
	if res == domain.MaintInspectionFailed {
		msg = "Inspection FAILED — request returned to In Progress"
	}
	redirectMsg(w, r, "/maintenance/"+reqID.String(), msg)
}

func (h *Handler) handleMaintenanceAttachmentUpload(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	reqID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid maintenance ID.")
		return
	}
	maxBytes := h.cfg.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+httpx.MaxFormBodyBytes)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Upload", "Attachment upload exceeded maximum size or was malformed.")
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	if !httpx.VerifyCSRFToken(actor, r.FormValue("csrf_token")) {
		h.renderError(w, http.StatusForbidden, "Security Token Invalid", "Missing or invalid CSRF token. Please reload the page and try again.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance Request", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load property.")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), fmt.Errorf("please choose a file to upload"))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), fmt.Errorf("failed reading uploaded file"))
		return
	}
	if _, err := h.svc.UploadMaintenanceAttachment(
		r.Context(), actor, base.Property.ID, reqID, nil,
		header.Filename, data, r.FormValue("caption"), false,
	); err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), err)
		return
	}
	redirectMsg(w, r, "/maintenance/"+reqID.String(), "Attachment uploaded securely")
}

func (h *Handler) handleMaintenanceAttachmentDownload(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	attID, err := uuid.Parse(r.PathValue("attachmentID"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid attachment ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Attachment", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load property.")
		return
	}
	att, rc, err := h.svc.OpenMaintenanceAttachment(r.Context(), actor, base.Property.ID, attID)
	if err != nil {
		h.renderDomainError(w, err, "Attachment Unavailable", "The requested attachment could not be accessed.")
		return
	}
	defer rc.Close()
	httpapi.ServeMaintenanceAttachment(w, att, rc, r.URL.Query().Get("inline") == "1")
}

func (h *Handler) handleMaintenanceRestrictionCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	reqID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid maintenance ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance Request", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load property.")
		return
	}
	start, err := domain.ParseDate(r.FormValue("start_date"))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), fmt.Errorf("valid start date required"))
		return
	}
	end, err := domain.ParseDate(r.FormValue("end_date"))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), fmt.Errorf("valid end date required"))
		return
	}
	_, err = h.svc.CreateMaintenanceRestriction(r.Context(), actor, base.Property.ID, reqID, start, end, r.FormValue("notes"))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), err)
		return
	}
	redirectMsg(w, r, "/maintenance/"+reqID.String(), "Accommodation unit inventory restriction applied")
}

func (h *Handler) handleMaintenanceRestrictionUpdate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	reqID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid maintenance ID.")
		return
	}
	linkID, err := uuid.Parse(r.PathValue("linkID"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid restriction ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance Request", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load property.")
		return
	}
	start, err := domain.ParseDate(r.FormValue("start_date"))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), fmt.Errorf("valid start date required"))
		return
	}
	end, err := domain.ParseDate(r.FormValue("end_date"))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), fmt.Errorf("valid end date required"))
		return
	}
	_, err = h.svc.UpdateMaintenanceRestrictionDates(
		r.Context(), actor, base.Property.ID, reqID, linkID,
		start, end, parseIntDefault(r.FormValue("group_version"), 1),
	)
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), err)
		return
	}
	redirectMsg(w, r, "/maintenance/"+reqID.String(), "Inventory restriction dates updated")
}

func (h *Handler) handleMaintenanceRestrictionRelease(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	reqID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid maintenance ID.")
		return
	}
	linkID, err := uuid.Parse(r.PathValue("linkID"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid restriction ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance Request", "maintenance")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load property.")
		return
	}
	_, err = h.svc.ReleaseMaintenanceRestriction(r.Context(), actor, base.Property.ID, reqID, linkID)
	if err != nil {
		h.redirectErr(w, r, "/maintenance/"+reqID.String(), err)
		return
	}
	redirectMsg(w, r, "/maintenance/"+reqID.String(), "Inventory restriction released explicitly")
}

// ---------------- Schedule & Workload ----------------

func (h *Handler) handleMaintenanceScheduleGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Maintenance Workload & Preventative Schedule", "maintenance-schedule")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load schedule view.")
		return
	}
	if !base.CanReadMaintenance {
		h.renderError(w, http.StatusForbidden, "Access Denied", "Your administrative role cannot view maintenance schedules.")
		return
	}

	staffFilter := parseOptionalUUID(r.URL.Query().Get("assigned_to"))
	today := time.Now().UTC()
	todayDay := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	endDay := todayDay.AddDate(0, 0, 7)

	scheduledJobs, err := h.svc.ListMaintenanceRequests(r.Context(), actor, base.Property.ID, domain.MaintenanceRequestFilter{
		Status:     "OPEN",
		AssignedTo: staffFilter,
		DateFrom:   &todayDay,
		DateTo:     &endDay,
		Limit:      100,
	})
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Error", "Unable to load upcoming maintenance workload.")
		return
	}
	overdueJobs, _ := h.svc.ListMaintenanceRequests(r.Context(), actor, base.Property.ID, domain.MaintenanceRequestFilter{
		AssignedTo:  staffFilter,
		OverdueOnly: true,
		Limit:       50,
	})
	unassignedJobs, _ := h.svc.ListMaintenanceRequests(r.Context(), actor, base.Property.ID, domain.MaintenanceRequestFilter{
		Status: "REPORTED",
		Limit:  50,
	})
	schedules, err := h.svc.ListMaintenanceSchedules(r.Context(), actor, base.Property.ID, true)
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Error", "Unable to load preventative schedules.")
		return
	}
	cats, _ := h.svc.ListMaintenanceCategories(r.Context(), actor, base.Property.ID, false)
	locs, _ := h.svc.ListMaintenanceLocations(r.Context(), actor, base.Property.ID, false)
	assets, _ := h.svc.ListMaintenanceAssets(r.Context(), actor, base.Property.ID, false)
	units, _ := h.svc.ListUnits(r.Context(), actor, base.Property.ID, nil)
	staff, _ := h.svc.ListMaintenanceAssignableStaff(r.Context(), actor, base.Property.ID)
	contractors, _ := h.svc.ListMaintenanceContractors(r.Context(), actor, base.Property.ID, false)

	var editingSched *domain.MaintenanceSchedule
	if editID := parseOptionalUUID(r.URL.Query().Get("edit_schedule")); editID != nil {
		for i := range schedules {
			if schedules[i].ID == *editID {
				editingSched = &schedules[i]
				break
			}
		}
	}

	h.renderPage(w, "maintenance_schedule.html", struct {
		baseView
		ScheduledJobs   []domain.MaintenanceRequest
		OverdueJobs     []domain.MaintenanceRequest
		UnassignedJobs  []domain.MaintenanceRequest
		Schedules       []domain.MaintenanceSchedule
		EditingSchedule *domain.MaintenanceSchedule
		Categories      []domain.MaintenanceCategory
		Locations       []domain.MaintenanceLocation
		Assets          []domain.MaintenanceAsset
		Units           []domain.Unit
		Staff           []domain.OpsStaffMember
		Contractors     []domain.MaintenanceContractor
		SelectedStaffID string
		TodayStr        string
	}{
		baseView:        base,
		ScheduledJobs:   scheduledJobs,
		OverdueJobs:     overdueJobs,
		UnassignedJobs:  unassignedJobs,
		Schedules:       schedules,
		EditingSchedule: editingSched,
		Categories:      cats,
		Locations:       locs,
		Assets:          assets,
		Units:           units,
		Staff:           staff,
		Contractors:     contractors,
		SelectedStaffID: r.URL.Query().Get("assigned_to"),
		TodayStr:        domain.FormatDate(todayDay),
	})
}

func (h *Handler) handleMaintenanceScheduleCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Schedule", "maintenance-schedule")
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load property.")
		return
	}
	catID, err := uuid.Parse(strings.TrimSpace(r.FormValue("category_id")))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/schedule", fmt.Errorf("valid category required"))
		return
	}
	due, err := domain.ParseDate(r.FormValue("next_due_date"))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/schedule", fmt.Errorf("valid next_due_date required"))
		return
	}
	_, err = h.svc.CreateMaintenanceSchedule(r.Context(), actor, domain.MaintenanceSchedule{
		PropertyID:           base.Property.ID,
		Title:                r.FormValue("title"),
		Description:          r.FormValue("description"),
		CategoryID:           catID,
		LocationID:           parseOptionalUUID(r.FormValue("location_id")),
		UnitID:               parseOptionalUUID(r.FormValue("unit_id")),
		AssetID:              parseOptionalUUID(r.FormValue("asset_id")),
		Priority:             domain.MaintenancePriority(strings.TrimSpace(r.FormValue("priority"))),
		Frequency:            domain.MaintenanceFrequency(strings.TrimSpace(r.FormValue("frequency"))),
		NextDueDate:          due,
		AssignedUserID:       parseOptionalUUID(r.FormValue("assigned_user_id")),
		AssignedContractorID: parseOptionalUUID(r.FormValue("assigned_contractor_id")),
		InspectionRequired:   r.FormValue("inspection_required") == "true",
		Notes:                r.FormValue("notes"),
		IsActive:             true,
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/schedule", err)
		return
	}
	redirectMsg(w, r, "/maintenance/schedule", "Preventative maintenance schedule created")
}

func (h *Handler) handleMaintenanceScheduleUpdate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	schedID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid schedule ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Schedule", "maintenance-schedule")
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load property.")
		return
	}
	catID, err := uuid.Parse(strings.TrimSpace(r.FormValue("category_id")))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/schedule", fmt.Errorf("valid category required"))
		return
	}
	due, err := domain.ParseDate(r.FormValue("next_due_date"))
	if err != nil {
		h.redirectErr(w, r, "/maintenance/schedule", fmt.Errorf("valid next_due_date required"))
		return
	}
	_, err = h.svc.UpdateMaintenanceSchedule(r.Context(), actor, domain.MaintenanceSchedule{
		ID:                   schedID,
		PropertyID:           base.Property.ID,
		Title:                r.FormValue("title"),
		Description:          r.FormValue("description"),
		CategoryID:           catID,
		LocationID:           parseOptionalUUID(r.FormValue("location_id")),
		UnitID:               parseOptionalUUID(r.FormValue("unit_id")),
		AssetID:              parseOptionalUUID(r.FormValue("asset_id")),
		Priority:             domain.MaintenancePriority(strings.TrimSpace(r.FormValue("priority"))),
		Frequency:            domain.MaintenanceFrequency(strings.TrimSpace(r.FormValue("frequency"))),
		NextDueDate:          due,
		AssignedUserID:       parseOptionalUUID(r.FormValue("assigned_user_id")),
		AssignedContractorID: parseOptionalUUID(r.FormValue("assigned_contractor_id")),
		InspectionRequired:   r.FormValue("inspection_required") == "true",
		Notes:                r.FormValue("notes"),
		IsActive:             r.FormValue("is_active") == "true",
		Version:              parseIntDefault(r.FormValue("version"), 1),
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/schedule", err)
		return
	}
	redirectMsg(w, r, "/maintenance/schedule", "Preventative maintenance schedule updated")
}

func (h *Handler) handleMaintenanceScheduleRun(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Schedule", "maintenance-schedule")
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load property.")
		return
	}
	var asOf *time.Time
	if raw := strings.TrimSpace(r.FormValue("as_of_date")); raw != "" {
		d, err := domain.ParseDate(raw)
		if err != nil {
			h.redirectErr(w, r, "/maintenance/schedule", fmt.Errorf("as_of_date must be YYYY-MM-DD"))
			return
		}
		asOf = &d
	}
	created, err := h.svc.RunDuePreventativeSchedules(r.Context(), actor, base.Property.ID, asOf)
	if err != nil {
		h.redirectErr(w, r, "/maintenance/schedule", err)
		return
	}
	redirectMsg(w, r, "/maintenance/schedule", fmt.Sprintf("Preventative generator completed: %d job(s) created", len(created)))
}

// ---------------- Facilities, Categories, Assets & Contractors ----------------

func (h *Handler) handleMaintenanceFacilitiesGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Facilities, Assets & Categories", "maintenance-facilities")
	if err != nil {
		h.renderDomainError(w, err, "Maintenance Unavailable", "Unable to load facilities.")
		return
	}
	if !base.CanReadMaintenance {
		h.renderError(w, http.StatusForbidden, "Access Denied", "Your administrative role cannot view facilities & asset records.")
		return
	}
	cats, err := h.svc.ListMaintenanceCategories(r.Context(), actor, base.Property.ID, true)
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load categories.")
		return
	}
	locs, _ := h.svc.ListMaintenanceLocations(r.Context(), actor, base.Property.ID, true)
	assets, _ := h.svc.ListMaintenanceAssets(r.Context(), actor, base.Property.ID, true)
	contractors, _ := h.svc.ListMaintenanceContractors(r.Context(), actor, base.Property.ID, true)
	units, _ := h.svc.ListUnits(r.Context(), actor, base.Property.ID, nil)

	var inspectedAsset *domain.MaintenanceAsset
	var assetHistory []domain.MaintenanceRequest
	if assetID := parseOptionalUUID(r.URL.Query().Get("asset_id")); assetID != nil {
		if a, hist, err := h.svc.GetMaintenanceAssetWithHistory(r.Context(), actor, base.Property.ID, *assetID); err == nil {
			inspectedAsset = &a
			assetHistory = hist
		}
	}

	var editCat *domain.MaintenanceCategory
	if id := parseOptionalUUID(r.URL.Query().Get("edit_cat")); id != nil {
		for i := range cats {
			if cats[i].ID == *id {
				editCat = &cats[i]
				break
			}
		}
	}
	var editLoc *domain.MaintenanceLocation
	if id := parseOptionalUUID(r.URL.Query().Get("edit_loc")); id != nil {
		for i := range locs {
			if locs[i].ID == *id {
				editLoc = &locs[i]
				break
			}
		}
	}
	var editAsset *domain.MaintenanceAsset
	if id := parseOptionalUUID(r.URL.Query().Get("edit_asset")); id != nil {
		for i := range assets {
			if assets[i].ID == *id {
				editAsset = &assets[i]
				break
			}
		}
	}
	var editContractor *domain.MaintenanceContractor
	if id := parseOptionalUUID(r.URL.Query().Get("edit_contractor")); id != nil {
		for i := range contractors {
			if contractors[i].ID == *id {
				editContractor = &contractors[i]
				break
			}
		}
	}

	h.renderPage(w, "maintenance_facilities.html", struct {
		baseView
		Categories        []domain.MaintenanceCategory
		Locations         []domain.MaintenanceLocation
		Assets            []domain.MaintenanceAsset
		Contractors       []domain.MaintenanceContractor
		Units             []domain.Unit
		InspectedAsset    *domain.MaintenanceAsset
		AssetHistory      []domain.MaintenanceRequest
		EditingCategory   *domain.MaintenanceCategory
		EditingLocation   *domain.MaintenanceLocation
		EditingAsset      *domain.MaintenanceAsset
		EditingContractor *domain.MaintenanceContractor
	}{
		baseView:          base,
		Categories:        cats,
		Locations:         locs,
		Assets:            assets,
		Contractors:       contractors,
		Units:             units,
		InspectedAsset:    inspectedAsset,
		AssetHistory:      assetHistory,
		EditingCategory:   editCat,
		EditingLocation:   editLoc,
		EditingAsset:      editAsset,
		EditingContractor: editContractor,
	})
}

func (h *Handler) handleMaintenanceCategoryCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Facilities", "maintenance-facilities")
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load property.")
		return
	}
	_, err = h.svc.CreateMaintenanceCategory(r.Context(), actor, domain.MaintenanceCategory{
		PropertyID:  base.Property.ID,
		Code:        r.FormValue("code"),
		Name:        r.FormValue("name"),
		Description: r.FormValue("description"),
		IsActive:    true,
		SortOrder:   parseIntDefault(r.FormValue("sort_order"), 100),
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/facilities", err)
		return
	}
	redirectMsg(w, r, "/maintenance/facilities", "Maintenance category added")
}

func (h *Handler) handleMaintenanceCategoryUpdate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Facilities", "maintenance-facilities")
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load property.")
		return
	}
	_, err = h.svc.UpdateMaintenanceCategory(r.Context(), actor, domain.MaintenanceCategory{
		ID:          id,
		PropertyID:  base.Property.ID,
		Code:        r.FormValue("code"),
		Name:        r.FormValue("name"),
		Description: r.FormValue("description"),
		IsActive:    r.FormValue("is_active") == "true",
		SortOrder:   parseIntDefault(r.FormValue("sort_order"), 100),
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/facilities", err)
		return
	}
	redirectMsg(w, r, "/maintenance/facilities", "Maintenance category updated")
}

func (h *Handler) handleMaintenanceLocationCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Facilities", "maintenance-facilities")
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load property.")
		return
	}
	_, err = h.svc.CreateMaintenanceLocation(r.Context(), actor, domain.MaintenanceLocation{
		PropertyID:   base.Property.ID,
		Code:         r.FormValue("code"),
		Name:         r.FormValue("name"),
		LocationType: r.FormValue("location_type"),
		Description:  r.FormValue("description"),
		UnitID:       parseOptionalUUID(r.FormValue("unit_id")),
		IsActive:     true,
		SortOrder:    parseIntDefault(r.FormValue("sort_order"), 100),
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/facilities", err)
		return
	}
	redirectMsg(w, r, "/maintenance/facilities", "Location created")
}

func (h *Handler) handleMaintenanceLocationUpdate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Facilities", "maintenance-facilities")
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load property.")
		return
	}
	_, err = h.svc.UpdateMaintenanceLocation(r.Context(), actor, domain.MaintenanceLocation{
		ID:           id,
		PropertyID:   base.Property.ID,
		Code:         r.FormValue("code"),
		Name:         r.FormValue("name"),
		LocationType: r.FormValue("location_type"),
		Description:  r.FormValue("description"),
		UnitID:       parseOptionalUUID(r.FormValue("unit_id")),
		IsActive:     r.FormValue("is_active") == "true",
		SortOrder:    parseIntDefault(r.FormValue("sort_order"), 100),
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/facilities", err)
		return
	}
	redirectMsg(w, r, "/maintenance/facilities", "Location updated")
}

func (h *Handler) handleMaintenanceAssetCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Facilities", "maintenance-facilities")
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load property.")
		return
	}
	var installed, warranty *time.Time
	if raw := strings.TrimSpace(r.FormValue("installed_on")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			installed = &d
		}
	}
	if raw := strings.TrimSpace(r.FormValue("warranty_expires_on")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			warranty = &d
		}
	}
	created, err := h.svc.CreateMaintenanceAsset(r.Context(), actor, domain.MaintenanceAsset{
		PropertyID:        base.Property.ID,
		AssetCode:         r.FormValue("asset_code"),
		Name:              r.FormValue("name"),
		Description:       r.FormValue("description"),
		LocationID:        parseOptionalUUID(r.FormValue("location_id")),
		UnitID:            parseOptionalUUID(r.FormValue("unit_id")),
		AssetCategory:     r.FormValue("asset_category"),
		Manufacturer:      r.FormValue("manufacturer"),
		Model:             r.FormValue("model"),
		SerialNumber:      r.FormValue("serial_number"),
		InstalledOn:       installed,
		WarrantyExpiresOn: warranty,
		WarrantyNotes:     r.FormValue("warranty_notes"),
		Status:            domain.AssetStatusActive,
		Notes:             r.FormValue("notes"),
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/facilities", err)
		return
	}
	redirectMsg(w, r, "/maintenance/facilities", "Asset "+created.AssetCode+" registered")
}

func (h *Handler) handleMaintenanceAssetUpdate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Facilities", "maintenance-facilities")
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load property.")
		return
	}
	var installed, warranty *time.Time
	if raw := strings.TrimSpace(r.FormValue("installed_on")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			installed = &d
		}
	}
	if raw := strings.TrimSpace(r.FormValue("warranty_expires_on")); raw != "" {
		if d, err := domain.ParseDate(raw); err == nil {
			warranty = &d
		}
	}
	_, err = h.svc.UpdateMaintenanceAsset(r.Context(), actor, domain.MaintenanceAsset{
		ID:                id,
		PropertyID:        base.Property.ID,
		AssetCode:         r.FormValue("asset_code"),
		Name:              r.FormValue("name"),
		Description:       r.FormValue("description"),
		LocationID:        parseOptionalUUID(r.FormValue("location_id")),
		UnitID:            parseOptionalUUID(r.FormValue("unit_id")),
		AssetCategory:     r.FormValue("asset_category"),
		Manufacturer:      r.FormValue("manufacturer"),
		Model:             r.FormValue("model"),
		SerialNumber:      r.FormValue("serial_number"),
		InstalledOn:       installed,
		WarrantyExpiresOn: warranty,
		WarrantyNotes:     r.FormValue("warranty_notes"),
		Status:            domain.MaintenanceAssetStatus(strings.TrimSpace(r.FormValue("status"))),
		Notes:             r.FormValue("notes"),
		Version:           parseIntDefault(r.FormValue("version"), 1),
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/facilities", err)
		return
	}
	http.Redirect(w, r, "/maintenance/facilities?msg="+url.QueryEscape("Asset updated")+"#assets", http.StatusSeeOther)
}

func (h *Handler) handleMaintenanceContractorCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Facilities", "maintenance-facilities")
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load property.")
		return
	}
	_, err = h.svc.CreateMaintenanceContractor(r.Context(), actor, domain.MaintenanceContractor{
		PropertyID: base.Property.ID,
		Name:       r.FormValue("name"),
		Company:    r.FormValue("company"),
		Trade:      r.FormValue("trade"),
		Email:      r.FormValue("email"),
		Phone:      r.FormValue("phone"),
		Notes:      r.FormValue("notes"),
		IsActive:   true,
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/facilities", err)
		return
	}
	redirectMsg(w, r, "/maintenance/facilities", "Contractor added")
}

func (h *Handler) handleMaintenanceContractorUpdate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusNotFound, "Not Found", "Invalid ID.")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Facilities", "maintenance-facilities")
	if err != nil {
		h.renderDomainError(w, err, "Error", "Unable to load property.")
		return
	}
	_, err = h.svc.UpdateMaintenanceContractor(r.Context(), actor, domain.MaintenanceContractor{
		ID:         id,
		PropertyID: base.Property.ID,
		Name:       r.FormValue("name"),
		Company:    r.FormValue("company"),
		Trade:      r.FormValue("trade"),
		Email:      r.FormValue("email"),
		Phone:      r.FormValue("phone"),
		Notes:      r.FormValue("notes"),
		IsActive:   r.FormValue("is_active") == "true",
	})
	if err != nil {
		h.redirectErr(w, r, "/maintenance/facilities", err)
		return
	}
	redirectMsg(w, r, "/maintenance/facilities", "Contractor updated")
}
