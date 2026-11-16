package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/httpx"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

// Handler serves /healthz, /readyz, and /api/v1/*.
type Handler struct {
	svc                 *service.Service
	store               *postgres.Store
	cfg                 config.Config
	publicGlobalLimiter *httpx.PublicRateLimiter
	publicReadLimiter   *httpx.PublicRateLimiter
	publicWriteLimiter  *httpx.PublicRateLimiter
	publicLookupLimiter *httpx.PublicRateLimiter
}

func New(svc *service.Service, store *postgres.Store, cfg config.Config) *Handler {
	return &Handler{
		svc:                 svc,
		store:               store,
		cfg:                 cfg,
		publicGlobalLimiter: httpx.NewPublicRateLimiter(600, time.Minute),
		publicReadLimiter:   httpx.NewPublicRateLimiter(180, time.Minute),
		publicWriteLimiter:  httpx.NewPublicRateLimiter(30, time.Minute),
		publicLookupLimiter: httpx.NewPublicRateLimiter(20, time.Minute),
	}
}

// Register mounts all health and API routes onto mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.handleHealthz)
	mux.HandleFunc("GET /readyz", h.handleReadyz)

	mux.HandleFunc("POST /api/v1/auth/login", h.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", h.requireAuth(true, h.handleLogout))
	mux.HandleFunc("GET /api/v1/auth/me", h.requireAuth(false, h.handleMe))
	mux.HandleFunc("POST /api/v1/auth/change-password", h.requireAuth(true, h.handleChangePassword))

	mux.HandleFunc("GET /api/v1/properties", h.requireAuth(false, h.handleListProperties))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}", h.requireAuth(false, h.handleGetProperty))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}", h.requireAuth(true, h.handleUpdateProperty))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/unit-types", h.requireAuth(false, h.handleListUnitTypes))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/unit-types", h.requireAuth(true, h.handleCreateUnitType))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/unit-types/{id}", h.requireAuth(true, h.handleUpdateUnitType))
	mux.HandleFunc("DELETE /api/v1/properties/{propertyID}/unit-types/{id}", h.requireAuth(true, h.handleArchiveUnitType))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/units", h.requireAuth(false, h.handleListUnits))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/units", h.requireAuth(true, h.handleCreateUnit))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/units/{id}", h.requireAuth(true, h.handleUpdateUnit))
	mux.HandleFunc("DELETE /api/v1/properties/{propertyID}/units/{id}", h.requireAuth(true, h.handleArchiveUnit))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/extras", h.requireAuth(false, h.handleListExtras))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/extras", h.requireAuth(true, h.handleCreateExtra))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/extras/{id}", h.requireAuth(true, h.handleUpdateExtra))
	mux.HandleFunc("DELETE /api/v1/properties/{propertyID}/extras/{id}", h.requireAuth(true, h.handleArchiveExtra))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/rate-plans", h.requireAuth(false, h.handleListRatePlans))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/rate-plans", h.requireAuth(true, h.handleCreateRatePlan))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/rate-plans/{id}", h.requireAuth(true, h.handleUpdateRatePlan))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/rate-rules", h.requireAuth(false, h.handleListRateRules))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/rate-rules", h.requireAuth(true, h.handleCreateRateRule))
	mux.HandleFunc("PATCH /api/v1/properties/{propertyID}/rate-rules/{id}", h.requireAuth(true, h.handleUpdateRateRule))
	mux.HandleFunc("DELETE /api/v1/properties/{propertyID}/rate-rules/{id}", h.requireAuth(true, h.handleArchiveRateRule))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/rate-preview", h.requireAuth(false, h.handleRatePreview))

	mux.HandleFunc("GET /api/v1/properties/{propertyID}/availability-blocks", h.requireAuth(false, h.handleListBlocks))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/availability-blocks", h.requireAuth(true, h.handleCreateBlock))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/availability-blocks/{id}/release", h.requireAuth(true, h.handleReleaseBlock))

	mux.HandleFunc("GET /api/v1/users", h.requireAuth(false, h.handleListUsers))
	mux.HandleFunc("POST /api/v1/users", h.requireAuth(true, h.handleCreateUser))
	mux.HandleFunc("PATCH /api/v1/users/{id}", h.requireAuth(true, h.handleUpdateUser))

	mux.HandleFunc("GET /api/v1/audit-logs", h.requireAuth(false, h.handleListAuditLogs))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/audit-logs/{auditLogID}/redact", h.requireAuth(true, h.handleRedactAuditLog))

	h.registerReservationRoutes(mux)
	h.registerBlockoutReleaseRoutes(mux)
	h.registerCalendarChannelRoutes(mux)
	h.registerOperationsRoutes(mux)
	h.registerMaintenanceRoutes(mux)
	h.registerGuestCRMRoutes(mux)
	h.registerReportRoutes(mux)
	h.registerWorkspaceRoutes(mux)
	h.registerPropertyBrandingRoutes(mux)
	h.registerPublicRoutes(mux)
}

