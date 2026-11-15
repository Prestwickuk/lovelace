package webui

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/httpx"
	"github.com/tirbach/tir-bach/internal/service"
)

// --- Rates & Preview ---

func (h *Handler) handleRatesGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Rates & Preview", "rates")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	plans, _ := h.svc.ListRatePlans(r.Context(), actor, base.Property.ID)
	uts, _ := h.svc.ListUnitTypes(r.Context(), actor, base.Property.ID)
	rules, _ := h.svc.ListRateRules(r.Context(), actor, base.Property.ID, nil, nil)

	q := r.URL.Query()
	pPlanID := q.Get("preview_plan")
	pUTID := q.Get("preview_unit_type")
	pStart := q.Get("preview_start")
	pEnd := q.Get("preview_end")

	var preview *domain.RatePreview
	if pPlanID != "" && pUTID != "" && pStart != "" && pEnd != "" {
		planUUID, err1 := uuid.Parse(pPlanID)
		utUUID, err2 := uuid.Parse(pUTID)
		sd, err3 := domain.ParseDate(pStart)
		ed, err4 := domain.ParseDate(pEnd)
		if err1 == nil && err2 == nil && err3 == nil && err4 == nil {
			res, err := h.svc.PreviewRates(r.Context(), actor, base.Property.ID, utUUID, planUUID, sd, ed)
			if err != nil {
				base.Error = err.Error()
			} else {
				preview = &res
			}
		} else {
			base.Error = "Invalid preview parameters."
		}
	} else {
		if len(plans) > 0 {
			pPlanID = plans[0].ID.String()
		}
		if len(uts) > 0 {
			pUTID = uts[0].ID.String()
		}
		today := time.Now().UTC()
		pStart = domain.FormatDate(today)
		pEnd = domain.FormatDate(today.AddDate(0, 0, 3))
	}

	h.renderPage(w, "rates.html", struct {
		baseView
		RatePlans         []domain.RatePlan
		UnitTypes         []domain.UnitType
		RateRules         []domain.RateRule
		Preview           *domain.RatePreview
		PreviewPlanID     string
		PreviewUnitTypeID string
		PreviewStart      string
		PreviewEnd        string
	}{
		baseView:          base,
		RatePlans:         plans,
		UnitTypes:         uts,
		RateRules:         rules,
		Preview:           preview,
		PreviewPlanID:     pPlanID,
		PreviewUnitTypeID: pUTID,
		PreviewStart:      pStart,
		PreviewEnd:        pEnd,
	})
}

func (h *Handler) handleRatePlanCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Rates", "rates")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	rp := domain.RatePlan{
		PropertyID:  base.Property.ID,
		Code:        r.FormValue("code"),
		Name:        r.FormValue("name"),
		Description: r.FormValue("description"),
		IsDefault:   r.FormValue("is_default") == "true",
		IsActive:    true,
		Priority:    parseIntDefault(r.FormValue("priority"), 100),
	}
	if _, err := h.svc.CreateRatePlan(r.Context(), actor, rp); err != nil {
		h.redirectErr(w, r, "/rates", err)
		return
	}
	redirectMsg(w, r, "/rates", "Rate plan created.")
}

func (h *Handler) handleRateRuleCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Rates", "rates")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	planID, _ := uuid.Parse(r.FormValue("rate_plan_id"))
	utID, _ := uuid.Parse(r.FormValue("unit_type_id"))
	sd, err := domain.ParseDate(r.FormValue("start_date"))
	if err != nil {
		h.redirectErr(w, r, "/rates", err)
		return
	}
	ed, err := domain.ParseDate(r.FormValue("end_date"))
	if err != nil {
		h.redirectErr(w, r, "/rates", err)
		return
	}
	var dow int
	for _, val := range r.Form["days"] {
		dow |= parseIntDefault(val, 0)
	}
	var minOverride *int
	if rawMin := strings.TrimSpace(r.FormValue("min_nights_override")); rawMin != "" {
		v := parseIntDefault(rawMin, 0)
		minOverride = &v
	}

	rr := domain.RateRule{
		PropertyID:        base.Property.ID,
		RatePlanID:        planID,
		UnitTypeID:        utID,
		Name:              r.FormValue("name"),
		StartDate:         sd,
		EndDate:           ed,
		DaysOfWeek:        domain.DaysOfWeekMask(dow),
		NightlyRatePence:  parseInt64Default(r.FormValue("nightly_rate_pence"), 0),
		MinNightsOverride: minOverride,
		Priority:          parseIntDefault(r.FormValue("priority"), 100),
		IsActive:          true,
	}
	if _, err := h.svc.CreateRateRule(r.Context(), actor, rr); err != nil {
		h.redirectErr(w, r, "/rates", err)
		return
	}
	redirectMsg(w, r, "/rates", "Rate rule created.")
}

