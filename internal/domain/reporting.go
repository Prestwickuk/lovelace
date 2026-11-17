package domain

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

var reportCurrencyRegex = regexp.MustCompile(`^[A-Z]{3}$`)

const (
	MaxReportRangeDays         = 366
	MaxForecastHorizonDays     = 180
	DefaultForecastHorizonDays = 30
	MaxAccountingExportRows    = 25000
	AccountingExportSchemaVer  = "lovelace.accounting.v1"
)

type ReportExportType string

const (
	ReportExportBookingLineItems ReportExportType = "booking_line_items"
	ReportExportPaymentLedger    ReportExportType = "payment_ledger"
	ReportExportRefundLedger     ReportExportType = "refund_ledger"
)

type ReportExportFormat string

const (
	ReportFormatCSV  ReportExportFormat = "csv"
	ReportFormatJSON ReportExportFormat = "json"
)

type ReportSeasonPreset string

const (
	SeasonPresetPeak     ReportSeasonPreset = "peak"
	SeasonPresetShoulder ReportSeasonPreset = "shoulder"
	SeasonPresetOffPeak  ReportSeasonPreset = "off_peak"
	SeasonPresetCustom   ReportSeasonPreset = "custom"
)

// ReportPeriod bounds a reporting query using property-local half-open dates [StartDate, EndDate).
type ReportPeriod struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Days      int    `json:"days"`
	Timezone  string `json:"timezone"`
}

// ReportMetricDefinition documents exact metric calculation formulas and exclusions.
type ReportMetricDefinition struct {
	Metric     string `json:"metric"`
	Formula    string `json:"formula"`
	Exclusions string `json:"exclusions"`
	TimeBasis  string `json:"time_basis"`
	StatusNote string `json:"status_note,omitempty"`
}

// ReportDataCompleteness surfaces gaps, manual unquoted stays, currency splits, or unmapped OTA items
// so operators never mistake incomplete source records for clean totals.
type ReportDataCompleteness struct {
	UnmappedExternalReservations        int64    `json:"unmapped_external_reservations"`
	ConflictExternalReservations        int64    `json:"conflict_external_reservations"`
	FailedExternalReservations          int64    `json:"failed_external_reservations"`
	PendingChannelSyncJobs              int64    `json:"pending_channel_sync_jobs"`
	OpenPaymentExceptions               int64    `json:"open_payment_exceptions"`
	ManualUnquotedBookingsCount         int64    `json:"manual_unquoted_bookings_count"`
	ZeroValueAcceptedStaysCount         int64    `json:"zero_value_accepted_stays_count"`
	UnresolvedPartialRefundCount        int64    `json:"unresolved_partial_refund_count"`
	UnresolvedLegacyRefundPaymentsCount int64    `json:"unresolved_legacy_refund_payments_count"`
	InventoryHistoryIncomplete          bool     `json:"inventory_history_incomplete"`
	InventoryHistoryReliableFrom        *string  `json:"inventory_history_reliable_from,omitempty"`
	MultiCurrencyDetected               bool     `json:"multi_currency_detected"`
	Currencies                          []string `json:"currencies"`
	Warnings                            []string `json:"warnings"`
}

// OccupancyMetrics summarizes inventory utilization for a period or slice.
type OccupancyMetrics struct {
	PhysicalUnitNights    int64  `json:"physical_unit_nights"`
	OutOfOrderNights      int64  `json:"out_of_order_nights"`
	OwnerAdminBlockNights int64  `json:"owner_admin_block_nights"`
	SellableUnitNights    int64  `json:"sellable_unit_nights"`
	OccupiedUnitNights    int64  `json:"occupied_unit_nights"`
	PendingHoldNights     int64  `json:"pending_hold_nights"`
	OccupancyRateBPS      *int   `json:"occupancy_rate_bps"`
	OccupancyRatePct      string `json:"occupancy_rate_pct"`
	ZeroAvailability      bool   `json:"zero_availability"`
}

