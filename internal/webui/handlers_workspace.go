package webui

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func (h *Handler) registerWorkspaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /workspace", h.requirePageAuth(false, h.handleWorkspaceGet))
	mux.HandleFunc("POST /workspace/profile", h.requirePageAuth(true, h.handleWorkspaceProfileSelectPost))
	mux.HandleFunc("POST /workspace/profile/reset", h.requirePageAuth(true, h.handleWorkspaceProfileResetPost))
	mux.HandleFunc("POST /workspace/widgets/add", h.requirePageAuth(true, h.handleWorkspaceWidgetAddPost))
	mux.HandleFunc("POST /workspace/widgets/{instanceID}/update", h.requirePageAuth(true, h.handleWorkspaceWidgetUpdatePost))
	mux.HandleFunc("POST /workspace/widgets/{instanceID}/move", h.requirePageAuth(true, h.handleWorkspaceWidgetMovePost))
	mux.HandleFunc("POST /workspace/widgets/{instanceID}/remove", h.requirePageAuth(true, h.handleWorkspaceWidgetRemovePost))
	mux.HandleFunc("POST /workspace/reset", h.requirePageAuth(true, h.handleWorkspaceResetPost))
}

func (h *Handler) handleWorkspaceGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "My Workspace", "workspace")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	if base.Property.ID == uuid.Nil {
		h.renderError(w, http.StatusForbidden, "No Property Scope", "No active property is available for your account.")
		return
	}

	dash, err := h.svc.GetWorkspaceDashboard(r.Context(), actor, actor.UserID, base.Property.ID, base.Workspace)
	if err != nil {
		h.renderDomainError(w, err, "Workspace Unavailable", "Unable to resolve your personal workspace dashboard.")
		return
	}

	editMode := strings.TrimSpace(r.URL.Query().Get("edit")) == "1"
	h.renderPage(w, "workspace.html", struct {
		baseView
		Dashboard WorkspacePageData
		EditMode  bool
	}{
		baseView: base,
		Dashboard: WorkspacePageData{
			WorkspaceDashboardData: dash,
			WidgetCount:            len(dash.Widgets),
			MaxWidgets:             domain.MaxDashboardWidgets,
		},
		EditMode: editMode,
	})
}

// WorkspacePageData adds UI-specific metadata cleanly without polluting domain/service types.
type WorkspacePageData struct {
	service.WorkspaceDashboardData
	WidgetCount int
	MaxWidgets  int
}

func (h *Handler) resolveWorkspacePostScope(w http.ResponseWriter, r *http.Request, actor service.Actor) (uuid.UUID, bool) {
	if rawUID := strings.TrimSpace(r.FormValue("user_id")); rawUID != "" {
		uid, err := uuid.Parse(rawUID)
		if err != nil || uid != actor.UserID {
			h.renderError(w, http.StatusForbidden, "Access Denied", "Cannot modify another operator's workspace layout.")
			return uuid.Nil, false
		}
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "My Workspace", "workspace")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return uuid.Nil, false
	}
	propID := base.Property.ID
	if submitted := parseOptionalUUID(r.FormValue("property_id")); submitted != nil {
		propID = *submitted
	}
	return propID, true
}

func (h *Handler) redirectWorkspaceErr(w http.ResponseWriter, r *http.Request, retPath string, err error) {
	if errors.Is(err, domain.ErrForbidden) {
		h.renderError(w, http.StatusForbidden, "Access Denied", "You do not have permission to update that workspace or widget.")
		return
	}
	if errors.Is(err, domain.ErrPasswordRotationRequired) {
		http.Redirect(w, r, "/account?rotate=1", http.StatusSeeOther)
		return
	}
	redirectAdminWithQuery(w, r, retPath, "err", err.Error())
}

func (h *Handler) handleWorkspaceWidgetAddPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := h.resolveWorkspacePostScope(w, r, actor)
	if !ok {
		return
	}
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/workspace?edit=1")
	widgetID := strings.TrimSpace(r.FormValue("widget_id"))
	size := strings.TrimSpace(r.FormValue("size"))
	slot := strings.TrimSpace(r.FormValue("slot"))

	if _, err := h.svc.AddDashboardWidget(r.Context(), actor, actor.UserID, propID, widgetID, size, slot, nil); err != nil {
		h.redirectWorkspaceErr(w, r, retPath, err)
		return
	}
	redirectAdminWithQuery(w, r, retPath, "msg", "Widget added to your workspace.")
}