func (h *Handler) handleRateRuleArchive(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Rates", "rates")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	if err := h.svc.ArchiveRateRule(r.Context(), actor, base.Property.ID, id); err != nil {
		h.redirectErr(w, r, "/rates", err)
		return
	}
	redirectMsg(w, r, "/rates", "Rate rule archived.")
}

// --- Availability Blocks (Legacy direct handlers; main /blocks UI in handlers_phase1_1.go) ---

func (h *Handler) handleBlocksCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Availability Blocks", "blocks")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	unitID, _ := uuid.Parse(r.FormValue("unit_id"))
	sd, err := domain.ParseDate(r.FormValue("start_date"))
	if err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	ed, err := domain.ParseDate(r.FormValue("end_date"))
	if err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	b := domain.AvailabilityBlock{
		PropertyID: base.Property.ID,
		UnitID:     unitID,
		StartDate:  sd,
		EndDate:    ed,
		BlockType:  domain.BlockType(r.FormValue("block_type")),
		Reason:     r.FormValue("reason"),
	}
	if _, err := h.svc.CreateAvailabilityBlock(r.Context(), actor, b); err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	redirectMsg(w, r, "/blocks", "Availability block created.")
}

func (h *Handler) handleBlocksRelease(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid id")
		return
	}
	base, err := h.buildBaseView(r.Context(), r, actor, "Availability Blocks", "blocks")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	if _, err := h.svc.ReleaseAvailabilityBlock(r.Context(), actor, base.Property.ID, id); err != nil {
		h.redirectErr(w, r, "/blocks", err)
		return
	}
	redirectMsg(w, r, "/blocks", "Availability block released.")
}

// --- Users & Account ---

func (h *Handler) handleUsersGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Admin Users", "users")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	users, err := h.svc.ListUsers(r.Context(), actor)
	if err != nil {
		h.renderDomainError(w, err, "Unable to Load Users", "Unable to retrieve administrative user accounts.")
		return
	}
	h.renderPage(w, "users.html", struct {
		baseView
		Users []domain.AdminUser
	}{
		baseView: base,
		Users:    users,
	})
}

func (h *Handler) handleUsersCreate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Admin Users", "users")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	propID := base.Property.ID
	_, err = h.svc.CreateUser(r.Context(), actor, service.CreateUserInput{
		PropertyID: &propID,
		Email:      r.FormValue("email"),
		FullName:   r.FormValue("full_name"),
		Role:       domain.Role(r.FormValue("role")),
		Password:   r.FormValue("password"),
	})
	if err != nil {
		h.redirectErr(w, r, "/users", err)
		return
	}
	redirectMsg(w, r, "/users", "Operator account created.")
}

func (h *Handler) handleUsersUpdate(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Request", "invalid id")
		return
	}
	_, err = h.svc.UpdateUser(r.Context(), actor, service.UpdateUserInput{
		ID:       id,
		FullName: r.FormValue("full_name"),
		Role:     domain.Role(r.FormValue("role")),
		IsActive: r.FormValue("is_active") == "true",
		Version:  parseIntDefault(r.FormValue("version"), 1),
	})
	if err != nil {
		h.redirectErr(w, r, "/users", err)
		return
	}
	redirectMsg(w, r, "/users", "User updated.")
}

func (h *Handler) handleAccountGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "My Account", "account")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	h.renderPage(w, "account.html", base)
}

func (h *Handler) handleAccountPasswordPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	res, err := h.svc.ChangeOwnPassword(r.Context(), actor, r.FormValue("current_password"), r.FormValue("new_password"))
	if err != nil {
		h.redirectErr(w, r, "/account", err)
		return
	}
	httpx.SetSessionCookie(w, h.cfg, res.SessionToken)
	redirectMsg(w, r, "/account", "Password updated and prior active sessions revoked.")
}

// --- Audit Logs ---

func (h *Handler) handleAuditGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Audit Log", "audit")
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Server Error", "failed to load property")
		return
	}
	resType := r.URL.Query().Get("resource_type")
	logs, err := h.svc.ListAuditLogs(r.Context(), actor, &base.Property.ID, resType, 150)
	if err != nil {
		h.renderDomainError(w, err, "Unable to Load Audit Log", "Unable to retrieve audit log records.")
		return
	}
	h.renderPage(w, "audit.html", struct {
		baseView
		Logs           []domain.AuditLog
		FilterResource string
	}{
		baseView:       base,
		Logs:           logs,
		FilterResource: resType,
	})
}
