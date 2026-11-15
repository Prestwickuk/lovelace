package httpapi

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func (h *Handler) registerCalendarChannelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/calendar", h.requireAuth(false, h.handleGetCalendar))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/availability-check", h.requireAuth(false, h.handleCheckUnitAvailability))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/integrations/channel-manager", h.requireAuth(false, h.handleListChannelIntegrations))
	mux.HandleFunc("PUT /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}", h.requireAuth(true, h.handleSaveChannelIntegration))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/status", h.requireAuth(true, h.handleSetChannelIntegrationStatus))
	mux.HandleFunc("DELETE /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}", h.requireAuth(true, h.handleDeleteChannelIntegration))

	mux.HandleFunc("POST /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/verify", h.requireAuth(true, h.handleVerifyChannelIntegration))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/disconnect", h.requireAuth(true, h.handleDisconnectChannelIntegration))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/listings", h.requireAuth(false, h.handleDiscoverChannelListings))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/health", h.requireAuth(false, h.handleGetChannelHealth))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/mappings", h.requireAuth(false, h.handleListChannelMappings))
	mux.HandleFunc("PUT /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/mappings", h.requireAuth(true, h.handleUpsertChannelMapping))
	mux.HandleFunc("DELETE /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/mappings/{mappingID}", h.requireAuth(true, h.handleDeleteChannelMapping))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/sync/preview", h.requireAuth(true, h.handlePreviewChannelSync))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/integrations/channel-manager/{providerCode}/sync", h.requireAuth(true, h.handleTriggerChannelSync))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/integrations/channel-manager/jobs/process", h.requireAuth(true, h.handleProcessChannelJobs))

	mux.HandleFunc("POST /api/v1/webhooks/channels/{providerCode}/{connectionID}", h.handleChannelWebhook)
}

func (h *Handler) handleGetCalendar(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	cq := service.CalendarQuery{
		PropertyID:  propID,
		View:        q.Get("view"),
		ChannelCode: q.Get("channel_code"),
		Status:      q.Get("status"),
	}
	if sRaw := q.Get("start_date"); sRaw != "" {
		d, err := domain.ParseDate(sRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", "start_date must be YYYY-MM-DD")
			return
		}
		cq.StartDate = d
	}
	if eRaw := q.Get("end_date"); eRaw != "" {
		d, err := domain.ParseDate(eRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD")
			return
		}
		cq.EndDate = d
	}
	if utRaw := q.Get("unit_type_id"); utRaw != "" {
		utID, err := uuid.Parse(utRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_id", "invalid unit_type_id")
			return
		}
		cq.UnitTypeID = &utID
	}

	viewData, err := h.svc.GetCalendarView(r.Context(), actor, cq)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewData)
}

func (h *Handler) handleCheckUnitAvailability(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	unitID, err := uuid.Parse(q.Get("unit_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "valid unit_id query parameter is required")
		return
	}
	var sDate, eDate time.Time
	sDate, err = domain.ParseDate(q.Get("start_date"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "start_date must be YYYY-MM-DD")
		return
	}
	eDate, err = domain.ParseDate(q.Get("end_date"))
	if err != nil || !eDate.After(sDate) {
		writeError(w, http.StatusBadRequest, "invalid_date", "end_date must be YYYY-MM-DD after start_date")
		return
	}

	res, err := h.svc.CheckUnitAvailability(r.Context(), actor, propID, unitID, q.Get("channel_code"), sDate, eDate)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleListChannelIntegrations(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListChannelProviders(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

func (h *Handler) handleSaveChannelIntegration(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	providerCode := r.PathValue("providerCode")
	var body struct {
		DisplayName string            `json:"display_name"`
		Inputs      map[string]string `json:"inputs"`
		Status      string            `json:"status"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	saved, err := h.svc.SaveChannelConfig(r.Context(), actor, service.SaveChannelConfigInput{
		PropertyID:   propID,
		ProviderCode: providerCode,
		DisplayName:  body.DisplayName,
		Inputs:       body.Inputs,
		TargetStatus: channels.ConnectionStatus(body.Status),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *Handler) handleSetChannelIntegrationStatus(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	providerCode := r.PathValue("providerCode")
	var body struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	updated, err := h.svc.SetChannelStatus(r.Context(), actor, propID, providerCode, channels.ConnectionStatus(body.Status))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleDeleteChannelIntegration(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	providerCode := r.PathValue("providerCode")
	if err := h.svc.DeleteChannelConfig(r.Context(), actor, propID, providerCode); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) handleVerifyChannelIntegration(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	res, err := h.svc.VerifyChannelConnection(r.Context(), actor, propID, r.PathValue("providerCode"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleDisconnectChannelIntegration(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	res, err := h.svc.DisconnectChannelConnection(r.Context(), actor, propID, r.PathValue("providerCode"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleDiscoverChannelListings(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.DiscoverChannelListings(r.Context(), actor, propID, r.PathValue("providerCode"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

func (h *Handler) handleGetChannelHealth(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	summary, err := h.svc.GetChannelHealth(r.Context(), actor, propID, r.PathValue("providerCode"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *Handler) handleListChannelMappings(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListChannelListingMappings(r.Context(), actor, propID, r.PathValue("providerCode"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

func (h *Handler) handleUpsertChannelMapping(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body service.UpsertChannelMappingInput
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.ProviderCode = r.PathValue("providerCode")
	saved, err := h.svc.UpsertChannelListingMapping(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *Handler) handleDeleteChannelMapping(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	mapID, ok := parseUUIDParam(w, r, "mappingID")
	if !ok {
		return
	}
	if err := h.svc.DeleteChannelListingMapping(r.Context(), actor, propID, mapID); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) handlePreviewChannelSync(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body service.ChannelSyncRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.ProviderCode = r.PathValue("providerCode")
	prev, err := h.svc.PreviewChannelSync(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, prev)
}

func (h *Handler) handleTriggerChannelSync(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body service.ChannelSyncRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	body.ProviderCode = r.PathValue("providerCode")
	res, err := h.svc.TriggerChannelSync(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}

func (h *Handler) handleProcessChannelJobs(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	if !auth.HasPermission(actor.Role, auth.PermChannelsWrite) || (actor.PropertyID != nil && *actor.PropertyID != propID) {
		writeDomainError(w, domain.ErrForbidden)
		return
	}
	processed, err := h.svc.ProcessPendingChannelSyncJobs(r.Context(), 20)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"processed": processed})
}

func (h *Handler) handleChannelWebhook(w http.ResponseWriter, r *http.Request) {
	connID, ok := parseUUIDParam(w, r, "connectionID")
	if !ok {
		return
	}
	providerCode := r.PathValue("providerCode")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_payload", "unable to read webhook payload")
		return
	}
	headers := make(map[string]string, len(r.Header))
	for k, vals := range r.Header {
		if len(vals) > 0 {
			headers[k] = strings.Join(vals, ",")
		}
	}
	endpointToken := r.URL.Query().Get("token")
	if endpointToken == "" {
		endpointToken = r.Header.Get("X-Lovelace-Webhook-Token")
	}
	res, err := h.svc.ProcessChannelWebhook(r.Context(), providerCode, connID, endpointToken, r.URL.Path, headers, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	status := http.StatusAccepted
	if res.Deduplicated {
		status = http.StatusOK
	}
	writeJSON(w, status, res)
}