type OccupancyDailyPoint struct {
	Date string `json:"date"`
	OccupancyMetrics
}

type OccupancyMonthlyPoint struct {
	Month string `json:"month"`
	OccupancyMetrics
}

type OccupancyUnitTypeBreakdown struct {
	UnitTypeID   uuid.UUID `json:"unit_type_id"`
	UnitTypeCode string    `json:"unit_type_code"`
	UnitTypeName string    `json:"unit_type_name"`
	Category     string    `json:"category"`
	UnitCount    int64     `json:"unit_count"`
	OccupancyMetrics
}

// CurrencyRevenueMetrics stores booked stay revenue and cash-flow payments strictly in a single ISO currency.
type CurrencyRevenueMetrics struct {
	Currency                    string `json:"currency"`
	AcceptedBookingsCount       int64  `json:"accepted_bookings_count"`
	OccupiedUnitNights          int64  `json:"occupied_unit_nights"`
	SellableUnitNights          int64  `json:"sellable_unit_nights"`
	QuotedAccommodationPence    int64  `json:"quoted_accommodation_pence"`
	OccupancySurchargePence     int64  `json:"occupancy_surcharge_pence"`
	DiscountPence               int64  `json:"discount_pence"`
	AccommodationNetPence       int64  `json:"accommodation_net_pence"`
	ExtrasSubtotalPence         int64  `json:"extras_subtotal_pence"`
	TaxPence                    int64  `json:"tax_pence"`
	ManualFallbackTotalPence    int64  `json:"manual_fallback_total_pence"`
	GrossBookedTotalPence       int64  `json:"gross_booked_total_pence"`
	ProvisionalPendingPence     int64  `json:"provisional_pending_pence"`
	CancelledNoShowValuePence   int64  `json:"cancelled_no_show_value_pence"`
	GrossCollectedPaymentsPence int64  `json:"gross_collected_payments_pence"`
	RefundedPence               int64  `json:"refunded_pence"`
	NetCollectedCashPence       int64  `json:"net_collected_cash_pence"`
	OutstandingBalancePence     int64  `json:"outstanding_balance_pence"`
	ADRPence                    *int64 `json:"adr_pence"`
	ADRFormatted                string `json:"adr_formatted"`
	RevPARPence                 *int64 `json:"revpar_pence"`
	RevPARFormatted             string `json:"revpar_formatted"`
	ZeroOccupancyForADR         bool   `json:"zero_occupancy_for_adr"`
	ZeroAvailabilityForRevPAR   bool   `json:"zero_availability_for_revpar"`
}

type DailyRevenuePoint struct {
	Date                        string `json:"date"`
	Currency                    string `json:"currency"`
	OccupiedUnitNights          int64  `json:"occupied_unit_nights"`
	AccommodationNetPence       int64  `json:"accommodation_net_pence"`
	GrossBookedTotalPence       int64  `json:"gross_booked_total_pence"`
	GrossCollectedPaymentsPence int64  `json:"gross_collected_payments_pence"`
	RefundedPence               int64  `json:"refunded_pence"`
	NetCollectedCashPence       int64  `json:"net_collected_cash_pence"`
}

// OccupancyReport is returned by GET /reports/occupancy.
type OccupancyReport struct {
	PropertyID   uuid.UUID                    `json:"property_id"`
	PropertyName string                       `json:"property_name"`
	Period       ReportPeriod                 `json:"period"`
	Summary      OccupancyMetrics             `json:"summary"`
	Daily        []OccupancyDailyPoint        `json:"daily"`
	Monthly      []OccupancyMonthlyPoint      `json:"monthly"`
	ByUnitType   []OccupancyUnitTypeBreakdown `json:"by_unit_type"`
	Completeness ReportDataCompleteness       `json:"completeness"`
	Definitions  []ReportMetricDefinition     `json:"definitions"`
}

