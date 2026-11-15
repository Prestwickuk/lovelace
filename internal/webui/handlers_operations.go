package webui

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func (h *Handler) registerOperationsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /operations", h.requirePageAuth(false, h.handleOperationsCentre))
	mux.HandleFunc("POST /operations/housekeeping/tasks", h.requirePageAuth(true, h.handleOpsCreateTaskPost))
	mux.HandleFunc("POST /operations/housekeeping/tasks/{id}/stage", h.requirePageAuth(true, h.handleOpsTransitionTaskPost))
	mux.HandleFunc("POST /operations/housekeeping/tasks/{id}/assign", h.requirePageAuth(true, h.handleOpsAssignTaskPost))
	mux.HandleFunc("GET /payment-exceptions", h.requirePageAuth(false, h.handlePaymentExceptionsGet))
	mux.HandleFunc("POST /payment-exceptions/{id}/resolve", h.requirePageAuth(true, h.handlePaymentExceptionResolvePost))
}

func (h *Handler) handleOperationsCentre(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Operations Centre", "dashboard")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}

	q := r.URL.Query()
	var opDatePtr *time.Time
	if rawDate := strings.TrimSpace(q.Get("date")); rawDate != "" {
		parsed, err := domain.ParseDate(rawDate)
		if err != nil {
			base.Error = "Invalid operational date requested; showing today."
		} else {
			opDatePtr = &parsed
		}
	}

	focus := strings.TrimSpace(q.Get("focus"))
	ops, err := h.svc.GetOperationsCentre(r.Context(), actor, base.Property.ID, opDatePtr, focus)
	if err != nil {
		h.renderDomainError(w, err, "Operations Centre Unavailable", "Failed to load operational overview.")
		return
	}

	dateStr := domain.FormatDate(ops.OperationalDate)
	prevDateStr := domain.FormatDate(ops.OperationalDate.AddDate(0, 0, -1))
	nextDateStr := domain.FormatDate(ops.OperationalDate.AddDate(0, 0, 1))
	todayStr := domain.FormatDate(ops.TodayDate)
	returnURL := buildOpsReturnURL(dateStr, string(ops.Focus), "")

	h.renderPage(w, "dashboard.html", struct {
		baseView
		Ops          domain.OperationsCentreData
		DateStr      string
		PrevDateStr  string
		NextDateStr  string
		TodayDateStr string
		ReturnURL    string
	}{
		baseView:     base,
		Ops:          ops,
		DateStr:      dateStr,
		PrevDateStr:  prevDateStr,
		NextDateStr:  nextDateStr,
		TodayDateStr: todayStr,
		ReturnURL:    returnURL,
	})
}

func (h *Handler) handleOpsCreateTaskPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Operations Centre", "dashboard")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}

	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/")
	unitID, err := uuid.Parse(strings.TrimSpace(r.FormValue("unit_id")))
	if err != nil {
		redirectAdminWithQuery(w, r, retPath, "err", "Select a valid accommodation unit.")
		return
	}
	schedDate := time.Now().UTC()
	if rawDate := strings.TrimSpace(r.FormValue("scheduled_date")); rawDate != "" {
		if d, perr := domain.ParseDate(rawDate); perr == nil {
			schedDate = d
		}
	}
	assignedTo := parseOptionalUUID(r.FormValue("assigned_to"))

	task, err := h.svc.CreateHousekeepingTask(
		r.Context(), actor, base.Property.ID, unitID,
		domain.HousekeepingTaskType(r.FormValue("task_type")),
		domain.ChangeoverStage(r.FormValue("stage")),
		domain.HousekeepingPriority(r.FormValue("priority")),
		schedDate,
		r.FormValue("title"),
		r.FormValue("notes"),
		assignedTo,
	)
	if err != nil {
		if errors.Is(err, domain.ErrForbidden) {
			h.renderDomainError(w, err, "Access Denied", "Your administrative role cannot create housekeeping tasks.")
			return
		}
		redirectAdminWithQuery(w, r, retPath, "err", err.Error())
		return
	}
	redirectAdminWithQuery(w, r, retPath+"#changeover-board", "msg", "Queued "+task.UnitCode+" ("+task.Stage.Label()+").")
}

func (h *Handler) handleOpsTransitionTaskPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	taskID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid task id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Operations Centre", "dashboard")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}

	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/")
	updated, err := h.svc.TransitionHousekeepingTaskStage(
		r.Context(),
		actor,
		base.Property.ID,
		taskID,
		domain.ChangeoverStage(r.FormValue("stage")),
		r.FormValue("notes"),
		parseIntDefault(r.FormValue("version"), 1),
	)
	if err != nil {
		if errors.Is(err, domain.ErrForbidden) {
			h.renderDomainError(w, err, "Access Denied", "Your administrative role cannot update housekeeping stages.")
			return
		}
		redirectAdminWithQuery(w, r, retPath, "err", err.Error())
		return
	}
	redirectAdminWithQuery(w, r, retPath+"#changeover-board", "msg", updated.UnitCode+" moved to "+updated.Stage.Label()+".")
}

func (h *Handler) handleOpsAssignTaskPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	taskID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid task id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Operations Centre", "dashboard")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}

	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/")
	assignee := parseOptionalUUID(r.FormValue("assigned_to"))
	updated, err := h.svc.AssignHousekeepingTask(
		r.Context(), actor, base.Property.ID, taskID, assignee, parseIntDefault(r.FormValue("version"), 1),
	)
	if err != nil {
		if errors.Is(err, domain.ErrForbidden) {
			h.renderDomainError(w, err, "Access Denied", "Your administrative role cannot assign housekeeping tasks.")
			return
		}
		redirectAdminWithQuery(w, r, retPath, "err", err.Error())
		return
	}
	msg := "Unassigned housekeeping task for " + updated.UnitCode + "."
	if updated.AssigneeName != "" {
		msg = "Assigned " + updated.UnitCode + " to " + updated.AssigneeName + "."
	}
	redirectAdminWithQuery(w, r, retPath+"#changeover-board", "msg", msg)
}

func (h *Handler) handlePaymentExceptionsGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Payment Exceptions", "payment-exceptions")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	exceptions, err := h.svc.ListPaymentExceptions(r.Context(), actor, base.Property.ID)
	if err != nil {
		h.renderDomainError(w, err, "Payment Exceptions Unavailable", "Your administrative role cannot view payment exceptions.")
		return
	}
	var openCount, resolvedCount int
	for _, ex := range exceptions {
		if ex.Status == "OPEN" {
			openCount++
		} else {
			resolvedCount++
		}
	}
	h.renderPage(w, "payment_exceptions.html", struct {
		baseView
		PaymentExceptions []domain.PaymentException
		OpenCount         int
		ResolvedCount     int
	}{
		baseView:          base,
		PaymentExceptions: exceptions,
		OpenCount:         openCount,
		ResolvedCount:     resolvedCount,
	})
}

func (h *Handler) handlePaymentExceptionResolvePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	exID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid payment exception id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Payment Exceptions", "payment-exceptions")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/payment-exceptions")
	if _, err := h.svc.ResolvePaymentException(r.Context(), actor, base.Property.ID, exID, r.FormValue("resolution_notes")); err != nil {
		if errors.Is(err, domain.ErrForbidden) {
			h.renderDomainError(w, err, "Access Denied", "Your administrative role cannot resolve payment exceptions.")
			return
		}
		redirectAdminWithQuery(w, r, retPath, "err", err.Error())
		return
	}
	redirectAdminWithQuery(w, r, retPath, "msg", "Payment exception marked as manually resolved.")
}

func buildOpsReturnURL(dateStr, focus, anchor string) string {
	v := url.Values{}
	if dateStr != "" {
		v.Set("date", dateStr)
	}
	if focus != "" && focus != string(domain.OpsFocusAll) {
		v.Set("focus", focus)
	}
	u := "/"
	if enc := v.Encode(); enc != "" {
		u += "?" + enc
	}
	if anchor != "" {
		u += "#" + anchor
	}
	return u
}

func safeAdminReturnPath(raw, fallback string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "//") {
		return fallback
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.IsAbs() || u.Host != "" {
		return fallback
	}
	return trimmed
}

func redirectAdminWithQuery(w http.ResponseWriter, r *http.Request, targetPath, key, val string) {
	basePart, frag, hasFrag := strings.Cut(targetPath, "#")
	u, err := url.Parse(basePart)
	if err != nil {
		u = &url.URL{Path: "/"}
	}
	q := u.Query()
	q.Set(key, val)
	u.RawQuery = q.Encode()
	dest := u.String()
	if hasFrag && frag != "" {
		dest += "#" + frag
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
