package httpapi

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func (h *Handler) registerGuestCRMRoutes(mux *http.ServeMux) {
	// Signed transport webhook (provider delivery/bounce events only).
	mux.HandleFunc("POST /api/v1/webhooks/email-events", h.handleEmailDeliveryWebhook)

	// Guest directory, profiles, preferences, notes, duplicate merges, GDPR.
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/guests", h.requireAuth(false, h.handleListGuests))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/guests", h.requireAuth(true, h.handleCreateGuest))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/guests/duplicates", h.requireAuth(false, h.handleListGuestDuplicates))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/guests/duplicates/dismiss", h.requireAuth(true, h.handleDismissGuestDuplicate))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/guests/merge", h.requireAuth(true, h.handleMergeGuests))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/guests/{guestID}", h.requireAuth(false, h.handleGetGuestDetail))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/guests/{guestID}", h.requireAuth(true, h.handleUpdateGuest))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/guests/{guestID}/preferences", h.requireAuth(true, h.handleAddGuestPreference))
	mux.HandleFunc("DELETE /api/v1/properties/{propertyID}/guests/{guestID}/preferences/{prefID}", h.requireAuth(true, h.handleDeleteGuestPreference))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/guests/{guestID}/notes", h.requireAuth(true, h.handleAddGuestNote))
	mux.HandleFunc("DELETE /api/v1/properties/{propertyID}/guests/{guestID}/notes/{noteID}", h.requireAuth(true, h.handleDeleteGuestNote))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/guests/{guestID}/consent", h.requireAuth(true, h.handleRecordGuestConsent))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/guests/{guestID}/export", h.requireAuth(false, h.handleExportGuestData))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/guests/{guestID}/anonymize", h.requireAuth(true, h.handleAnonymizeGuest))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/guests/{guestID}/portal/send-verification", h.requireAuth(true, h.handleSendGuestPortalVerification))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/guests/{guestID}/portal/revoke-sessions", h.requireAuth(true, h.handleRevokeGuestPortalSessions))

	// Guest enquiries, special requests & complaints.
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/enquiries", h.requireAuth(false, h.handleListGuestEnquiries))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/enquiries", h.requireAuth(true, h.handleCreateGuestEnquiry))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/enquiries/{enquiryID}", h.requireAuth(false, h.handleGetGuestEnquiry))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/enquiries/{enquiryID}", h.requireAuth(true, h.handleUpdateGuestEnquiry))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/enquiries/{enquiryID}/messages", h.requireAuth(true, h.handleAddGuestEnquiryMessage))

	// Communications, outbound send, follow-ups, failed outbox retries.
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/guests/{guestID}/interactions", h.requireAuth(true, h.handleLogGuestInteraction))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/guests/{guestID}/emails", h.requireAuth(true, h.handleSendGuestEmail))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/communications/{commID}/complete-followup", h.requireAuth(true, h.handleCompleteCommFollowUp))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/failed-emails", h.requireAuth(false, h.handleListFailedEmails))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/failed-emails/{outboxID}/retry", h.requireAuth(true, h.handleRetryFailedEmail))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/outbox/{outboxID}/reconcile", h.requireAuth(true, h.handleReconcileOutboxDispatch))

	// Templates, automation rules, scheduled jobs & retention policy.
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/communication-templates", h.requireAuth(false, h.handleListTemplates))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/communication-templates", h.requireAuth(true, h.handleCreateTemplate))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/communication-templates/{templateID}", h.requireAuth(true, h.handleUpdateTemplate))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/communication-templates/{templateID}/preview", h.requireAuth(true, h.handlePreviewTemplate))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/automation-rules", h.requireAuth(false, h.handleListAutomationRules))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/automation-rules", h.requireAuth(true, h.handleCreateAutomationRule))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/automation-rules/{ruleID}", h.requireAuth(true, h.handleUpdateAutomationRule))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/communication-jobs", h.requireAuth(false, h.handleListCommunicationJobs))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/retention-policy", h.requireAuth(false, h.handleGetRetentionPolicy))
	mux.HandleFunc("PUT /api/v1/properties/{propertyID}/retention-policy", h.requireAuth(true, h.handleUpdateRetentionPolicy))
}

func (h *Handler) handleEmailDeliveryWebhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "failed to read webhook payload")
		return
	}
	sig := r.Header.Get("X-Email-Signature")
	if err := h.svc.ProcessEmailDeliveryWebhook(r.Context(), body, sig); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (h *Handler) handleListGuests(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListGuests(r.Context(), actor, propID, domain.GuestDirectoryFilter{
		Query:      r.URL.Query().Get("q"),
		Segment:    r.URL.Query().Get("segment"),
		Status:     r.URL.Query().Get("status"),
		IncludeAll: r.URL.Query().Get("include_all") == "true",
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"guests": items})
}