type authedHandler func(w http.ResponseWriter, r *http.Request, actor service.Actor)

func (h *Handler) requireAuth(requireCSRF bool, next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := httpx.ActorFromContext(r.Context())
		if !ok {
			writeDomainError(w, domain.ErrUnauthorized)
			return
		}
		if requireCSRF {
			token := r.Header.Get("X-CSRF-Token")
			if !httpx.VerifyCSRFToken(actor, token) {
				writeError(w, http.StatusForbidden, "csrf_invalid", "missing or invalid X-CSRF-Token")
				return
			}
		}
		if actor.MustRotatePassword {
			switch r.URL.Path {
			case "/api/v1/auth/me", "/api/v1/auth/logout", "/api/v1/auth/change-password":
			default:
				writeError(w, http.StatusForbidden, "password_rotation_required", "you must change your temporary password before accessing other resources")
				return
			}
		}
		next(w, r, actor)
	}
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleReadyz(w http.ResponseWriter, r *http.Request) {
	report := h.svc.Readiness(r.Context())
	if !report.Ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":      "not_ready",
			"ready":       false,
			"database":    report.Database,
			"payments":    report.Payments,
			"email":       report.Email,
			"public_api":  report.Public,
			"attachments": report.Attachments,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ready",
		"ready":       true,
		"database":    report.Database,
		"payments":    report.Payments,
		"email":       report.Email,
		"public_api":  report.Public,
		"attachments": report.Attachments,
	})
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	meta := httpx.MetaFromContext(r.Context())
	res, err := h.svc.Login(r.Context(), body.Email, body.Password, meta.IPAddress, meta.UserAgent, meta.RequestID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.SetSessionCookie(w, h.cfg, res.SessionToken)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":       res.User,
		"csrf_token": res.CSRFToken,
	})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	if err := h.svc.Logout(r.Context(), actor); err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.ClearSessionCookie(w, h.cfg)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":                   actor.UserID,
			"property_id":          actor.PropertyID,
			"email":                actor.Email,
			"full_name":            actor.FullName,
			"role":                 actor.Role,
			"must_rotate_password": actor.MustRotatePassword,
		},
		"csrf_token": actor.CSRFToken,
	})
}

func (h *Handler) handleChangePassword(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	res, err := h.svc.ChangeOwnPassword(r.Context(), actor, body.CurrentPassword, body.NewPassword)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.SetSessionCookie(w, h.cfg, res.SessionToken)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":       res.User,
		"csrf_token": res.CSRFToken,
	})
}

func (h *Handler) handleListUsers(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	users, err := h.svc.ListUsers(r.Context(), actor)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(users)})
}

