package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func (h *Handler) registerOperationsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/operations", h.requireAuth(false, h.handleGetOperationsCentre))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/housekeeping/tasks", h.requireAuth(true, h.handleCreateHousekeepingTask))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/housekeeping/tasks/{taskID}/stage", h.requireAuth(true, h.handleTransitionHousekeepingTask))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/housekeeping/tasks/{taskID}/assign", h.requireAuth(true, h.handleAssignHousekeepingTask))
}

func (h *Handler) handleGetOperationsCentre(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var opDate *time.Time
	if rawDate := strings.TrimSpace(r.URL.Query().Get("date")); rawDate != "" {
		parsed, err := domain.ParseDate(rawDate)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_date", "date must be YYYY-MM-DD")
			return
		}
		opDate = &parsed
	}
	data, err := h.svc.GetOperationsCentre(r.Context(), actor, propID, opDate, r.URL.Query().Get("focus"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (h *Handler) handleCreateHousekeepingTask(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		UnitID        uuid.UUID                   `json:"unit_id"`
		TaskType      domain.HousekeepingTaskType `json:"task_type"`
		Stage         domain.ChangeoverStage      `json:"stage"`
		Priority      domain.HousekeepingPriority `json:"priority"`
		ScheduledDate string                      `json:"scheduled_date"`
		Title         string                      `json:"title"`
		Notes         string                      `json:"notes"`
		AssignedTo    *uuid.UUID                  `json:"assigned_to"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	scheduledDate := time.Now().UTC()
	if strings.TrimSpace(body.ScheduledDate) != "" {
		d, err := domain.ParseDate(body.ScheduledDate)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_date", "scheduled_date must be YYYY-MM-DD")
			return
		}
		scheduledDate = d
	}
	created, err := h.svc.CreateHousekeepingTask(
		r.Context(), actor, propID, body.UnitID,
		body.TaskType, body.Stage, body.Priority,
		scheduledDate, body.Title, body.Notes, body.AssignedTo,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleTransitionHousekeepingTask(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	taskID, ok := parseUUIDParam(w, r, "taskID")
	if !ok {
		return
	}
	var body struct {
		Stage   domain.ChangeoverStage `json:"stage"`
		Notes   string                 `json:"notes"`
		Version int                    `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	updated, err := h.svc.TransitionHousekeepingTaskStage(r.Context(), actor, propID, taskID, body.Stage, body.Notes, body.Version)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleAssignHousekeepingTask(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	taskID, ok := parseUUIDParam(w, r, "taskID")
	if !ok {
		return
	}
	var body struct {
		AssignedTo *uuid.UUID `json:"assigned_to"`
		Version    int        `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	updated, err := h.svc.AssignHousekeepingTask(r.Context(), actor, propID, taskID, body.AssignedTo, body.Version)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