// RevenueReport is returned by GET /reports/revenue.
type RevenueReport struct {
	PropertyID   uuid.UUID                `json:"property_id"`
	PropertyName string                   `json:"property_name"`
	Period       ReportPeriod             `json:"period"`
	ByCurrency   []CurrencyRevenueMetrics `json:"by_currency"`
	DailySeries  []DailyRevenuePoint      `json:"daily_series"`
	Completeness ReportDataCompleteness   `json:"completeness"`
	Definitions  []ReportMetricDefinition `json:"definitions"`
}

type ChannelPerformanceRow struct {
	ChannelCode             string  `json:"channel_code"`
	ChannelName             string  `json:"channel_name"`
	Category                string  `json:"category"`
	Currency                string  `json:"currency"`
	AcceptedBookings        int64   `json:"accepted_bookings"`
	CancelledOrNoShowCount  int64   `json:"cancelled_or_no_show_count"`
	PendingPipelineCount    int64   `json:"pending_pipeline_count"`
	OccupiedUnitNights      int64   `json:"occupied_unit_nights"`
	UnitNightShareBPS       *int    `json:"unit_night_share_bps"`
	AccommodationNetPence   int64   `json:"accommodation_net_pence"`
	GrossBookedTotalPence   int64   `json:"gross_booked_total_pence"`
	GrossCollectedCashPence int64   `json:"gross_collected_cash_pence"`
	ADRPence                *int64  `json:"adr_pence"`
	ADRFormatted            string  `json:"adr_formatted"`
	AvgLeadTimeDays         *string `json:"avg_lead_time_days"`
	ExternalMappedCount     int64   `json:"external_mapped_count"`
	ExternalUnmappedCount   int64   `json:"external_unmapped_count"`
	ExternalConflictCount   int64   `json:"external_conflict_count"`
}

type ChannelComparisonReport struct {
	PropertyID   uuid.UUID                `json:"property_id"`
	PropertyName string                   `json:"property_name"`
	Period       ReportPeriod             `json:"period"`
	Rows         []ChannelPerformanceRow  `json:"rows"`
	Completeness ReportDataCompleteness   `json:"completeness"`
	Definitions  []ReportMetricDefinition `json:"definitions"`
}

type SeasonalWindowMetrics struct {
	Label      string                   `json:"label"`
	Preset     ReportSeasonPreset       `json:"preset"`
	Period     ReportPeriod             `json:"period"`
	Occupancy  OccupancyMetrics         `json:"occupancy"`
	ByCurrency []CurrencyRevenueMetrics `json:"by_currency"`
}

type CurrencyDeltaSummary struct {
	Currency              string `json:"currency"`
	BookedTotalDeltaPence int64  `json:"booked_total_delta_pence"`
	BookedTotalDeltaPct   string `json:"booked_total_delta_pct"`
	NetCashDeltaPence     int64  `json:"net_cash_delta_pence"`
	ADRDeltaPence         *int64 `json:"adr_delta_pence"`
	RevPARDeltaPence      *int64 `json:"revpar_delta_pence"`
}

type SeasonalComparisonReport struct {
	PropertyID          uuid.UUID                `json:"property_id"`
	PropertyName        string                   `json:"property_name"`
	SeasonPreset        ReportSeasonPreset       `json:"season_preset"`
	Current             SeasonalWindowMetrics    `json:"current"`
	Comparison          SeasonalWindowMetrics    `json:"comparison"`
	OccupancyDeltaBPS   *int                     `json:"occupancy_delta_bps"`
	OccupancyDeltaLabel string                   `json:"occupancy_delta_label"`
	CurrencyDeltas      []CurrencyDeltaSummary   `json:"currency_deltas"`
	Completeness        ReportDataCompleteness   `json:"completeness"`
	Definitions         []ReportMetricDefinition `json:"definitions"`
}

