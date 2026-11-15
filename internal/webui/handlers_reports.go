package webui

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func (h *Handler) registerReportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /reports", h.requirePageAuth(false, h.handleReportsGet))
	mux.HandleFunc("GET /reports/export", h.requirePageAuth(false, h.handleReportsExportGet))
}

func (h *Handler) renderReportDomainError(w http.ResponseWriter, err error, title string) {
	var verr *domain.ValidationError
	if errors.As(err, &verr) {
		h.renderError(w, http.StatusUnprocessableEntity, title, verr.Error())
		return
	}
	h.renderDomainError(w, err, title, "Unable to load the requested report.")
}

func (h *Handler) handleReportsGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Reports & Business Intelligence", "reports")
	if err != nil {
		h.renderReportDomainError(w, err, "Reports Unavailable")
		return
	}
	if base.Property.ID == ([16]byte{}) {
		h.renderError(w, http.StatusNotFound, "Property Not Found", "No property record is assigned to this administrator.")
		return
	}

	q := r.URL.Query()
	startDate := strings.TrimSpace(q.Get("start_date"))
	endDate := strings.TrimSpace(q.Get("end_date"))
	currency := strings.TrimSpace(q.Get("currency"))
	season := strings.TrimSpace(q.Get("season"))
	if season == "" {
		season = "peak"
	}
	compareStart := strings.TrimSpace(q.Get("compare_start"))
	compareEnd := strings.TrimSpace(q.Get("compare_end"))

	horizon := domain.DefaultForecastHorizonDays
	if rawH := strings.TrimSpace(q.Get("horizon_days")); rawH != "" {
		if parsed, perr := strconv.Atoi(rawH); perr == nil && parsed > 0 {
			horizon = parsed
		}
	}

	overview, err := h.svc.GetOverviewReport(r.Context(), actor, base.Property.ID, service.ReportWindowQuery{
		StartDate: startDate,
		EndDate:   endDate,
		Currency:  currency,
	}, horizon)
	if err != nil {
		h.renderReportDomainError(w, err, "Invalid Report Query")
		return
	}

	seasonal, err := h.svc.GetSeasonalComparisonReport(r.Context(), actor, base.Property.ID, service.SeasonalComparisonQuery{
		Season:       season,
		StartDate:    startDate,
		EndDate:      endDate,
		CompareStart: compareStart,
		CompareEnd:   compareEnd,
		Currency:     currency,
	})
	if err != nil {
		h.renderReportDomainError(w, err, "Invalid Seasonal Query")
		return
	}

	data := struct {
		baseView
		FilterStart        string
		FilterEnd          string
		FilterCurrency     string
		FilterSeason       string
		FilterCompareStart string
		FilterCompareEnd   string
		FilterHorizon      int
		Overview           domain.OverviewReport
		Seasonal           domain.SeasonalComparisonReport
	}{
		baseView:           base,
		FilterStart:        overview.Period.StartDate,
		FilterEnd:          overview.Period.EndDate,
		FilterCurrency:     currency,
		FilterSeason:       season,
		FilterCompareStart: seasonal.Comparison.Period.StartDate,
		FilterCompareEnd:   seasonal.Comparison.Period.EndDate,
		FilterHorizon:      horizon,
		Overview:           overview,
		Seasonal:           seasonal,
	}
	h.renderPage(w, "reports.html", data)
}

func (h *Handler) handleReportsExportGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Accounting Export", "reports")
	if err != nil {
		h.renderReportDomainError(w, err, "Accounting Export Unavailable")
		return
	}
	q := r.URL.Query()
	res, csvBytes, err := h.svc.ExportAccountingReport(r.Context(), actor, base.Property.ID, service.AccountingExportQuery{
		Type:      q.Get("type"),
		Format:    "csv",
		StartDate: q.Get("start_date"),
		EndDate:   q.Get("end_date"),
		Currency:  q.Get("currency"),
	})
	if err != nil {
		h.renderReportDomainError(w, err, "Accounting Export Failed")
		return
	}
	filename := fmt.Sprintf("lovelace-%s-%s-to-%s.csv", res.ExportType, res.Period.StartDate, res.Period.EndDate)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(csvBytes)
}
