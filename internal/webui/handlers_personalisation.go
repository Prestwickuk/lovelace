package webui

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/auth"
	"github.com/Prestwickuk/lovelace/internal/branding"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func permittedAdminRoutesForRole(role domain.Role) []domain.AdminRouteOption {
	all := domain.AllowedAdminRoutes()
	out := make([]domain.AdminRouteOption, 0, len(all))
	for _, opt := range all {
		if opt.PermKey == "" || auth.HasPermission(role, auth.Permission(opt.PermKey)) {
			out = append(out, opt)
		}
	}
	return out
}

func (h *Handler) renderAccountPage(ctx context.Context, w http.ResponseWriter, actor service.Actor, base baseView) {
	var propPayload domain.PropertyWorkspacePayload
	if base.Workspace.PropertyWorkspace != nil {
		propPayload = base.Workspace.PropertyWorkspace.Payload
	}
	var routes []domain.AdminRouteOption
	if !actor.MustRotatePassword {
		routes = permittedAdminRoutesForRole(actor.Role)
		if h.svc != nil {
			if sum, err := h.svc.GetRecoveryCodeSummary(ctx, actor); err == nil {
				base.RecoverySummary = &sum
			}
		}
	}
	base.AccountPrefs = &accountPersonalisationData{
		Enabled:           !actor.MustRotatePassword,
		Accents:           branding.CuratedAccents(),
		NavSections:       domain.NavSections(),
		PermittedRoutes:   routes,
		PropertyWorkspace: propPayload,
	}
	h.renderPage(w, "account.html", base)
}

func (h *Handler) resolveTargetUserIDForPost(w http.ResponseWriter, r *http.Request, actor service.Actor) (uuid.UUID, bool) {
	if rawTarget := strings.TrimSpace(r.FormValue("user_id")); rawTarget != "" {
		parsed, err := uuid.Parse(rawTarget)
		if err != nil || parsed != actor.UserID {
			h.renderDomainError(w, domain.ErrForbidden, "Access Denied", "You may only modify your own personalisation settings.")
			return uuid.Nil, false
		}
	}
	return actor.UserID, true
}

func (h *Handler) handleAccountGlobalPreferencesPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	targetUserID, ok := h.resolveTargetUserIDForPost(w, r, actor)
	if !ok {
		return
	}
	rawDensity := strings.TrimSpace(r.FormValue("density"))
	explicitDensity := rawDensity != "" && !strings.EqualFold(rawDensity, "inherit")
	rawRoute := strings.TrimSpace(r.FormValue("default_route"))
	explicitRoute := rawRoute != ""

	in := domain.UserPreferences{
		UserID:               targetUserID,
		ThemeMode:            r.FormValue("theme_mode"),
		Accent:               r.FormValue("accent"),
		Density:              rawDensity,
		TextScale:            r.FormValue("text_scale"),
		HighContrast:         r.FormValue("high_contrast") == "1" || r.FormValue("high_contrast") == "true",
		ReducedMotion:        r.FormValue("reduced_motion") == "1" || r.FormValue("reduced_motion") == "true",
		DefaultRoute:         rawRoute,
		ExplicitDensity:      explicitDensity,
		ExplicitDefaultRoute: explicitRoute,
		InheritDensity:       !explicitDensity,
		InheritDefaultRoute:  !explicitRoute,
		Payload: domain.UserPreferencePayload{
			Shortcuts:         r.Form["shortcuts"],
			CollapsedSections: r.Form["collapsed_sections"],
			CalendarHorizon:   r.FormValue("calendar_horizon"),
			CalendarGrouping:  r.FormValue("calendar_grouping"),
			TablePageSize:     parseIntDefault(r.FormValue("table_page_size"), domain.DefaultTablePageSize),
		},
	}
	if _, err := h.svc.UpdateUserGlobalPreferences(r.Context(), actor, targetUserID, in); err != nil {
		h.redirectPersonalisationErr(w, r, "/account", err)
		return
	}
	redirectAdminWithQuery(w, r, "/account", "msg", "Global personalisation preferences saved.")
}