type ForecastCurrencyProjection struct {
	Currency                     string `json:"currency"`
	OnTheBooksAccommodationPence int64  `json:"on_the_books_accommodation_pence"`
	OnTheBooksGrossTotalPence    int64  `json:"on_the_books_gross_total_pence"`
	ProvisionalPendingPence      int64  `json:"provisional_pending_pence"`
	TrailingBenchmarkADRPence    *int64 `json:"trailing_benchmark_adr_pence"`
	AllocatedAdditionalNights    int64  `json:"allocated_additional_nights"`
	NightShareBPS                *int   `json:"night_share_bps,omitempty"`
	ProjectedIncrementalPence    int64  `json:"projected_incremental_pence"`
	ProjectedTotalPence          int64  `json:"projected_total_pence"`
	ProjectedLowPence            int64  `json:"projected_low_pence"`
	ProjectedHighPence           int64  `json:"projected_high_pence"`
	ProjectionNote               string `json:"projection_note,omitempty"`
}

type ForecastReport struct {
	PropertyID                 uuid.UUID                    `json:"property_id"`
	PropertyName               string                       `json:"property_name"`
	AsOfDate                   string                       `json:"as_of_date"`
	HorizonDays                int                          `json:"horizon_days"`
	Window                     ReportPeriod                 `json:"window"`
	ReadOnlyGuarantee          string                       `json:"read_only_guarantee"`
	Method                     string                       `json:"method"`
	HistoricalBaselineWindow   string                       `json:"historical_baseline_window"`
	HistoricalSampleUnitNights int64                        `json:"historical_sample_unit_nights"`
	TrailingOccupancyBPS       *int                         `json:"trailing_occupancy_bps"`
	FutureSellableUnitNights   int64                        `json:"future_sellable_unit_nights"`
	OnTheBooksUnitNights       int64                        `json:"on_the_books_unit_nights"`
	PendingHoldUnitNights      int64                        `json:"pending_hold_unit_nights"`
	ProjectedAdditionalNights  int64                        `json:"projected_additional_nights"`
	ProjectedTotalUnitNights   int64                        `json:"projected_total_unit_nights"`
	ProjectedOccupancyBPS      *int                         `json:"projected_occupancy_bps"`
	ConfidenceLowNights        int64                        `json:"confidence_low_nights"`
	ConfidenceHighNights       int64                        `json:"confidence_high_nights"`
	Exclusions                 []string                     `json:"exclusions"`
	ByCurrency                 []ForecastCurrencyProjection `json:"by_currency"`
	Completeness               ReportDataCompleteness       `json:"completeness"`
	Definitions                []ReportMetricDefinition     `json:"definitions"`
}

// GuestRetentionReport intentionally returns aggregate counts only and ZERO guest IDs or PII.
type GuestRetentionReport struct {
	PropertyID                   uuid.UUID                `json:"property_id"`
	PropertyName                 string                   `json:"property_name"`
	Period                       ReportPeriod             `json:"period"`
	PrivacyNotice                string                   `json:"privacy_notice"`
	CompletedStaysCount          int64                    `json:"completed_stays_count"`
	DistinctCanonicalGuestsCount int64                    `json:"distinct_canonical_guests_count"`
	SingleStayGuestsCount        int64                    `json:"single_stay_guests_count"`
	ReturningGuestsCount         int64                    `json:"returning_guests_count"`
	ReturningGuestStaysCount     int64                    `json:"returning_guest_stays_count"`
	AnonymizedGuestStaysCount    int64                    `json:"anonymized_guest_stays_count"`
	UnlinkedCompletedStaysCount  int64                    `json:"unlinked_completed_stays_count"`
	MergedAliasStaysResolved     int64                    `json:"merged_alias_stays_resolved"`
	RepeatGuestRateBPS           *int                     `json:"repeat_guest_rate_bps"`
	RepeatGuestRatePct           string                   `json:"repeat_guest_rate_pct"`
	AvgStaysPerGuest             string                   `json:"avg_stays_per_guest"`
	Definitions                  []ReportMetricDefinition `json:"definitions"`
}