func (h *Handler) handleGetGuestDetail(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	detail, err := h.svc.GetGuestDetail(r.Context(), actor, propID, guestID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) handleCreateGuest(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body domain.Guest
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	created, err := h.svc.CreateGuest(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleUpdateGuest(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	var body domain.Guest
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.ID = guestID
	updated, err := h.svc.UpdateGuest(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleListGuestDuplicates(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	scan, err := h.svc.ListDuplicateCandidates(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"duplicates":          scan.Candidates,
		"scanned_guests":      scan.ScannedGuests,
		"total_active_guests": scan.TotalActiveGuests,
		"is_complete":         scan.IsComplete,
	})
}

func (h *Handler) handleDismissGuestDuplicate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		GuestAID       uuid.UUID `json:"guest_a_id"`
		GuestBID       uuid.UUID `json:"guest_b_id"`
		ReasonCategory string    `json:"reason_category"`
		Reason         string    `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.DismissGuestDuplicateWithCategory(r.Context(), actor, propID, body.GuestAID, body.GuestBID, body.ReasonCategory, body.Reason); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

func (h *Handler) handleMergeGuests(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		SourceGuestID uuid.UUID `json:"source_guest_id"`
		TargetGuestID uuid.UUID `json:"target_guest_id"`
		Notes         string    `json:"notes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	target, ev, err := h.svc.MergeGuests(r.Context(), actor, propID, body.SourceGuestID, body.TargetGuestID, body.Notes)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"guest": target, "merge_event": ev})
}

func (h *Handler) handleAddGuestPreference(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	var body domain.GuestPreference
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.GuestID = guestID
	out, err := h.svc.AddGuestPreference(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleDeleteGuestPreference(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	prefID, ok := parseUUIDParam(w, r, "prefID")
	if !ok {
		return
	}
	if err := h.svc.RemoveGuestPreference(r.Context(), actor, propID, guestID, prefID); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) handleAddGuestNote(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	var body domain.GuestNote
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.GuestID = guestID
	out, err := h.svc.AddGuestNote(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleDeleteGuestNote(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	noteID, ok := parseUUIDParam(w, r, "noteID")
	if !ok {
		return
	}
	if err := h.svc.RemoveGuestNote(r.Context(), actor, propID, guestID, noteID); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) handleRecordGuestConsent(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	var body domain.GuestConsentRecord
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.GuestID = guestID
	body.OccurredAt = time.Time{} // server-side authoritative timestamp only
	out, err := h.svc.RecordGuestConsent(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleExportGuestData(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	bundle, err := h.svc.ExportGuestData(r.Context(), actor, propID, guestID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\"guest-export-"+guestID.String()[:8]+".json\"")
	writeJSON(w, http.StatusOK, bundle)
}

func (h *Handler) handleAnonymizeGuest(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	out, err := h.svc.AnonymizeGuest(r.Context(), actor, propID, guestID, body.Reason)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handleListGuestEnquiries(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	var guestPtr, assignPtr *uuid.UUID
	if v := strings.TrimSpace(q.Get("guest_id")); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			guestPtr = &id
		}
	}
	if v := strings.TrimSpace(q.Get("assigned_to")); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			assignPtr = &id
		}
	}
	items, err := h.svc.ListGuestEnquiries(r.Context(), actor, propID, domain.GuestEnquiryFilter{
		Query:      q.Get("q"),
		Status:     q.Get("status"),
		Category:   q.Get("category"),
		Priority:   q.Get("priority"),
		GuestID:    guestPtr,
		AssignedTo: assignPtr,
		OpenOnly:   q.Get("open") == "true" || q.Get("open") == "1",
		Overdue:    q.Get("overdue") == "true" || q.Get("overdue") == "1",
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enquiries": items})
}

func (h *Handler) handleCreateGuestEnquiry(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body domain.GuestEnquiry
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	out, err := h.svc.CreateGuestEnquiry(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleGetGuestEnquiry(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	enqID, ok := parseUUIDParam(w, r, "enquiryID")
	if !ok {
		return
	}
	enq, msgs, err := h.svc.GetGuestEnquiryDetail(r.Context(), actor, propID, enqID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enquiry": enq, "messages": msgs})
}

func (h *Handler) handleUpdateGuestEnquiry(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	enqID, ok := parseUUIDParam(w, r, "enquiryID")
	if !ok {
		return
	}
	var body domain.GuestEnquiry
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.ID = enqID
	out, err := h.svc.UpdateGuestEnquiry(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handleAddGuestEnquiryMessage(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	enqID, ok := parseUUIDParam(w, r, "enquiryID")
	if !ok {
		return
	}
	var body domain.GuestEnquiryMessage
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.EnquiryID = enqID
	out, err := h.svc.AddGuestEnquiryMessage(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleLogGuestInteraction(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	var body domain.GuestCommunication
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.GuestID = &guestID
	if body.OccurredAt.IsZero() {
		body.OccurredAt = time.Now().UTC()
	}
	out, err := h.svc.LogGuestInteraction(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleSendGuestEmail(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	var body service.SendGuestEmailInput
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.GuestID = guestID
	out, err := h.svc.SendGuestEmail(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleCompleteCommFollowUp(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	commID, ok := parseUUIDParam(w, r, "commID")
	if !ok {
		return
	}
	if err := h.svc.CompleteCommunicationFollowUp(r.Context(), actor, propID, commID); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "completed"})
}

func (h *Handler) handleListFailedEmails(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListFailedPropertyEmails(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"failed_emails": items})
}

func (h *Handler) handleRetryFailedEmail(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	outboxID, ok := parseUUIDParam(w, r, "outboxID")
	if !ok {
		return
	}
	if err := h.svc.RetryFailedOutboxEmail(r.Context(), actor, propID, outboxID); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "queued"})
}

func (h *Handler) handleReconcileOutboxDispatch(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	outboxID, ok := parseUUIDParam(w, r, "outboxID")
	if !ok {
		return
	}
	var req struct {
		Resolution   string `json:"resolution"`
		ReasonCode   string `json:"reason_code"`
		EvidenceType string `json:"evidence_type"`
		EvidenceRef  string `json:"evidence_ref"`
		Note         string `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := h.svc.ReconcileUnknownOutboxDispatch(r.Context(), actor, service.ReconcileOutboxInput{
		PropertyID:   propID,
		OutboxID:     outboxID,
		Resolution:   req.Resolution,
		ReasonCode:   req.ReasonCode,
		EvidenceType: req.EvidenceType,
		EvidenceRef:  req.EvidenceRef,
		Note:         req.Note,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Handler) handleListTemplates(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListCommunicationTemplates(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": items})
}

func (h *Handler) handleCreateTemplate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body domain.CommunicationTemplate
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	out, err := h.svc.CreateCommunicationTemplate(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleUpdateTemplate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	templateID, ok := parseUUIDParam(w, r, "templateID")
	if !ok {
		return
	}
	var body domain.CommunicationTemplate
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.ID = templateID
	out, err := h.svc.UpdateCommunicationTemplate(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handlePreviewTemplate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	templateID, ok := parseUUIDParam(w, r, "templateID")
	if !ok {
		return
	}
	var body struct {
		GuestID       *uuid.UUID `json:"guest_id"`
		ReservationID *uuid.UUID `json:"reservation_id"`
		EnquiryID     *uuid.UUID `json:"enquiry_id"`
	}
	if !decodeOptionalJSON(w, r, &body) {
		return
	}
	subj, txt, htm, err := h.svc.PreviewCommunicationTemplateWithEnquiry(r.Context(), actor, propID, templateID, body.GuestID, body.ReservationID, body.EnquiryID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"subject":   subj,
		"body_text": txt,
		"body_html": htm,
	})
}

func (h *Handler) handleListAutomationRules(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListCommunicationAutomationRules(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": items})
}

func (h *Handler) handleCreateAutomationRule(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body domain.CommunicationAutomationRule
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	out, err := h.svc.CreateCommunicationAutomationRule(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleUpdateAutomationRule(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	ruleID, ok := parseUUIDParam(w, r, "ruleID")
	if !ok {
		return
	}
	var body domain.CommunicationAutomationRule
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.ID = ruleID
	out, err := h.svc.UpdateCommunicationAutomationRule(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handleListCommunicationJobs(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListCommunicationJobs(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": items})
}

func (h *Handler) handleGetRetentionPolicy(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	pol, err := h.svc.GetPropertyRetentionPolicy(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pol)
}

func (h *Handler) handleUpdateRetentionPolicy(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body domain.PropertyRetentionPolicy
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	out, err := h.svc.UpdatePropertyRetentionPolicy(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handleSendGuestPortalVerification(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	if err := h.svc.SendGuestPortalVerification(r.Context(), actor, propID, guestID); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "queued",
	})
}

func (h *Handler) handleRevokeGuestPortalSessions(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	guestID, ok := parseUUIDParam(w, r, "guestID")
	if !ok {
		return
	}
	revoked, err := h.svc.RevokeGuestPortalSessions(r.Context(), actor, propID, guestID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"revoked_count": revoked,
	})
}
