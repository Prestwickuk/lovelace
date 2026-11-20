package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func (h *Handler) registerReportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/reports/overview", h.requireAuth(false, h.handleReportOverview))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/reports/occupancy", h.requireAuth(false, h.handleReportOccupancy))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/reports/revenue", h.requireAuth(false, h.handleReportRevenue))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/reports/channels", h.requireAuth(false, h.handleReportChannels))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/reports/seasons", h.requireAuth(false, h.handleReportSeasons))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/reports/forecast", h.requireAuth(false, h.handleReportForecast))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/reports/retention", h.requireAuth(false, h.handleReportRetention))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/reports/accounting-export", h.requireAuth(false, h.handleReportAccountingExport))
}

func (h *Handler) handleReportOverview(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	horizon := domain.DefaultForecastHorizonDays
	if raw := strings.TrimSpace(q.Get("horizon_days")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_horizon_days", "horizon_days must be an integer")
			return
		}
		horizon = v
	}
	res, err := h.svc.GetOverviewReport(r.Context(), actor, propID, service.ReportWindowQuery{
		StartDate: q.Get("start_date"),
		EndDate:   q.Get("end_date"),
		Currency:  q.Get("currency"),
	}, horizon)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleReportOccupancy(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	res, err := h.svc.GetOccupancyReport(r.Context(), actor, propID, service.ReportWindowQuery{
		StartDate: q.Get("start_date"),
		EndDate:   q.Get("end_date"),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleReportRevenue(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	res, err := h.svc.GetRevenueReport(r.Context(), actor, propID, service.ReportWindowQuery{
		StartDate: q.Get("start_date"),
		EndDate:   q.Get("end_date"),
		Currency:  q.Get("currency"),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleReportChannels(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	res, err := h.svc.GetChannelComparisonReport(r.Context(), actor, propID, service.ReportWindowQuery{
		StartDate: q.Get("start_date"),
		EndDate:   q.Get("end_date"),
		Currency:  q.Get("currency"),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleReportSeasons(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	res, err := h.svc.GetSeasonalComparisonReport(r.Context(), actor, propID, service.SeasonalComparisonQuery{
		Season:       q.Get("season"),
		StartDate:    q.Get("start_date"),
		EndDate:      q.Get("end_date"),
		CompareStart: q.Get("compare_start"),
		CompareEnd:   q.Get("compare_end"),
		Currency:     q.Get("currency"),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleReportForecast(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	horizon := 0
	if raw := strings.TrimSpace(q.Get("horizon_days")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_horizon_days", "horizon_days must be an integer")
			return
		}
		horizon = v
	}
	res, err := h.svc.GetForecastReport(r.Context(), actor, propID, service.ForecastQuery{
		AsOfDate:    q.Get("as_of_date"),
		HorizonDays: horizon,
		Currency:    q.Get("currency"),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleReportRetention(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	res, err := h.svc.GetGuestRetentionReport(r.Context(), actor, propID, service.ReportWindowQuery{
		StartDate: q.Get("start_date"),
		EndDate:   q.Get("end_date"),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleReportAccountingExport(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	q := r.URL.Query()
	expFormat, err := domain.ValidateReportExportFormat(q.Get("format"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	res, csvBytes, err := h.svc.ExportAccountingReport(r.Context(), actor, propID, service.AccountingExportQuery{
		Type:      q.Get("type"),
		Format:    string(expFormat),
		StartDate: q.Get("start_date"),
		EndDate:   q.Get("end_date"),
		Currency:  q.Get("currency"),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if expFormat == domain.ReportFormatJSON {
		writeJSON(w, http.StatusOK, res)
		return
	}
	filename := fmt.Sprintf("lovelace-%s-%s-to-%s.csv", res.ExportType, res.Period.StartDate, res.Period.EndDate)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(csvBytes)
}