func (h *Handler) handleWorkspaceWidgetUpdatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := h.resolveWorkspacePostScope(w, r, actor)
	if !ok {
		return
	}
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/workspace?edit=1")
	instID := strings.TrimSpace(r.PathValue("instanceID"))
	widgetID := strings.TrimSpace(r.FormValue("widget_id"))
	size := strings.TrimSpace(r.FormValue("size"))
	slot := strings.TrimSpace(r.FormValue("slot"))

	cfg, err := parseWidgetConfigFromForm(r, widgetID)
	if err != nil {
		h.redirectWorkspaceErr(w, r, retPath, err)
		return
	}
	if _, err := h.svc.UpdateDashboardWidget(r.Context(), actor, actor.UserID, propID, instID, size, slot, cfg); err != nil {
		h.redirectWorkspaceErr(w, r, retPath, err)
		return
	}
	redirectAdminWithQuery(w, r, retPath, "msg", "Widget configuration saved.")
}

func (h *Handler) handleWorkspaceWidgetMovePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := h.resolveWorkspacePostScope(w, r, actor)
	if !ok {
		return
	}
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/workspace?edit=1")
	instID := strings.TrimSpace(r.PathValue("instanceID"))
	dir := strings.TrimSpace(r.FormValue("direction"))
	targetPos := 0
	if dir == "" {
		rawPos := strings.TrimSpace(r.FormValue("position"))
		parsed, err := strconv.Atoi(rawPos)
		if err != nil {
			h.redirectWorkspaceErr(w, r, retPath, domain.NewValidationError(map[string]string{"position": "must be a whole number"}))
			return
		}
		targetPos = parsed
	}
	if _, err := h.svc.MoveDashboardWidget(r.Context(), actor, actor.UserID, propID, instID, dir, targetPos); err != nil {
		h.redirectWorkspaceErr(w, r, retPath, err)
		return
	}
	redirectAdminWithQuery(w, r, retPath, "msg", "Widget order updated.")
}

func (h *Handler) handleWorkspaceWidgetRemovePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := h.resolveWorkspacePostScope(w, r, actor)
	if !ok {
		return
	}
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/workspace?edit=1")
	instID := strings.TrimSpace(r.PathValue("instanceID"))
	if _, err := h.svc.RemoveDashboardWidget(r.Context(), actor, actor.UserID, propID, instID); err != nil {
		h.redirectWorkspaceErr(w, r, retPath, err)
		return
	}
	redirectAdminWithQuery(w, r, retPath, "msg", "Widget removed from workspace.")
}

func (h *Handler) handleWorkspaceProfileSelectPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := h.resolveWorkspacePostScope(w, r, actor)
	if !ok {
		return
	}
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/workspace")
	state, err := h.svc.SelectWorkspaceProfile(r.Context(), actor, actor.UserID, propID, r.FormValue("profile_code"))
	if err != nil {
		h.redirectWorkspaceErr(w, r, retPath, err)
		return
	}
	msg := "Workspace profile updated."
	if state.ActiveProfile != nil {
		msg = "Workspace profile switched to " + state.ActiveProfile.Name + "."
	}
	redirectAdminWithQuery(w, r, retPath, "msg", msg)
}

func (h *Handler) handleWorkspaceProfileResetPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := h.resolveWorkspacePostScope(w, r, actor)
	if !ok {
		return
	}
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/workspace")
	clearNamed := r.FormValue("clear_named_overrides") == "1" || r.FormValue("clear_named_overrides") == "true"
	res, err := h.svc.ResetWorkspaceProfile(r.Context(), actor, actor.UserID, propID, clearNamed)
	if err != nil {
		h.redirectWorkspaceErr(w, r, retPath, err)
		return
	}
	msg := "Workspace reset to role default profile (global theme and saved views preserved)."
	if res.Profile.ActiveProfile != nil {
		msg = "Workspace reset to " + res.Profile.ActiveProfile.Name + " role default profile (global theme and saved views preserved)."
	}
	redirectAdminWithQuery(w, r, retPath, "msg", msg)
}

