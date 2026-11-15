package webui

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func (h *Handler) registerGuestCRMRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /guests", h.requirePageAuth(false, h.handleGuestsListGet))
	mux.HandleFunc("POST /guests", h.requirePageAuth(true, h.handleGuestCreatePost))
	mux.HandleFunc("GET /guests/merge", h.requirePageAuth(false, h.handleGuestMergeQueueGet))
	mux.HandleFunc("POST /guests/merge", h.requirePageAuth(true, h.handleGuestMergePost))
	mux.HandleFunc("POST /guests/duplicates/dismiss", h.requirePageAuth(true, h.handleGuestDuplicateDismissPost))

	mux.HandleFunc("GET /guests/{id}", h.requirePageAuth(false, h.handleGuestDetailGet))
	mux.HandleFunc("POST /guests/{id}/update", h.requirePageAuth(true, h.handleGuestUpdatePost))
	mux.HandleFunc("POST /guests/{id}/preferences", h.requirePageAuth(true, h.handleGuestPreferenceAddPost))
	mux.HandleFunc("POST /guests/{id}/preferences/{prefID}/delete", h.requirePageAuth(true, h.handleGuestPreferenceDeletePost))
	mux.HandleFunc("POST /guests/{id}/notes", h.requirePageAuth(true, h.handleGuestNoteAddPost))
	mux.HandleFunc("POST /guests/{id}/notes/{noteID}/delete", h.requirePageAuth(true, h.handleGuestNoteDeletePost))
	mux.HandleFunc("POST /guests/{id}/interactions", h.requirePageAuth(true, h.handleGuestInteractionPost))
	mux.HandleFunc("POST /guests/{id}/emails", h.requirePageAuth(true, h.handleGuestEmailSendPost))
	mux.HandleFunc("POST /guests/{id}/consent", h.requirePageAuth(true, h.handleGuestConsentPost))
	mux.HandleFunc("GET /guests/{id}/export", h.requirePageAuth(false, h.handleGuestExportGet))
	mux.HandleFunc("POST /guests/{id}/anonymize", h.requirePageAuth(true, h.handleGuestAnonymizePost))
	mux.HandleFunc("POST /communications/{id}/complete-followup", h.requirePageAuth(true, h.handleCommFollowUpCompletePost))

	mux.HandleFunc("GET /enquiries", h.requirePageAuth(false, h.handleEnquiriesListGet))
	mux.HandleFunc("POST /enquiries", h.requirePageAuth(true, h.handleEnquiryCreatePost))
	mux.HandleFunc("GET /enquiries/{id}", h.requirePageAuth(false, h.handleEnquiryDetailGet))
	mux.HandleFunc("POST /enquiries/{id}/update", h.requirePageAuth(true, h.handleEnquiryUpdatePost))
	mux.HandleFunc("POST /enquiries/{id}/messages", h.requirePageAuth(true, h.handleEnquiryMessagePost))

	mux.HandleFunc("GET /communications/settings", h.requirePageAuth(false, h.handleCommsSettingsGet))
	mux.HandleFunc("POST /communications/templates", h.requirePageAuth(true, h.handleTemplateSavePost))
	mux.HandleFunc("POST /communications/templates/{id}/update", h.requirePageAuth(true, h.handleTemplateUpdatePost))
	mux.HandleFunc("POST /communications/automation-rules/{id}/update", h.requirePageAuth(true, h.handleAutomationRuleUpdatePost))
	mux.HandleFunc("POST /communications/failed/{outboxID}/retry", h.requirePageAuth(true, h.handleFailedEmailRetryPost))
	mux.HandleFunc("POST /communications/outbox/{outboxID}/reconcile", h.requirePageAuth(true, h.handleOutboxReconcilePost))
	mux.HandleFunc("POST /communications/retention-policy", h.requirePageAuth(true, h.handleRetentionPolicyPost))
}