// OverviewReport bundles the Phase 5 executive dashboard slices cleanly for UI/overview JSON.
type OverviewReport struct {
	PropertyID   uuid.UUID                `json:"property_id"`
	PropertyName string                   `json:"property_name"`
	Period       ReportPeriod             `json:"period"`
	Occupancy    OccupancyReport          `json:"occupancy"`
	Revenue      RevenueReport            `json:"revenue"`
	Channels     ChannelComparisonReport  `json:"channels"`
	Forecast     ForecastReport           `json:"forecast"`
	Retention    GuestRetentionReport     `json:"retention"`
	Unsupported  []UnsupportedMetricEntry `json:"unsupported_fields"`
}

type UnsupportedMetricEntry struct {
	Field  string `json:"field"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// AccountingExportResult is returned cleanly by JSON and CSV accounting exports.
type AccountingExportResult struct {
	SchemaVersion                       string                   `json:"schema_version"`
	PropertyID                          uuid.UUID                `json:"property_id"`
	PropertyName                        string                   `json:"property_name"`
	ExportType                          ReportExportType         `json:"export_type"`
	Period                              ReportPeriod             `json:"period"`
	CurrencyFilter                      string                   `json:"currency_filter,omitempty"`
	RowCount                            int                      `json:"row_count"`
	Truncated                           bool                     `json:"truncated"`
	UnresolvedLegacyRefundPaymentsCount int64                    `json:"unresolved_legacy_refund_payments_count,omitempty"`
	Warnings                            []string                 `json:"warnings,omitempty"`
	UnsupportedFields                   []UnsupportedMetricEntry `json:"unsupported_fields"`
	Headers                             []string                 `json:"headers"`
	Rows                                []map[string]string      `json:"rows"`
}

// AccountingUnsupportedFields explicitly marks financial ledger concepts that do not exist yet.
func AccountingUnsupportedFields() []UnsupportedMetricEntry {
	return []UnsupportedMetricEntry{
		{
			Field:  "ota_commission_pence",
			Status: "NOT IMPLEMENTED",
			Reason: "LOVELACE does not persist OTA commission fee breakdowns from external channel payouts.",
		},
		{
			Field:  "payout_settlement_date",
			Status: "NOT IMPLEMENTED",
			Reason: "Stripe / OTA bank payout batch settlement reconciliation timestamps are not persisted.",
		},
		{
			Field:  "net_of_fee_settlement_pence",
			Status: "NOT IMPLEMENTED",
			Reason: "Merchant processing fees and net bank transfer amounts are outside the current booking schema.",
		},
		{
			Field:  "unit_historical_commissioning_dates",
			Status: "NOT IMPLEMENTED",
			Reason: "Units expose created_at, archived_at, and current status without backdated commission/decommission effective dates.",
		},
	}
}

// ValidateReportWindow parses and bounds [startDate, endDate) to 1..366 calendar nights.
func ValidateReportWindow(startRaw, endRaw string) (time.Time, time.Time, error) {
	errs := map[string]string{}
	start, err := ParseDate(strings.TrimSpace(startRaw))
	if err != nil {
		errs["start_date"] = "must be YYYY-MM-DD"
	}
	end, err := ParseDate(strings.TrimSpace(endRaw))
	if err != nil {
		errs["end_date"] = "must be YYYY-MM-DD"
	}
	if len(errs) > 0 {
		return time.Time{}, time.Time{}, NewValidationError(errs)
	}
	if !end.After(start) {
		errs["date_range"] = "end_date must be strictly after start_date"
		return time.Time{}, time.Time{}, NewValidationError(errs)
	}
	days := int(end.Sub(start).Hours() / 24)
	if days > MaxReportRangeDays {
		errs["date_range"] = fmt.Sprintf("date range cannot exceed %d days", MaxReportRangeDays)
		return time.Time{}, time.Time{}, NewValidationError(errs)
	}
	return start, end, nil
}

// ValidateCurrencyFilter validates an optional 3-letter ISO currency code filter.
func ValidateCurrencyFilter(raw string) (string, error) {
	c := strings.ToUpper(strings.TrimSpace(raw))
	if c == "" {
		return "", nil
	}
	if !reportCurrencyRegex.MatchString(c) {
		return "", NewValidationError(map[string]string{
			"currency": "must be a 3-letter uppercase ISO currency code",
		})
	}
	return c, nil
}

// ValidateReportExportType validates supported accounting export feeds.
func ValidateReportExportType(raw string) (ReportExportType, error) {
	t := ReportExportType(strings.ToLower(strings.TrimSpace(raw)))
	if t == "" {
		t = ReportExportBookingLineItems
	}
	switch t {
	case ReportExportBookingLineItems, ReportExportPaymentLedger, ReportExportRefundLedger:
		return t, nil
	default:
		return "", NewValidationError(map[string]string{
			"type": "must be one of booking_line_items, payment_ledger, refund_ledger",
		})
	}
}

// ValidateReportExportFormat validates csv or json export formats.
func ValidateReportExportFormat(raw string) (ReportExportFormat, error) {
	f := ReportExportFormat(strings.ToLower(strings.TrimSpace(raw)))
	if f == "" {
		f = ReportFormatCSV
	}
	switch f {
	case ReportFormatCSV, ReportFormatJSON:
		return f, nil
	default:
		return "", NewValidationError(map[string]string{
			"format": "must be csv or json",
		})
	}
}

// ValidateSeasonPreset validates seasonal reporting comparison presets.
func ValidateSeasonPreset(raw string) (ReportSeasonPreset, error) {
	p := ReportSeasonPreset(strings.ToLower(strings.TrimSpace(raw)))
	if p == "" {
		return SeasonPresetPeak, nil
	}
	switch p {
	case SeasonPresetPeak, SeasonPresetShoulder, SeasonPresetOffPeak, SeasonPresetCustom:
		return p, nil
	default:
		return "", NewValidationError(map[string]string{
			"season": "must be peak, shoulder, off_peak, or custom",
		})
	}
}

// SafeBPS returns rounded basis points (numerator / denominator * 10000), or nil if denominator <= 0.
func SafeBPS(numerator, denominator int64) (*int, string) {
	if denominator <= 0 {
		return nil, "N/A (zero denominator)"
	}
	bps := int((numerator*10000 + denominator/2) / denominator)
	return &bps, fmt.Sprintf("%.2f%%", float64(bps)/100.0)
}

// SafeDividePence returns rounded minor units (numerator / denominator), or nil if denominator <= 0.
func SafeDividePence(numerator, denominator int64, currency string) (*int64, string) {
	if denominator <= 0 {
		return nil, "N/A"
	}
	val := (numerator + denominator/2) / denominator
	return &val, FormatReportMoney(currency, val)
}

// FormatReportMoney formats minor units using the explicit 3-letter currency code so unlike currencies are unmistakable.
func FormatReportMoney(currency string, pence int64) string {
	code := strings.ToUpper(strings.TrimSpace(currency))
	if code == "" || code == "GBP" {
		return FormatPence(pence, "GBP")
	}
	sign := ""
	abs := pence
	if abs < 0 {
		sign = "-"
		abs = -abs
	}
	return fmt.Sprintf("%s%s %d.%02d", sign, code, abs/100, abs%100)
}

// SanitizeCSVCell defends spreadsheet exports against formula injection (`=`, `+`, `-`, `@`, `\t`, `\r`,
// including after leading whitespace) while preserving signed numeric monetary columns cleanly when numeric-safe
// or prefixing `'` whenever untrusted text or ambiguous formula starters appear.
func SanitizeCSVCell(raw string, numericColumn bool) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\x00' {
			return -1
		}
		return r
	}, raw)
	if cleaned == "" {
		return ""
	}
	if numericColumn && isStrictSignedIntegerOrDecimal(cleaned) {
		return cleaned
	}
	trimmedLeft := strings.TrimLeftFunc(cleaned, unicode.IsSpace)
	first := cleaned[0]
	if first == '=' || first == '+' || first == '-' || first == '@' || first == '\t' || first == '\r' || first == '\n' {
		return "'" + cleaned
	}
	if len(trimmedLeft) > 0 {
		t := trimmedLeft[0]
		if t == '=' || t == '+' || t == '-' || t == '@' {
			return "'" + cleaned
		}
	}
	return cleaned
}

