package httpapi

import (
	"net/http"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func (h *Handler) registerWorkspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/workspace/dashboard", h.requireAuth(false, h.handleGetWorkspaceDashboard))
	mux.HandleFunc("PUT /api/v1/properties/{propertyID}/workspace/dashboard", h.requireAuth(true, h.handlePutWorkspaceDashboard))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/workspace/dashboard/reset", h.requireAuth(true, h.handleResetWorkspaceDashboard))
}

func (h *Handler) handleGetWorkspaceDashboard(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	ws, err := h.svc.ResolveWorkspace(r.Context(), actor, &propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	data, err := h.svc.GetWorkspaceDashboard(r.Context(), actor, actor.UserID, propID, ws)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (h *Handler) handlePutWorkspaceDashboard(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body domain.DashboardLayout
	if !decodeJSON(w, r, &body) {
		return
	}
	saved, err := h.svc.SaveDashboardLayout(r.Context(), actor, actor.UserID, propID, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *Handler) handleResetWorkspaceDashboard(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	resetLayout, err := h.svc.ResetDashboardLayout(r.Context(), actor, actor.UserID, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resetLayout)
}