func (h *Handler) handleGuestsListGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Directory", "guests")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	q := r.URL.Query()
	segment := q.Get("segment")
	includeAll := q.Get("include_all") == "1"
	sortKey := base.Workspace.GuestsSort
	if norm, ok := domain.IsAllowedGuestsSort(q.Get("sort"), false); ok {
		sortKey = norm
	}
	rawPageSize := q.Get("page_size")
	activeSavedViewID := ""
	if sv, ok := findSavedView(base.Workspace, domain.SavedViewScreenGuests, q.Get("saved_view")); ok {
		activeSavedViewID = sv.ID
		if !q.Has("segment") && sv.Filters["segment"] != "" {
			segment = sv.Filters["segment"]
		}
		if !q.Has("include_all") && sv.Filters["include_all"] != "" {
			includeAll = sv.Filters["include_all"] == "1"
		}
		if !q.Has("sort") && sv.Filters["sort"] != "" {
			if norm, ok := domain.IsAllowedGuestsSort(sv.Filters["sort"], false); ok {
				sortKey = norm
			}
		}
		if rawPageSize == "" && sv.Filters["page_size"] != "" {
			rawPageSize = sv.Filters["page_size"]
		}
	}

	filter := domain.GuestDirectoryFilter{
		Query:      q.Get("q"),
		Segment:    segment,
		Status:     q.Get("status"),
		IncludeAll: includeAll,
	}
	guests, err := h.svc.ListGuests(r.Context(), actor, base.Property.ID, filter)
	if err != nil {
		h.renderDomainError(w, err, "Guest Directory Error", "unable to load guest directory")
		return
	}
	sortGuestsForWorkspace(guests, sortKey)
	totalCount := len(guests)
	page, pageSize := resolvePageParams(rawPageSize, q.Get("page"), base.Workspace.TablePageSize)
	startIdx, endIdx, totalPages := paginateRange(totalCount, page, pageSize)
	if page > totalPages {
		page = totalPages
	}
	pagedGuests := guests[startIdx:endIdx]

	scan, _ := h.svc.ListDuplicateCandidates(r.Context(), actor, base.Property.ID)
	base.ViewControls = viewControlMeta{
		FilterSort:        sortKey,
		PageSize:          pageSize,
		Page:              page,
		TotalCount:        totalCount,
		TotalPages:        totalPages,
		HasPrevPage:       page > 1,
		HasNextPage:       page < totalPages,
		PrevPage:          page - 1,
		NextPage:          page + 1,
		ActiveSavedViewID: activeSavedViewID,
	}
	h.renderPage(w, "guests_list.html", struct {
		baseView
		Guests         []domain.GuestListItem
		Filter         domain.GuestDirectoryFilter
		DuplicateCount int
	}{
		baseView:       base,
		Guests:         pagedGuests,
		Filter:         filter,
		DuplicateCount: len(scan.Candidates),
	})
}

func (h *Handler) handleGuestCreatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Directory", "guests")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	created, err := h.svc.CreateGuest(r.Context(), actor, domain.Guest{
		PropertyID:             base.Property.ID,
		FirstName:              r.FormValue("first_name"),
		LastName:               r.FormValue("last_name"),
		FullName:               r.FormValue("full_name"),
		Email:                  r.FormValue("email"),
		Phone:                  r.FormValue("phone"),
		PreferredContactMethod: r.FormValue("preferred_contact_method"),
		PreferredLanguage:      r.FormValue("preferred_language"),
		Status:                 domain.GuestStatus(r.FormValue("status")),
		Notes:                  r.FormValue("notes"),
	})
	if err != nil {
		h.redirectErr(w, r, "/guests", err)
		return
	}
	redirectMsg(w, r, "/guests/"+created.ID.String(), "Guest profile created.")
}

func (h *Handler) handleGuestDetailGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	guestID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Guest", "invalid guest id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Profile", "guests")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	detail, err := h.svc.GetGuestDetail(r.Context(), actor, base.Property.ID, guestID)
	if err != nil {
		h.renderDomainError(w, err, "Guest Profile Error", "unable to load guest profile")
		return
	}
	if detail.Guest.ID != guestID {
		http.Redirect(w, r, "/guests/"+detail.Guest.ID.String()+"?msg=Redirected+to+merged+canonical+guest+profile", http.StatusSeeOther)
		return
	}
	templates, _ := h.svc.ListCommunicationTemplates(r.Context(), actor, base.Property.ID)
	staff, _ := h.svc.ListUsers(r.Context(), actor)

	var previewSubj, previewBody string
	if tplIDStr := r.URL.Query().Get("preview_template"); tplIDStr != "" {
		if tplID, pErr := uuid.Parse(tplIDStr); pErr == nil {
			var resPtr *uuid.UUID
			if len(detail.Reservations) > 0 {
				resPtr = &detail.Reservations[0].ID
			}
			s, txt, _, rErr := h.svc.PreviewCommunicationTemplate(r.Context(), actor, base.Property.ID, tplID, &detail.Guest.ID, resPtr)
			if rErr == nil {
				previewSubj = s
				previewBody = txt
			}
		}
	}

	h.renderPage(w, "guest_detail.html", struct {
		baseView
		Detail          domain.GuestDetailBundle
		Templates       []domain.CommunicationTemplate
		StaffUsers      []domain.AdminUser
		PreviewTplID    string
		PreviewSubject  string
		PreviewBodyText string
	}{
		baseView:        base,
		Detail:          detail,
		Templates:       templates,
		StaffUsers:      staff,
		PreviewTplID:    r.URL.Query().Get("preview_template"),
		PreviewSubject:  previewSubj,
		PreviewBodyText: previewBody,
	})
}