func (h *Handler) handleAccountGlobalPreferencesResetPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	targetUserID, ok := h.resolveTargetUserIDForPost(w, r, actor)
	if !ok {
		return
	}
	if err := h.svc.ResetUserGlobalPreferences(r.Context(), actor, targetUserID); err != nil {
		h.redirectPersonalisationErr(w, r, "/account", err)
		return
	}
	redirectAdminWithQuery(w, r, "/account", "msg", "Global personalisation preferences reset to platform defaults.")
}

func (h *Handler) handleAccountWorkspacePreferencesPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	targetUserID, ok := h.resolveTargetUserIDForPost(w, r, actor)
	if !ok {
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "My Account", "account")
	if err != nil {
		h.renderDomainError(w, err, "Server Error", "failed to load property")
		return
	}
	propID := base.Property.ID
	if submitted := parseOptionalUUID(r.FormValue("property_id")); submitted != nil {
		propID = *submitted
	}
	in := domain.PropertyWorkspacePayload{
		CalendarHorizon:  r.FormValue("calendar_horizon"),
		CalendarGrouping: r.FormValue("calendar_grouping"),
		TablePageSize:    parseIntDefault(r.FormValue("table_page_size"), 0),
		ReservationsSort: r.FormValue("reservations_sort"),
		GuestsSort:       r.FormValue("guests_sort"),
		DefaultRoute:     r.FormValue("default_route"),
		Shortcuts:        r.Form["shortcuts"],
	}
	if _, err := h.svc.UpdateUserPropertyWorkspace(r.Context(), actor, targetUserID, propID, in); err != nil {
		h.redirectPersonalisationErr(w, r, "/account", err)
		return
	}
	redirectAdminWithQuery(w, r, "/account", "msg", "Property workspace preferences saved.")
}

func (h *Handler) handleAccountWorkspacePreferencesResetPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	targetUserID, ok := h.resolveTargetUserIDForPost(w, r, actor)
	if !ok {
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "My Account", "account")
	if err != nil {
		h.renderDomainError(w, err, "Server Error", "failed to load property")
		return
	}
	propID := base.Property.ID
	if submitted := parseOptionalUUID(r.FormValue("property_id")); submitted != nil {
		propID = *submitted
	}
	if err := h.svc.ResetUserPropertyWorkspace(r.Context(), actor, targetUserID, propID); err != nil {
		h.redirectPersonalisationErr(w, r, "/account", err)
		return
	}
	redirectAdminWithQuery(w, r, "/account", "msg", "Property workspace preferences and saved views reset.")
}

func (h *Handler) handleAccountSavedViewCreatePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	targetUserID, ok := h.resolveTargetUserIDForPost(w, r, actor)
	if !ok {
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "My Account", "account")
	if err != nil {
		h.renderDomainError(w, err, "Server Error", "failed to load property")
		return
	}
	propID := base.Property.ID
	if submitted := parseOptionalUUID(r.FormValue("property_id")); submitted != nil {
		propID = *submitted
	}
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/account")
	screen := strings.ToLower(strings.TrimSpace(r.FormValue("screen")))
	filters := make(map[string]string)
	for _, key := range []string{"horizon", "grouping", "unit_type_id", "channel_code", "status", "sort", "page_size", "segment", "include_all"} {
		if val := strings.TrimSpace(r.FormValue(key)); val != "" {
			filters[key] = val
		}
	}
	if _, err := h.svc.SaveOperationalView(r.Context(), actor, targetUserID, propID, domain.SavedOperationalView{
		Name:    r.FormValue("name"),
		Screen:  screen,
		Filters: filters,
	}); err != nil {
		h.redirectPersonalisationErr(w, r, retPath, err)
		return
	}
	redirectAdminWithQuery(w, r, retPath, "msg", "Operational view saved.")
}