func isStrictSignedIntegerOrDecimal(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '-' {
		start = 1
	}
	if start >= len(s) {
		return false
	}
	dotSeen := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			if dotSeen || i == start || i == len(s)-1 {
				return false
			}
			dotSeen = true
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// RenderAccountingCSV renders deterministic UTF-8 RFC 4180 CSV with strict formula-injection neutralization.
func RenderAccountingCSV(export AccountingExportResult) ([]byte, error) {
	var buf bytes.Buffer
	// Write UTF-8 BOM only? No — keep clean UTF-8 without BOM so CLI/pandas/Excel/RFC4180 parsers agree cleanly,
	// unless needed; standard clean UTF-8 CSV headers are preferred across Go/accounting importers.
	w := csv.NewWriter(&buf)
	w.UseCRLF = false

	numericCols := map[string]bool{
		"nights":                    true,
		"unit_nights":               true,
		"accommodation_pence":       true,
		"occupancy_surcharge_pence": true,
		"extras_pence":              true,
		"discount_pence":            true,
		"tax_pence":                 true,
		"gross_total_pence":         true,
		"amount_paid_pence":         true,
		"balance_due_pence":         true,
		"amount_pence":              true,
	}

	headers := make([]string, len(export.Headers))
	for i, h := range export.Headers {
		headers[i] = SanitizeCSVCell(h, false)
	}
	if err := w.Write(headers); err != nil {
		return nil, err
	}
	for _, row := range export.Rows {
		record := make([]string, len(export.Headers))
		for i, h := range export.Headers {
			record[i] = SanitizeCSVCell(row[h], numericCols[h])
		}
		if err := w.Write(record); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// AllocateProjectedAdditionalNights deterministically distributes property-wide projected
// additional unit-nights across currencies using Hamilton largest-remainder apportionment over
// observed unit-night weights so the allocated currency nights sum to totalNights (or zero when
// no weighted currency history exists for multi-currency allocation).
func AllocateProjectedAdditionalNights(totalNights int64, weights map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(weights))
	if totalNights <= 0 || len(weights) == 0 {
		for k := range weights {
			out[k] = 0
		}
		return out
	}

	type share struct {
		cur    string
		weight int64
		floor  int64
		rem    int64
	}

	var totalWeight int64
	shares := make([]share, 0, len(weights))
	for c, w := range weights {
		if w > 0 {
			totalWeight += w
			shares = append(shares, share{cur: c, weight: w})
		} else {
			out[c] = 0
		}
	}

	if totalWeight <= 0 {
		// Only assign 100% if a single currency exists and needs no cross-currency split.
		if len(weights) == 1 {
			for c := range weights {
				out[c] = totalNights
			}
		}
		return out
	}

	var sumFloor int64
	for i := range shares {
		num := totalNights * shares[i].weight
		shares[i].floor = num / totalWeight
		shares[i].rem = num % totalWeight
		out[shares[i].cur] = shares[i].floor
		sumFloor += shares[i].floor
	}

	sort.Slice(shares, func(i, j int) bool {
		if shares[i].rem != shares[j].rem {
			return shares[i].rem > shares[j].rem
		}
		if shares[i].weight != shares[j].weight {
			return shares[i].weight > shares[j].weight
		}
		return shares[i].cur < shares[j].cur
	})

	remaining := totalNights - sumFloor
	for i := 0; i < len(shares) && remaining > 0; i++ {
		out[shares[i].cur]++
		remaining--
	}
	return out
}