func (h *Handler) handleGuestUpdatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	guestID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Guest", "invalid guest id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Profile", "guests")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	addrJSON := json.RawMessage(`{}`)
	if raw := strings.TrimSpace(r.FormValue("address_line")); raw != "" {
		addrJSON, _ = json.Marshal(map[string]string{
			"line1":    raw,
			"town":     strings.TrimSpace(r.FormValue("town")),
			"postcode": strings.TrimSpace(r.FormValue("postcode")),
		})
	}
	_, err = h.svc.UpdateGuest(r.Context(), actor, domain.Guest{
		ID:                     guestID,
		PropertyID:             base.Property.ID,
		FirstName:              r.FormValue("first_name"),
		LastName:               r.FormValue("last_name"),
		FullName:               r.FormValue("full_name"),
		Email:                  r.FormValue("email"),
		Phone:                  r.FormValue("phone"),
		PreferredContactMethod: r.FormValue("preferred_contact_method"),
		PreferredLanguage:      r.FormValue("preferred_language"),
		Status:                 domain.GuestStatus(r.FormValue("status")),
		AddressJSON:            addrJSON,
		Notes:                  r.FormValue("notes"),
		Version:                parseIntDefault(r.FormValue("version"), 1),
	})
	if err != nil {
		h.redirectErr(w, r, "/guests/"+guestID.String(), err)
		return
	}
	redirectMsg(w, r, "/guests/"+guestID.String(), "Guest profile updated.")
}

func (h *Handler) handleGuestPreferenceAddPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	guestID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Guest", "invalid guest id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Profile", "guests")
	if err != nil {
		return
	}
	if _, err := h.svc.AddGuestPreference(r.Context(), actor, domain.GuestPreference{
		PropertyID:      base.Property.ID,
		GuestID:         guestID,
		Category:        domain.GuestPreferenceCategory(r.FormValue("category")),
		PreferenceKey:   r.FormValue("preference_key"),
		PreferenceValue: r.FormValue("preference_value"),
		Notes:           r.FormValue("notes"),
	}); err != nil {
		h.redirectErr(w, r, "/guests/"+guestID.String(), err)
		return
	}
	redirectMsg(w, r, "/guests/"+guestID.String(), "Guest preference saved.")
}