func (h *Handler) handleAccountSavedViewDeletePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	targetUserID, ok := h.resolveTargetUserIDForPost(w, r, actor)
	if !ok {
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "My Account", "account")
	if err != nil {
		h.renderDomainError(w, err, "Server Error", "failed to load property")
		return
	}
	propID := base.Property.ID
	if submitted := parseOptionalUUID(r.FormValue("property_id")); submitted != nil {
		propID = *submitted
	}
	retPath := safeAdminReturnPath(r.FormValue("return_to"), "/account")
	viewID := r.PathValue("id")
	if err := h.svc.DeleteOperationalView(r.Context(), actor, targetUserID, propID, viewID); err != nil {
		h.redirectPersonalisationErr(w, r, retPath, err)
		return
	}
	redirectAdminWithQuery(w, r, retPath, "msg", "Saved view removed.")
}

func (h *Handler) redirectPersonalisationErr(w http.ResponseWriter, r *http.Request, retPath string, err error) {
	if errors.Is(err, domain.ErrForbidden) {
		h.renderError(w, http.StatusForbidden, "Access Denied", "You do not have permission to access or update that personalisation scope.")
		return
	}
	if errors.Is(err, domain.ErrPasswordRotationRequired) {
		http.Redirect(w, r, "/account?rotate=1", http.StatusSeeOther)
		return
	}
	redirectAdminWithQuery(w, r, retPath, "err", err.Error())
}

func findSavedView(ws domain.ResolvedWorkspace, screen, id string) (domain.SavedOperationalView, bool) {
	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return domain.SavedOperationalView{}, false
	}
	for _, v := range ws.SavedViews {
		if v.Screen == screen && v.ID == cleanID {
			return v, true
		}
	}
	return domain.SavedOperationalView{}, false
}

func resolvePageParams(rawSize, rawPage string, workspaceSize int) (page int, pageSize int) {
	pageSize = workspaceSize
	if !domain.IsAllowedPageSize(pageSize, false) {
		pageSize = domain.DefaultTablePageSize
	}
	if parsed := parseIntDefault(rawSize, 0); domain.IsAllowedPageSize(parsed, false) {
		pageSize = parsed
	}
	page = parseIntDefault(rawPage, 1)
	if page < 1 {
		page = 1
	}
	return page, pageSize
}

func paginateRange(total, page, pageSize int) (start, end, totalPages int) {
	if total == 0 {
		return 0, 0, 1
	}
	totalPages = (total + pageSize - 1) / pageSize
	if page > totalPages {
		page = totalPages
	}
	start = (page - 1) * pageSize
	if start < 0 {
		start = 0
	}
	end = start + pageSize
	if end > total {
		end = total
	}
	return start, end, totalPages
}

func sortReservationsForWorkspace(items []domain.Reservation, sortKey string) {
	switch sortKey {
	case domain.SortReservationsStayDesc:
		sort.SliceStable(items, func(i, j int) bool {
			if !items[i].ArrivalDate.Equal(items[j].ArrivalDate) {
				return items[i].ArrivalDate.After(items[j].ArrivalDate)
			}
			return items[i].CreatedAt.After(items[j].CreatedAt)
		})
	case domain.SortReservationsCreated:
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		})
	default:
		// Repository returns lower(stay_range) ASC, created_at DESC.
	}
}

func sortGuestsForWorkspace(items []domain.GuestListItem, sortKey string) {
	switch sortKey {
	case domain.SortGuestsStaysDesc:
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].CompletedStays != items[j].CompletedStays {
				return items[i].CompletedStays > items[j].CompletedStays
			}
			return strings.ToLower(items[i].FullName) < strings.ToLower(items[j].FullName)
		})
	case domain.SortGuestsRecentStay:
		sort.SliceStable(items, func(i, j int) bool {
			ti, tj := items[i].LastStayDate, items[j].LastStayDate
			if ti != nil && tj != nil && !ti.Equal(*tj) {
				return ti.After(*tj)
			}
			if (ti != nil) != (tj != nil) {
				return ti != nil
			}
			return strings.ToLower(items[i].FullName) < strings.ToLower(items[j].FullName)
		})
	default:
		// Default alphabetical order / store order.
	}
}