func (h *Handler) handleWorkspaceResetPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := h.resolveWorkspacePostScope(w, r, actor)
	if !ok {
		return
	}
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/workspace")
	resolved, err := h.svc.ResetDashboardLayout(r.Context(), actor, actor.UserID, propID)
	if err != nil {
		h.redirectWorkspaceErr(w, r, retPath, err)
		return
	}
	msg := "Workspace layout reset to platform default."
	if resolved.ProfileName != "" {
		msg = "Workspace layout reset to " + resolved.ProfileName + " profile default."
	}
	redirectAdminWithQuery(w, r, retPath, "msg", msg)
}

func parseStrictFormInt(r *http.Request, key string) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(r.FormValue(key)))
	if err != nil {
		return 0, domain.NewValidationError(map[string]string{key: "must be a valid whole number"})
	}
	return v, nil
}

func parseWidgetConfigFromForm(r *http.Request, widgetID string) (map[string]any, error) {
	switch widgetID {
	case domain.WidgetOpsArrivalsSummary:
		limit, err := parseStrictFormInt(r, "limit")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"show_pending_holds":  r.FormValue("show_pending_holds") == "1",
			"show_in_house_count": r.FormValue("show_in_house_count") == "1",
			"limit":               limit,
		}, nil

	case domain.WidgetOpsChangeoverBoard:
		limit, err := parseStrictFormInt(r, "limit")
		if err != nil {
			return nil, err
		}
		stages := r.Form["stage_filter"]
		if len(stages) == 0 {
			stages = []string{}
		}
		return map[string]any{
			"stage_filter": stages,
			"limit":        limit,
		}, nil

	case domain.WidgetOpsAlertsAttention:
		cats := r.Form["include_categories"]
		if len(cats) == 0 {
			cats = []string{}
		}
		return map[string]any{
			"include_categories": cats,
		}, nil

	case domain.WidgetMaintOpenQueue:
		limit, err := parseStrictFormInt(r, "limit")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"priority_min":   strings.TrimSpace(r.FormValue("priority_min")),
			"assigned_to_me": r.FormValue("assigned_to_me") == "1",
			"limit":          limit,
		}, nil

	case domain.WidgetMaintPreventativeDue:
		horizonDays, err := parseStrictFormInt(r, "horizon_days")
		if err != nil {
			return nil, err
		}
		limit, err := parseStrictFormInt(r, "limit")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"horizon_days": horizonDays,
			"limit":        limit,
		}, nil

	case domain.WidgetResPendingInquiries:
		limit, err := parseStrictFormInt(r, "limit")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"include_inquiries": r.FormValue("include_inquiries") == "1",
			"limit":             limit,
		}, nil

	case domain.WidgetResMiniCalendar:
		days, err := parseStrictFormInt(r, "days")
		if err != nil {
			return nil, err
		}
		utID := strings.TrimSpace(r.FormValue("unit_type_id"))
		var utVal any
		if utID != "" {
			utVal = utID
		}
		return map[string]any{
			"days":         days,
			"unit_type_id": utVal,
		}, nil

	case domain.WidgetCRMOpenEnquiries:
		limit, err := parseStrictFormInt(r, "limit")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"priority_filter": strings.TrimSpace(r.FormValue("priority_filter")),
			"only_complaints": r.FormValue("only_complaints") == "1",
			"limit":           limit,
		}, nil

	case domain.WidgetChannelsSyncHealth:
		return map[string]any{
			"show_ok_providers": r.FormValue("show_ok_providers") == "1",
		}, nil

	case domain.WidgetReportsKPISnapshot:
		windowDays, err := parseStrictFormInt(r, "window_days")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"window_days":       windowDays,
			"show_prior_period": r.FormValue("show_prior_period") == "1",
		}, nil

	case domain.WidgetWorkspaceQuickShortcuts:
		maxItems, err := parseStrictFormInt(r, "max_items")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"max_items":          maxItems,
			"show_saved_filters": r.FormValue("show_saved_filters") == "1",
		}, nil

	default:
		return nil, domain.NewValidationError(map[string]string{
			"widget_id": "unknown or unsupported dashboard widget identifier",
		})
	}
}
