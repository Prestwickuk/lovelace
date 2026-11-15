package domain

import (
	"fmt"
	"sort"
	"time"
)

const MaxRatePreviewNights = 90

// NightlyRateBreakdown describes the resolved tariff for one night of a stay.
type NightlyRateBreakdown struct {
	Date       string `json:"date"`
	Weekday    string `json:"weekday"`
	RatePence  int64  `json:"rate_pence"`
	RuleName   string `json:"rule_name"`
	RuleID     string `json:"rule_id,omitempty"`
	MinNights  int    `json:"min_nights"`
	IsOverride bool   `json:"is_override"`
}

// RatePreview summarizes resolved nightly pricing over [startDate, endDate).
type RatePreview struct {
	UnitTypeID         string                 `json:"unit_type_id"`
	UnitTypeName       string                 `json:"unit_type_name"`
	RatePlanID         string                 `json:"rate_plan_id"`
	RatePlanName       string                 `json:"rate_plan_name"`
	Currency           string                 `json:"currency"`
	StartDate          string                 `json:"start_date"`
	EndDate            string                 `json:"end_date"`
	Nights             int                    `json:"nights"`
	TotalPence         int64                  `json:"total_pence"`
	EffectiveMinNights int                    `json:"effective_min_nights"`
	EffectiveMaxNights int                    `json:"effective_max_nights"`
	MeetsStayRules     bool                   `json:"meets_stay_rules"`
	Breakdown          []NightlyRateBreakdown `json:"breakdown"`
}

// CalculateRatePreview resolves nightly rates across [startDate, endDate) using unit type base rate
// and active rate rules ordered by priority (highest wins).
func CalculateRatePreview(
	ut UnitType,
	plan RatePlan,
	rules []RateRule,
	currency string,
	startDate, endDate time.Time,
) (RatePreview, error) {
	s := time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, time.UTC)
	e := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 0, 0, 0, 0, time.UTC)

	if !e.After(s) {
		return RatePreview{}, NewValidationError(map[string]string{
			"date_range": "end_date must be strictly after start_date",
		})
	}
	nights := int(e.Sub(s).Hours() / 24)
	if nights > MaxRatePreviewNights {
		return RatePreview{}, NewValidationError(map[string]string{
			"date_range": fmt.Sprintf("preview window cannot exceed %d nights", MaxRatePreviewNights),
		})
	}

	activeRules := make([]RateRule, 0, len(rules))
	for _, r := range rules {
		if r.IsActive && r.ArchivedAt == nil && r.UnitTypeID == ut.ID && r.RatePlanID == plan.ID {
			activeRules = append(activeRules, r)
		}
	}
	// Sort highest priority first, then most recently created, then ID for deterministic resolution.
	sort.Slice(activeRules, func(i, j int) bool {
		if activeRules[i].Priority != activeRules[j].Priority {
			return activeRules[i].Priority > activeRules[j].Priority
		}
		if !activeRules[i].CreatedAt.Equal(activeRules[j].CreatedAt) {
			return activeRules[i].CreatedAt.After(activeRules[j].CreatedAt)
		}
		return activeRules[i].ID.String() < activeRules[j].ID.String()
	})

	breakdown := make([]NightlyRateBreakdown, 0, nights)
	var total int64
	effectiveMin := ut.MinNights

	for cur := s; cur.Before(e); cur = cur.AddDate(0, 0, 1) {
		rate := ut.BaseNightlyRatePence
		ruleName := "Base Tariff"
		ruleID := ""
		minNights := ut.MinNights
		isOverride := false

		for _, r := range activeRules {
			if !cur.Before(r.StartDate) && cur.Before(r.EndDate) && r.DaysOfWeek.MatchesWeekday(cur.Weekday()) {
				rate = r.NightlyRatePence
				ruleName = r.Name
				ruleID = r.ID.String()
				isOverride = true
				if r.MinNightsOverride != nil {
					minNights = *r.MinNightsOverride
				}
				break
			}
		}

		if minNights > effectiveMin {
			effectiveMin = minNights
		}
		total += rate
		breakdown = append(breakdown, NightlyRateBreakdown{
			Date:       FormatDate(cur),
			Weekday:    cur.Weekday().String()[:3],
			RatePence:  rate,
			RuleName:   ruleName,
			RuleID:     ruleID,
			MinNights:  minNights,
			IsOverride: isOverride,
		})
	}

	return RatePreview{
		UnitTypeID:         ut.ID.String(),
		UnitTypeName:       ut.Name,
		RatePlanID:         plan.ID.String(),
		RatePlanName:       plan.Name,
		Currency:           currency,
		StartDate:          FormatDate(s),
		EndDate:            FormatDate(e),
		Nights:             nights,
		TotalPence:         total,
		EffectiveMinNights: effectiveMin,
		EffectiveMaxNights: ut.MaxNights,
		MeetsStayRules:     nights >= effectiveMin && nights <= ut.MaxNights,
		Breakdown:          breakdown,
	}, nil
}

// FormatPence formats integer minor currency units for display (e.g. 9500 -> "£95.00").
func FormatPence(pence int64, currency string) string {
	symbol := "£"
	switch currency {
	case "EUR":
		symbol = "€"
	case "USD":
		symbol = "$"
	case "GBP", "":
		symbol = "£"
	default:
		symbol = currency + " "
	}
	neg := ""
	if pence < 0 {
		neg = "-"
		pence = -pence
	}
	return fmt.Sprintf("%s%s%d.%02d", neg, symbol, pence/100, pence%100)
}
