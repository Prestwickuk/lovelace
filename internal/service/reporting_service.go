package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

type ReportWindowQuery struct {
	StartDate string
	EndDate   string
	Currency  string
}

type SeasonalComparisonQuery struct {
	Season       string
	StartDate    string
	EndDate      string
	CompareStart string
	CompareEnd   string
	Currency     string
}

type ForecastQuery struct {
	AsOfDate    string
	HorizonDays int
	Currency    string
}

type AccountingExportQuery struct {
	Type      string
	Format    string
	StartDate string
	EndDate   string
	Currency  string
}

func (s *Service) resolveReportWindow(prop domain.Property, startRaw, endRaw string) (time.Time, time.Time, error) {
	if strings.TrimSpace(startRaw) == "" || strings.TrimSpace(endRaw) == "" {
		loc, err := time.LoadLocation(prop.Timezone)
		if err != nil {
			loc = time.UTC
		}
		nowLocal := time.Now().In(loc)
		endDt := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
		startDt := endDt.AddDate(0, 0, -30)
		return startDt, endDt, nil
	}
	return domain.ValidateReportWindow(startRaw, endRaw)
}

func (s *Service) GetOccupancyReport(ctx context.Context, actor Actor, propertyID uuid.UUID, q ReportWindowQuery) (domain.OccupancyReport, error) {
	if err := s.requirePerm(actor, auth.PermReportsRead); err != nil {
		return domain.OccupancyReport{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.OccupancyReport{}, err
	}
	prop, err := s.store.GetPropertyReportMetadata(ctx, propertyID)
	if err != nil {
		return domain.OccupancyReport{}, err
	}
	startDt, endDt, err := s.resolveReportWindow(prop, q.StartDate, q.EndDate)
	if err != nil {
		return domain.OccupancyReport{}, err
	}
	return s.store.GetOccupancyReport(ctx, prop, startDt, endDt)
}

func (s *Service) GetRevenueReport(ctx context.Context, actor Actor, propertyID uuid.UUID, q ReportWindowQuery) (domain.RevenueReport, error) {
	if err := s.requirePerm(actor, auth.PermReportsRead); err != nil {
		return domain.RevenueReport{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.RevenueReport{}, err
	}
	curFilter, err := domain.ValidateCurrencyFilter(q.Currency)
	if err != nil {
		return domain.RevenueReport{}, err
	}
	prop, err := s.store.GetPropertyReportMetadata(ctx, propertyID)
	if err != nil {
		return domain.RevenueReport{}, err
	}
	startDt, endDt, err := s.resolveReportWindow(prop, q.StartDate, q.EndDate)
	if err != nil {
		return domain.RevenueReport{}, err
	}
	return s.store.GetRevenueReport(ctx, prop, startDt, endDt, curFilter)
}

func (s *Service) GetChannelComparisonReport(ctx context.Context, actor Actor, propertyID uuid.UUID, q ReportWindowQuery) (domain.ChannelComparisonReport, error) {
	if err := s.requirePerm(actor, auth.PermReportsRead); err != nil {
		return domain.ChannelComparisonReport{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.ChannelComparisonReport{}, err
	}
	curFilter, err := domain.ValidateCurrencyFilter(q.Currency)
	if err != nil {
		return domain.ChannelComparisonReport{}, err
	}
	prop, err := s.store.GetPropertyReportMetadata(ctx, propertyID)
	if err != nil {
		return domain.ChannelComparisonReport{}, err
	}
	startDt, endDt, err := s.resolveReportWindow(prop, q.StartDate, q.EndDate)
	if err != nil {
		return domain.ChannelComparisonReport{}, err
	}
	return s.store.GetChannelComparison(ctx, prop, startDt, endDt, curFilter)
}

func (s *Service) GetForecastReport(ctx context.Context, actor Actor, propertyID uuid.UUID, q ForecastQuery) (domain.ForecastReport, error) {
	if err := s.requirePerm(actor, auth.PermReportsRead); err != nil {
		return domain.ForecastReport{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.ForecastReport{}, err
	}
	curFilter, err := domain.ValidateCurrencyFilter(q.Currency)
	if err != nil {
		return domain.ForecastReport{}, err
	}
	horizon := q.HorizonDays
	if horizon == 0 {
		horizon = domain.DefaultForecastHorizonDays
	}
	if horizon < 1 || horizon > domain.MaxForecastHorizonDays {
		return domain.ForecastReport{}, domain.NewValidationError(map[string]string{
			"horizon_days": fmt.Sprintf("must be between 1 and %d", domain.MaxForecastHorizonDays),
		})
	}
	prop, err := s.store.GetPropertyReportMetadata(ctx, propertyID)
	if err != nil {
		return domain.ForecastReport{}, err
	}
	var asOf time.Time
	if strings.TrimSpace(q.AsOfDate) == "" {
		loc, err := time.LoadLocation(prop.Timezone)
		if err != nil {
			loc = time.UTC
		}
		nowLocal := time.Now().In(loc)
		asOf = time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, time.UTC)
	} else {
		asOf, err = domain.ParseDate(q.AsOfDate)
		if err != nil {
			return domain.ForecastReport{}, domain.NewValidationError(map[string]string{
				"as_of_date": "must be YYYY-MM-DD",
			})
		}
	}
	return s.store.GetForecastReport(ctx, prop, asOf, horizon, curFilter)
}

func (s *Service) GetGuestRetentionReport(ctx context.Context, actor Actor, propertyID uuid.UUID, q ReportWindowQuery) (domain.GuestRetentionReport, error) {
	if err := s.requirePerm(actor, auth.PermReportsRead); err != nil {
		return domain.GuestRetentionReport{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.GuestRetentionReport{}, err
	}
	prop, err := s.store.GetPropertyReportMetadata(ctx, propertyID)
	if err != nil {
		return domain.GuestRetentionReport{}, err
	}
	startDt, endDt, err := s.resolveReportWindow(prop, q.StartDate, q.EndDate)
	if err != nil {
		return domain.GuestRetentionReport{}, err
	}
	return s.store.GetGuestRetentionReport(ctx, prop, startDt, endDt)
}

func (s *Service) GetSeasonalComparisonReport(ctx context.Context, actor Actor, propertyID uuid.UUID, q SeasonalComparisonQuery) (domain.SeasonalComparisonReport, error) {
	if err := s.requirePerm(actor, auth.PermReportsRead); err != nil {
		return domain.SeasonalComparisonReport{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.SeasonalComparisonReport{}, err
	}
	preset, err := domain.ValidateSeasonPreset(q.Season)
	if err != nil {
		return domain.SeasonalComparisonReport{}, err
	}
	curFilter, err := domain.ValidateCurrencyFilter(q.Currency)
	if err != nil {
		return domain.SeasonalComparisonReport{}, err
	}
	prop, err := s.store.GetPropertyReportMetadata(ctx, propertyID)
	if err != nil {
		return domain.SeasonalComparisonReport{}, err
	}

	curStart, curEnd, err := s.resolveReportWindow(prop, q.StartDate, q.EndDate)
	if err != nil {
		return domain.SeasonalComparisonReport{}, err
	}
	var compStart, compEnd time.Time
	curLabel := "Selected Window"
	compLabel := "Prior Year Equivalent"

	switch preset {
	case domain.SeasonPresetCustom:
		if strings.TrimSpace(q.CompareStart) == "" || strings.TrimSpace(q.CompareEnd) == "" {
			return domain.SeasonalComparisonReport{}, domain.NewValidationError(map[string]string{
				"compare_dates": "compare_start and compare_end are required for custom season comparisons",
			})
		}
		compStart, compEnd, err = domain.ValidateReportWindow(q.CompareStart, q.CompareEnd)
		if err != nil {
			return domain.SeasonalComparisonReport{}, err
		}
		curLabel = "Primary Window"
		compLabel = "Comparison Window"
	case domain.SeasonPresetPeak, domain.SeasonPresetShoulder, domain.SeasonPresetOffPeak:
		// If explicit range wasn't supplied, use property-local reference year summer peak / shoulder / off-peak windows.
		if strings.TrimSpace(q.StartDate) == "" || strings.TrimSpace(q.EndDate) == "" {
			loc, lerr := time.LoadLocation(prop.Timezone)
			if lerr != nil {
				loc = time.UTC
			}
			yr := time.Now().In(loc).Year()
			switch preset {
			case domain.SeasonPresetPeak:
				curStart = time.Date(yr, 7, 1, 0, 0, 0, 0, time.UTC)
				curEnd = time.Date(yr, 9, 1, 0, 0, 0, 0, time.UTC)
				curLabel = fmt.Sprintf("Peak Summer %d (Jul-Aug)", yr)
				compLabel = fmt.Sprintf("Peak Summer %d (Jul-Aug)", yr-1)
			case domain.SeasonPresetShoulder:
				curStart = time.Date(yr, 5, 1, 0, 0, 0, 0, time.UTC)
				curEnd = time.Date(yr, 7, 1, 0, 0, 0, 0, time.UTC)
				curLabel = fmt.Sprintf("Shoulder Spring %d (May-Jun)", yr)
				compLabel = fmt.Sprintf("Shoulder Spring %d (May-Jun)", yr-1)
			case domain.SeasonPresetOffPeak:
				curStart = time.Date(yr, 1, 1, 0, 0, 0, 0, time.UTC)
				curEnd = time.Date(yr, 4, 1, 0, 0, 0, 0, time.UTC)
				curLabel = fmt.Sprintf("Off-Peak Q1 %d (Jan-Mar)", yr)
				compLabel = fmt.Sprintf("Off-Peak Q1 %d (Jan-Mar)", yr-1)
			}
		} else {
			curLabel = fmt.Sprintf("%s (%s to %s)", strings.ToUpper(string(preset)), domain.FormatDate(curStart), domain.FormatDate(curEnd))
		}
		if strings.TrimSpace(q.CompareStart) != "" && strings.TrimSpace(q.CompareEnd) != "" {
			compStart, compEnd, err = domain.ValidateReportWindow(q.CompareStart, q.CompareEnd)
			if err != nil {
				return domain.SeasonalComparisonReport{}, err
			}
		} else {
			compStart = curStart.AddDate(-1, 0, 0)
			compEnd = curEnd.AddDate(-1, 0, 0)
		}
	}

	curOcc, err := s.store.GetOccupancyReport(ctx, prop, curStart, curEnd)
	if err != nil {
		return domain.SeasonalComparisonReport{}, err
	}
	curRev, err := s.store.GetRevenueReport(ctx, prop, curStart, curEnd, curFilter)
	if err != nil {
		return domain.SeasonalComparisonReport{}, err
	}
	compOcc, err := s.store.GetOccupancyReport(ctx, prop, compStart, compEnd)
	if err != nil {
		return domain.SeasonalComparisonReport{}, err
	}
	compRev, err := s.store.GetRevenueReport(ctx, prop, compStart, compEnd, curFilter)
	if err != nil {
		return domain.SeasonalComparisonReport{}, err
	}

	var occDeltaBPS *int
	occDeltaLabel := "N/A"
	if curOcc.Summary.OccupancyRateBPS != nil && compOcc.Summary.OccupancyRateBPS != nil {
		d := *curOcc.Summary.OccupancyRateBPS - *compOcc.Summary.OccupancyRateBPS
		occDeltaBPS = &d
		occDeltaLabel = fmt.Sprintf("%+.2f pp", float64(d)/100.0)
	}

	curCurMap := map[string]domain.CurrencyRevenueMetrics{}
	compCurMap := map[string]domain.CurrencyRevenueMetrics{}
	curKeys := map[string]bool{}
	for _, r := range curRev.ByCurrency {
		curCurMap[r.Currency] = r
		curKeys[r.Currency] = true
	}
	for _, r := range compRev.ByCurrency {
		compCurMap[r.Currency] = r
		curKeys[r.Currency] = true
	}
	var deltas []domain.CurrencyDeltaSummary
	for cur := range curKeys {
		cr := curCurMap[cur]
		pr := compCurMap[cur]
		bookedDelta := cr.GrossBookedTotalPence - pr.GrossBookedTotalPence
		bookedPct := "N/A (zero baseline)"
		if pr.GrossBookedTotalPence > 0 {
			bookedPct = fmt.Sprintf("%+.1f%%", float64(bookedDelta)*100.0/float64(pr.GrossBookedTotalPence))
		}
		var adrDelta, revparDelta *int64
		if cr.ADRPence != nil && pr.ADRPence != nil {
			v := *cr.ADRPence - *pr.ADRPence
			adrDelta = &v
		}
		if cr.RevPARPence != nil && pr.RevPARPence != nil {
			v := *cr.RevPARPence - *pr.RevPARPence
			revparDelta = &v
		}
		deltas = append(deltas, domain.CurrencyDeltaSummary{
			Currency:              cur,
			BookedTotalDeltaPence: bookedDelta,
			BookedTotalDeltaPct:   bookedPct,
			NetCashDeltaPence:     cr.NetCollectedCashPence - pr.NetCollectedCashPence,
			ADRDeltaPence:         adrDelta,
			RevPARDeltaPence:      revparDelta,
		})
	}

	return domain.SeasonalComparisonReport{
		PropertyID:   prop.ID,
		PropertyName: prop.Name,
		SeasonPreset: preset,
		Current: domain.SeasonalWindowMetrics{
			Label:      curLabel,
			Preset:     preset,
			Period:     curOcc.Period,
			Occupancy:  curOcc.Summary,
			ByCurrency: curRev.ByCurrency,
		},
		Comparison: domain.SeasonalWindowMetrics{
			Label:      compLabel,
			Preset:     preset,
			Period:     compOcc.Period,
			Occupancy:  compOcc.Summary,
			ByCurrency: compRev.ByCurrency,
		},
		OccupancyDeltaBPS:   occDeltaBPS,
		OccupancyDeltaLabel: occDeltaLabel,
		CurrencyDeltas:      deltas,
		Completeness:        curRev.Completeness,
		Definitions:         revenueMetricDefinitionsCopy(curRev.Definitions),
	}, nil
}

func revenueMetricDefinitionsCopy(defs []domain.ReportMetricDefinition) []domain.ReportMetricDefinition {
	out := make([]domain.ReportMetricDefinition, len(defs))
	copy(out, defs)
	return out
}

func (s *Service) GetOverviewReport(ctx context.Context, actor Actor, propertyID uuid.UUID, q ReportWindowQuery, horizonDays int) (domain.OverviewReport, error) {
	if err := s.requirePerm(actor, auth.PermReportsRead); err != nil {
		return domain.OverviewReport{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.OverviewReport{}, err
	}
	occ, err := s.GetOccupancyReport(ctx, actor, propertyID, q)
	if err != nil {
		return domain.OverviewReport{}, err
	}
	rev, err := s.GetRevenueReport(ctx, actor, propertyID, q)
	if err != nil {
		return domain.OverviewReport{}, err
	}
	ch, err := s.GetChannelComparisonReport(ctx, actor, propertyID, q)
	if err != nil {
		return domain.OverviewReport{}, err
	}
	fc, err := s.GetForecastReport(ctx, actor, propertyID, ForecastQuery{HorizonDays: horizonDays, Currency: q.Currency})
	if err != nil {
		return domain.OverviewReport{}, err
	}
	ret, err := s.GetGuestRetentionReport(ctx, actor, propertyID, q)
	if err != nil {
		return domain.OverviewReport{}, err
	}
	return domain.OverviewReport{
		PropertyID:   propertyID,
		PropertyName: occ.PropertyName,
		Period:       occ.Period,
		Occupancy:    occ,
		Revenue:      rev,
		Channels:     ch,
		Forecast:     fc,
		Retention:    ret,
		Unsupported:  domain.AccountingUnsupportedFields(),
	}, nil
}

// ExportAccountingReport generates repeatable accounting export data (CSV or JSON), enforces
// PermReportsExport, and records an immutable PII-free audit event (`report.exported`).
func (s *Service) ExportAccountingReport(ctx context.Context, actor Actor, propertyID uuid.UUID, q AccountingExportQuery) (domain.AccountingExportResult, []byte, error) {
	if err := s.requirePerm(actor, auth.PermReportsExport); err != nil {
		return domain.AccountingExportResult{}, nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.AccountingExportResult{}, nil, err
	}
	expType, err := domain.ValidateReportExportType(q.Type)
	if err != nil {
		return domain.AccountingExportResult{}, nil, err
	}
	expFmt, err := domain.ValidateReportExportFormat(q.Format)
	if err != nil {
		return domain.AccountingExportResult{}, nil, err
	}
	curFilter, err := domain.ValidateCurrencyFilter(q.Currency)
	if err != nil {
		return domain.AccountingExportResult{}, nil, err
	}
	prop, err := s.store.GetPropertyReportMetadata(ctx, propertyID)
	if err != nil {
		return domain.AccountingExportResult{}, nil, err
	}
	startDt, endDt, err := s.resolveReportWindow(prop, q.StartDate, q.EndDate)
	if err != nil {
		return domain.AccountingExportResult{}, nil, err
	}

	result, err := s.store.GetAccountingExport(ctx, prop, expType, startDt, endDt, curFilter)
	if err != nil {
		return domain.AccountingExportResult{}, nil, err
	}

	var csvBytes []byte
	if expFmt == domain.ReportFormatCSV {
		csvBytes, err = domain.RenderAccountingCSV(result)
		if err != nil {
			return domain.AccountingExportResult{}, nil, err
		}
	}

	exportID, err := postgres.NewUUID()
	if err != nil {
		return domain.AccountingExportResult{}, nil, err
	}
	nowUTC := time.Now().UTC()

	if err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		return s.recordAudit(ctx, tx, actor, &propertyID, "report.exported", "report_export", exportID.String(), nil, map[string]any{
			"id":          exportID,
			"property_id": propertyID,
			"export_type": string(expType),
			"format":      string(expFmt),
			"start_date":  domain.FormatDate(startDt),
			"end_date":    domain.FormatDate(endDt),
			"currency":    curFilter,
			"row_count":   result.RowCount,
			"created_at":  nowUTC,
		})
	}); err != nil {
		return domain.AccountingExportResult{}, nil, err
	}

	return result, csvBytes, nil
}