func (h *Handler) handleGuestPreferenceDeletePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	guestID, _ := uuid.Parse(r.PathValue("id"))
	prefID, _ := uuid.Parse(r.PathValue("prefID"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Profile", "guests")
	if err != nil {
		return
	}
	if err := h.svc.RemoveGuestPreference(r.Context(), actor, base.Property.ID, guestID, prefID); err != nil {
		h.redirectErr(w, r, "/guests/"+guestID.String(), err)
		return
	}
	redirectMsg(w, r, "/guests/"+guestID.String(), "Preference removed.")
}

func (h *Handler) handleGuestNoteAddPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	guestID, _ := uuid.Parse(r.PathValue("id"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Profile", "guests")
	if err != nil {
		return
	}
	if _, err := h.svc.AddGuestNote(r.Context(), actor, domain.GuestNote{
		PropertyID:    base.Property.ID,
		GuestID:       guestID,
		ReservationID: parseOptionalUUID(r.FormValue("reservation_id")),
		Category:      domain.GuestNoteCategory(r.FormValue("category")),
		Content:       r.FormValue("content"),
		IsPinned:      r.FormValue("is_pinned") == "true",
	}); err != nil {
		h.redirectErr(w, r, "/guests/"+guestID.String(), err)
		return
	}
	redirectMsg(w, r, "/guests/"+guestID.String(), "Internal staff note recorded.")
}

func (h *Handler) handleGuestNoteDeletePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	guestID, _ := uuid.Parse(r.PathValue("id"))
	noteID, _ := uuid.Parse(r.PathValue("noteID"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Profile", "guests")
	if err != nil {
		return
	}
	if err := h.svc.RemoveGuestNote(r.Context(), actor, base.Property.ID, guestID, noteID); err != nil {
		h.redirectErr(w, r, "/guests/"+guestID.String(), err)
		return
	}
	redirectMsg(w, r, "/guests/"+guestID.String(), "Internal note deleted.")
}

func (h *Handler) handleGuestInteractionPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	guestID, _ := uuid.Parse(r.PathValue("id"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Profile", "guests")
	if err != nil {
		return
	}
	var followUpAt *time.Time
	if raw := strings.TrimSpace(r.FormValue("follow_up_at")); raw != "" {
		if t, pErr := time.Parse("2006-01-02T15:04", raw); pErr == nil {
			u := t.UTC()
			followUpAt = &u
		} else if d, dErr := domain.ParseDate(raw); dErr == nil {
			followUpAt = &d
		}
	}
	if _, err := h.svc.LogGuestInteraction(r.Context(), actor, domain.GuestCommunication{
		PropertyID:         base.Property.ID,
		GuestID:            &guestID,
		ReservationID:      parseOptionalUUID(r.FormValue("reservation_id")),
		InteractionType:    domain.InteractionType(r.FormValue("interaction_type")),
		Direction:          domain.CommDirection(r.FormValue("direction")),
		Subject:            r.FormValue("subject"),
		Summary:            r.FormValue("summary"),
		BodyText:           r.FormValue("body_text"),
		FollowUpRequired:   r.FormValue("follow_up_required") == "true",
		FollowUpAssignedTo: parseOptionalUUID(r.FormValue("follow_up_assigned_to")),
		FollowUpAt:         followUpAt,
		OccurredAt:         time.Now().UTC(),
	}); err != nil {
		h.redirectErr(w, r, "/guests/"+guestID.String(), err)
		return
	}
	redirectMsg(w, r, "/guests/"+guestID.String(), "Interaction logged to timeline.")
}

func (h *Handler) handleGuestEmailSendPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	guestID, _ := uuid.Parse(r.PathValue("id"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Profile", "guests")
	if err != nil {
		return
	}
	if _, err := h.svc.SendGuestEmail(r.Context(), actor, service.SendGuestEmailInput{
		PropertyID:    base.Property.ID,
		GuestID:       guestID,
		ReservationID: parseOptionalUUID(r.FormValue("reservation_id")),
		TemplateID:    parseOptionalUUID(r.FormValue("template_id")),
		Recipient:     r.FormValue("recipient"),
		Subject:       r.FormValue("subject"),
		BodyText:      r.FormValue("body_text"),
	}); err != nil {
		h.redirectErr(w, r, "/guests/"+guestID.String(), err)
		return
	}
	redirectMsg(w, r, "/guests/"+guestID.String(), "Email queued for delivery via outbox.")
}

func (h *Handler) handleCommFollowUpCompletePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	commID, _ := uuid.Parse(r.PathValue("id"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Profile", "guests")
	if err != nil {
		return
	}
	guestID := strings.TrimSpace(r.FormValue("guest_id"))
	back := "/guests"
	if guestID != "" {
		back = "/guests/" + guestID
	}
	if err := h.svc.CompleteCommunicationFollowUp(r.Context(), actor, base.Property.ID, commID); err != nil {
		h.redirectErr(w, r, back, err)
		return
	}
	redirectMsg(w, r, back, "Follow-up marked complete.")
}

func (h *Handler) handleGuestConsentPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	guestID, _ := uuid.Parse(r.PathValue("id"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Profile", "guests")
	if err != nil {
		return
	}
	if _, err := h.svc.RecordGuestConsent(r.Context(), actor, domain.GuestConsentRecord{
		PropertyID:    base.Property.ID,
		GuestID:       guestID,
		ConsentType:   "MARKETING_EMAIL",
		Status:        domain.ConsentStatus(r.FormValue("status")),
		Method:        r.FormValue("method"),
		Source:        r.FormValue("source"),
		PolicyVersion: r.FormValue("policy_version"),
		Notes:         r.FormValue("notes"),
	}); err != nil {
		h.redirectErr(w, r, "/guests/"+guestID.String(), err)
		return
	}
	redirectMsg(w, r, "/guests/"+guestID.String(), "Marketing consent record saved.")
}

func (h *Handler) handleGuestExportGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	guestID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Guest", "invalid guest id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Export", "guests")
	if err != nil {
		return
	}
	bundle, err := h.svc.ExportGuestData(r.Context(), actor, base.Property.ID, guestID)
	if err != nil {
		h.renderDomainError(w, err, "Export Failed", "could not export guest data")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"guest-export-"+guestID.String()[:8]+".json\"")
	_ = json.NewEncoder(w).Encode(bundle)
}

func (h *Handler) handleGuestAnonymizePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	guestID, _ := uuid.Parse(r.PathValue("id"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Guest Profile", "guests")
	if err != nil {
		return
	}
	if _, err := h.svc.AnonymizeGuest(r.Context(), actor, base.Property.ID, guestID, r.FormValue("reason")); err != nil {
		h.redirectErr(w, r, "/guests/"+guestID.String(), err)
		return
	}
	redirectMsg(w, r, "/guests/"+guestID.String(), "Guest personal data anonymized while preserving financial ledger records.")
}

func (h *Handler) handleGuestMergeQueueGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Duplicate Guest Merge", "guest-merge")
	if err != nil {
		return
	}
	scan, err := h.svc.ListDuplicateCandidates(r.Context(), actor, base.Property.ID)
	if err != nil {
		h.renderDomainError(w, err, "Duplicate Review", "unable to load duplicate candidates")
		return
	}
	guests, _ := h.svc.ListGuests(r.Context(), actor, base.Property.ID, domain.GuestDirectoryFilter{Limit: 100})
	h.renderPage(w, "guest_merge.html", struct {
		baseView
		Duplicates    []domain.GuestDuplicateCandidate
		DuplicateScan domain.DuplicateScanResult
		AllGuests     []domain.GuestListItem
	}{
		baseView:      base,
		Duplicates:    scan.Candidates,
		DuplicateScan: scan,
		AllGuests:     guests,
	})
}

func (h *Handler) handleGuestMergePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Duplicate Guest Merge", "guest-merge")
	if err != nil {
		return
	}
	srcID, err1 := uuid.Parse(r.FormValue("source_guest_id"))
	tgtID, err2 := uuid.Parse(r.FormValue("target_guest_id"))
	if err1 != nil || err2 != nil {
		redirectMsg(w, r, "/guests/merge", "Please select valid source and target guests.")
		return
	}
	target, _, err := h.svc.MergeGuests(r.Context(), actor, base.Property.ID, srcID, tgtID, r.FormValue("notes"))
	if err != nil {
		h.redirectErr(w, r, "/guests/merge", err)
		return
	}
	redirectMsg(w, r, "/guests/"+target.ID.String(), "Duplicate guest merged cleanly with full history preserved.")
}

func (h *Handler) handleGuestDuplicateDismissPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Duplicate Guest Merge", "guest-merge")
	if err != nil {
		return
	}
	idA, err1 := uuid.Parse(r.FormValue("guest_a_id"))
	idB, err2 := uuid.Parse(r.FormValue("guest_b_id"))
	if err1 != nil || err2 != nil {
		redirectMsg(w, r, "/guests/merge", "Invalid duplicate pair.")
		return
	}
	if err := h.svc.DismissGuestDuplicateWithCategory(r.Context(), actor, base.Property.ID, idA, idB, r.FormValue("reason_category"), r.FormValue("reason")); err != nil {
		h.redirectErr(w, r, "/guests/merge", err)
		return
	}
	redirectMsg(w, r, "/guests/merge", "Duplicate pair dismissed.")
}

func (h *Handler) handleEnquiriesListGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Enquiries & Requests", "enquiries")
	if err != nil {
		return
	}
	q := r.URL.Query()
	filter := domain.GuestEnquiryFilter{
		Query:    q.Get("q"),
		Status:   q.Get("status"),
		Category: q.Get("category"),
		Priority: q.Get("priority"),
		OpenOnly: q.Get("open") == "1",
		Overdue:  q.Get("overdue") == "1",
	}
	items, err := h.svc.ListGuestEnquiries(r.Context(), actor, base.Property.ID, filter)
	if err != nil {
		h.renderDomainError(w, err, "Enquiries Error", "unable to load enquiries")
		return
	}
	guests, _ := h.svc.ListGuests(r.Context(), actor, base.Property.ID, domain.GuestDirectoryFilter{Limit: 100})
	staff, _ := h.svc.ListUsers(r.Context(), actor)
	h.renderPage(w, "enquiries_list.html", struct {
		baseView
		Enquiries []domain.GuestEnquiry
		Filter    domain.GuestEnquiryFilter
		Guests    []domain.GuestListItem
		Staff     []domain.AdminUser
	}{
		baseView:  base,
		Enquiries: items,
		Filter:    filter,
		Guests:    guests,
		Staff:     staff,
	})
}

func (h *Handler) handleEnquiryCreatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Enquiries & Requests", "enquiries")
	if err != nil {
		return
	}
	var followUpAt *time.Time
	if raw := strings.TrimSpace(r.FormValue("follow_up_at")); raw != "" {
		if t, err := time.Parse("2006-01-02", raw); err == nil {
			followUpAt = &t
		}
	}
	created, err := h.svc.CreateGuestEnquiry(r.Context(), actor, domain.GuestEnquiry{
		PropertyID:           base.Property.ID,
		GuestID:              parseOptionalUUID(r.FormValue("guest_id")),
		ReservationID:        parseOptionalUUID(r.FormValue("reservation_id")),
		MaintenanceRequestID: parseOptionalUUID(r.FormValue("maintenance_request_id")),
		Category:             domain.EnquiryCategory(r.FormValue("category")),
		Priority:             domain.EnquiryPriority(r.FormValue("priority")),
		Source:               domain.EnquirySource(r.FormValue("source")),
		Subject:              r.FormValue("subject"),
		Description:          r.FormValue("description"),
		RequesterName:        r.FormValue("requester_name"),
		RequesterEmail:       r.FormValue("requester_email"),
		RequesterPhone:       r.FormValue("requester_phone"),
		AssignedUserID:       parseOptionalUUID(r.FormValue("assigned_user_id")),
		FollowUpAt:           followUpAt,
	})
	if err != nil {
		h.redirectErr(w, r, "/enquiries", err)
		return
	}
	redirectMsg(w, r, "/enquiries/"+created.ID.String(), "Enquiry logged.")
}

func (h *Handler) handleEnquiryDetailGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	enqID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid enquiry id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Enquiry Detail", "enquiries")
	if err != nil {
		return
	}
	enq, msgs, err := h.svc.GetGuestEnquiryDetail(r.Context(), actor, base.Property.ID, enqID)
	if err != nil {
		h.renderDomainError(w, err, "Enquiry Not Found", "unable to load enquiry")
		return
	}
	staff, _ := h.svc.ListUsers(r.Context(), actor)
	maintReqs, _ := h.svc.ListMaintenanceRequests(r.Context(), actor, base.Property.ID, domain.MaintenanceRequestFilter{Status: "ALL"})
	h.renderPage(w, "enquiry_detail.html", struct {
		baseView
		Enquiry   domain.GuestEnquiry
		Messages  []domain.GuestEnquiryMessage
		Staff     []domain.AdminUser
		MaintReqs []domain.MaintenanceRequest
	}{
		baseView:  base,
		Enquiry:   enq,
		Messages:  msgs,
		Staff:     staff,
		MaintReqs: maintReqs,
	})
}

func (h *Handler) handleEnquiryUpdatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	enqID, _ := uuid.Parse(r.PathValue("id"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Enquiry Detail", "enquiries")
	if err != nil {
		return
	}
	var followUpAt *time.Time
	if raw := strings.TrimSpace(r.FormValue("follow_up_at")); raw != "" {
		if t, err := time.Parse("2006-01-02", raw); err == nil {
			followUpAt = &t
		}
	}
	if _, err := h.svc.UpdateGuestEnquiry(r.Context(), actor, domain.GuestEnquiry{
		ID:                   enqID,
		PropertyID:           base.Property.ID,
		GuestID:              parseOptionalUUID(r.FormValue("guest_id")),
		ReservationID:        parseOptionalUUID(r.FormValue("reservation_id")),
		MaintenanceRequestID: parseOptionalUUID(r.FormValue("maintenance_request_id")),
		Category:             domain.EnquiryCategory(r.FormValue("category")),
		Status:               domain.EnquiryStatus(r.FormValue("status")),
		Priority:             domain.EnquiryPriority(r.FormValue("priority")),
		Subject:              r.FormValue("subject"),
		Description:          r.FormValue("description"),
		AssignedUserID:       parseOptionalUUID(r.FormValue("assigned_user_id")),
		FollowUpAt:           followUpAt,
		ResolutionNotes:      r.FormValue("resolution_notes"),
		Version:              parseIntDefault(r.FormValue("version"), 1),
	}); err != nil {
		h.redirectErr(w, r, "/enquiries/"+enqID.String(), err)
		return
	}
	redirectMsg(w, r, "/enquiries/"+enqID.String(), "Enquiry updated.")
}

func (h *Handler) handleEnquiryMessagePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	enqID, _ := uuid.Parse(r.PathValue("id"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Enquiry Detail", "enquiries")
	if err != nil {
		return
	}
	isInternal := r.FormValue("is_internal") == "true"
	if _, err := h.svc.AddGuestEnquiryMessage(r.Context(), actor, domain.GuestEnquiryMessage{
		PropertyID: base.Property.ID,
		EnquiryID:  enqID,
		Body:       r.FormValue("body"),
		IsInternal: isInternal,
	}); err != nil {
		h.redirectErr(w, r, "/enquiries/"+enqID.String(), err)
		return
	}
	redirectMsg(w, r, "/enquiries/"+enqID.String(), "Enquiry message added.")
}

func (h *Handler) handleCommsSettingsGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Templates & Automation", "comms-settings")
	if err != nil {
		return
	}
	templates, err := h.svc.ListCommunicationTemplates(r.Context(), actor, base.Property.ID)
	if err != nil {
		h.renderDomainError(w, err, "Communications Settings", "access denied or unavailable")
		return
	}
	rules, _ := h.svc.ListCommunicationAutomationRules(r.Context(), actor, base.Property.ID)
	jobs, _ := h.svc.ListCommunicationJobs(r.Context(), actor, base.Property.ID)
	failedEmails, _ := h.svc.ListFailedPropertyEmails(r.Context(), actor, base.Property.ID)
	retention, _ := h.svc.GetPropertyRetentionPolicy(r.Context(), actor, base.Property.ID)

	h.renderPage(w, "comms_settings.html", struct {
		baseView
		Templates    []domain.CommunicationTemplate
		Rules        []domain.CommunicationAutomationRule
		Jobs         []domain.CommunicationJob
		FailedEmails []domain.FailedEmailSummary
		Retention    domain.PropertyRetentionPolicy
		Placeholders map[string]string
	}{
		baseView:     base,
		Templates:    templates,
		Rules:        rules,
		Jobs:         jobs,
		FailedEmails: failedEmails,
		Retention:    retention,
		Placeholders: domain.AllowedTemplatePlaceholders,
	})
}

func (h *Handler) handleTemplateSavePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Templates & Automation", "comms-settings")
	if err != nil {
		return
	}
	if _, err := h.svc.CreateCommunicationTemplate(r.Context(), actor, domain.CommunicationTemplate{
		PropertyID:       base.Property.ID,
		Code:             r.FormValue("code"),
		Name:             r.FormValue("name"),
		Category:         r.FormValue("category"),
		Locale:           r.FormValue("locale"),
		SubjectTemplate:  r.FormValue("subject_template"),
		BodyTextTemplate: r.FormValue("body_text_template"),
		BodyHTMLTemplate: r.FormValue("body_html_template"),
		IsActive:         r.FormValue("is_active") != "false",
	}); err != nil {
		h.redirectErr(w, r, "/communications/settings", err)
		return
	}
	redirectMsg(w, r, "/communications/settings", "Communication template created.")
}

func (h *Handler) handleTemplateUpdatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	tplID, _ := uuid.Parse(r.PathValue("id"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Templates & Automation", "comms-settings")
	if err != nil {
		return
	}
	if _, err := h.svc.UpdateCommunicationTemplate(r.Context(), actor, domain.CommunicationTemplate{
		ID:               tplID,
		PropertyID:       base.Property.ID,
		Name:             r.FormValue("name"),
		Category:         r.FormValue("category"),
		SubjectTemplate:  r.FormValue("subject_template"),
		BodyTextTemplate: r.FormValue("body_text_template"),
		BodyHTMLTemplate: r.FormValue("body_html_template"),
		IsActive:         r.FormValue("is_active") == "true",
		Version:          parseIntDefault(r.FormValue("version"), 1),
	}); err != nil {
		h.redirectErr(w, r, "/communications/settings", err)
		return
	}
	redirectMsg(w, r, "/communications/settings", "Template updated (new immutable version recorded).")
}

func (h *Handler) handleAutomationRuleUpdatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	ruleID, _ := uuid.Parse(r.PathValue("id"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Templates & Automation", "comms-settings")
	if err != nil {
		return
	}
	tplID, _ := uuid.Parse(r.FormValue("template_id"))
	var sendHour *int
	if hStr := strings.TrimSpace(r.FormValue("send_hour_local")); hStr != "" {
		v := parseIntDefault(hStr, 9)
		sendHour = &v
	}
	statuses := strings.Split(r.FormValue("applicable_statuses"), ",")
	var cleanStatuses []string
	for _, st := range statuses {
		if s := strings.TrimSpace(strings.ToUpper(st)); s != "" {
			cleanStatuses = append(cleanStatuses, s)
		}
	}
	if len(cleanStatuses) == 0 {
		cleanStatuses = []string{"CONFIRMED"}
	}
	if _, err := h.svc.UpdateCommunicationAutomationRule(r.Context(), actor, domain.CommunicationAutomationRule{
		ID:                 ruleID,
		PropertyID:         base.Property.ID,
		Name:               r.FormValue("name"),
		OffsetHours:        parseIntDefault(r.FormValue("offset_hours"), 0),
		SendHourLocal:      sendHour,
		TemplateID:         tplID,
		ApplicableStatuses: cleanStatuses,
		IsActive:           r.FormValue("is_active") == "true",
	}); err != nil {
		h.redirectErr(w, r, "/communications/settings", err)
		return
	}
	redirectMsg(w, r, "/communications/settings", "Automation rule updated.")
}

func (h *Handler) handleFailedEmailRetryPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	outboxID, _ := uuid.Parse(r.PathValue("outboxID"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Templates & Automation", "comms-settings")
	if err != nil {
		return
	}
	if err := h.svc.RetryFailedOutboxEmail(r.Context(), actor, base.Property.ID, outboxID); err != nil {
		h.redirectErr(w, r, "/communications/settings", err)
		return
	}
	redirectMsg(w, r, "/communications/settings", "Failed email queued for immediate retry.")
}

func (h *Handler) handleOutboxReconcilePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	outboxID, _ := uuid.Parse(r.PathValue("outboxID"))
	base, err := h.buildBaseView(r.Context(), r, actor, "Templates & Automation", "comms-settings")
	if err != nil {
		return
	}
	if _, err := h.svc.ReconcileUnknownOutboxDispatch(r.Context(), actor, service.ReconcileOutboxInput{
		PropertyID:   base.Property.ID,
		OutboxID:     outboxID,
		Resolution:   r.FormValue("resolution"),
		ReasonCode:   r.FormValue("reason_code"),
		EvidenceType: r.FormValue("evidence_type"),
		EvidenceRef:  r.FormValue("evidence_ref"),
		Note:         r.FormValue("note"),
	}); err != nil {
		h.redirectErr(w, r, "/communications/settings", err)
		return
	}
	redirectMsg(w, r, "/communications/settings", "Uncertain dispatch reconciled.")
}

func (h *Handler) handleRetentionPolicyPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Templates & Automation", "comms-settings")
	if err != nil {
		return
	}
	if _, err := h.svc.UpdatePropertyRetentionPolicy(r.Context(), actor, domain.PropertyRetentionPolicy{
		PropertyID:                 base.Property.ID,
		EnquiryRetentionDays:       parseIntDefault(r.FormValue("enquiry_retention_days"), 1095),
		InactiveGuestRetentionDays: parseIntDefault(r.FormValue("inactive_guest_retention_days"), 2555),
		CommsRetentionDays:         parseIntDefault(r.FormValue("comms_retention_days"), 1095),
		AutoAnonymizeEnabled:       r.FormValue("auto_anonymize_enabled") == "true",
	}); err != nil {
		h.redirectErr(w, r, "/communications/settings", err)
		return
	}
	redirectMsg(w, r, "/communications/settings", "Data retention policy updated.")
}