func (h *Handler) handleCreateUser(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	var body struct {
		PropertyID         *uuid.UUID  `json:"property_id"`
		Email              string      `json:"email"`
		FullName           string      `json:"full_name"`
		Role               domain.Role `json:"role"`
		Password           string      `json:"password"`
		MustRotatePassword bool        `json:"must_rotate_password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	u, err := h.svc.CreateUser(r.Context(), actor, service.CreateUserInput{
		PropertyID:         body.PropertyID,
		Email:              body.Email,
		FullName:           body.FullName,
		Role:               body.Role,
		Password:           body.Password,
		MustRotatePassword: body.MustRotatePassword,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (h *Handler) handleUpdateUser(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		FullName           string      `json:"full_name"`
		Role               domain.Role `json:"role"`
		IsActive           bool        `json:"is_active"`
		MustRotatePassword bool        `json:"must_rotate_password"`
		Version            int         `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	u, err := h.svc.UpdateUser(r.Context(), actor, service.UpdateUserInput{
		ID:                 id,
		FullName:           body.FullName,
		Role:               body.Role,
		IsActive:           body.IsActive,
		MustRotatePassword: body.MustRotatePassword,
		Version:            body.Version,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *Handler) handleListAuditLogs(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	var propID *uuid.UUID
	if raw := r.URL.Query().Get("property_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_param", "invalid property_id")
			return
		}
		propID = &id
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	logs, err := h.svc.ListAuditLogs(r.Context(), actor, propID, r.URL.Query().Get("resource_type"), limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(logs)})
}

func (h *Handler) handleRedactAuditLog(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	auditLogID, err := strconv.ParseInt(r.PathValue("auditLogID"), 10, 64)
	if err != nil || auditLogID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_id", "invalid auditLogID")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	updated, err := h.svc.RedactHistoricalAuditLog(r.Context(), actor, propID, auditLogID, body.Reason)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// --- JSON Helpers ---

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "malformed or invalid JSON request body")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain a single JSON object")
		return false
	}
	return true
}

func parseUUIDParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	raw := r.PathValue(name)
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "invalid UUID parameter: "+name)
		return uuid.Nil, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func writeDomainError(w http.ResponseWriter, err error) {
	var valErr *domain.ValidationError
	if errors.As(err, &valErr) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{
				"code":    "validation_failed",
				"message": valErr.Error(),
				"fields":  valErr.Fields,
			},
		})
		return
	}
	switch {
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required or invalid credentials")
	case errors.Is(err, domain.ErrPasswordRotationRequired):
		writeError(w, http.StatusForbidden, "password_rotation_required", "you must change your temporary password before accessing other resources")
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "insufficient permissions for this action")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "requested resource was not found")
	case errors.Is(err, domain.ErrOverlap):
		writeError(w, http.StatusConflict, "date_overlap", err.Error())
	case errors.Is(err, domain.ErrReleaseRestricted):
		writeError(w, http.StatusConflict, "release_restricted", err.Error())
	case errors.Is(err, domain.ErrStripeDisabled):
		writeError(w, http.StatusConflict, "stripe_disabled", "online card payments are currently disabled for this property")
	case errors.Is(err, domain.ErrActiveCheckoutExists):
		writeError(w, http.StatusConflict, "active_checkout_exists", "an active payment attempt already exists for this booking hold")
	case errors.Is(err, domain.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "resource conflict or stale version")
	case errors.Is(err, domain.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts; please try again later")
	case errors.Is(err, domain.ErrChannelSyncBlocked), errors.Is(err, channels.ErrOperationBlocked):
		writeError(w, http.StatusUnprocessableEntity, "capability_blocked", err.Error())
	case errors.Is(err, domain.ErrChannelConfirmationRequired):
		writeError(w, http.StatusUnprocessableEntity, "confirmation_required", "broad live sync requires explicit confirm_scope=true confirmation")
	case errors.Is(err, domain.ErrChannelWebhookUnauthorized), errors.Is(err, channels.ErrWebhookVerificationFailed):
		writeError(w, http.StatusUnauthorized, "webhook_unauthorized", "channel webhook verification failed")
	case errors.Is(err, channels.ErrAuthFailed), errors.Is(err, channels.ErrTokenExpired):
		writeError(w, http.StatusUnprocessableEntity, "provider_auth_failed", "channel provider authentication failed")
	case errors.Is(err, channels.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "provider_rate_limited", "channel provider rate limit exceeded")
	case errors.Is(err, channels.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error())
	case errors.Is(err, channels.ErrProviderUnavailable), errors.Is(err, channels.ErrInvalidResponse), errors.Is(err, channels.ErrRedirectBlocked), errors.Is(err, channels.ErrResponseTooLarge):
		writeError(w, http.StatusBadGateway, "provider_error", "channel provider communication error")
	case errors.Is(err, domain.ErrValidation):
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
