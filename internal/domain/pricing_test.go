package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCalculateRatePreview(t *testing.T) {
	propID := uuid.New()
	utID := uuid.New()
	planID := uuid.New()

	ut := UnitType{
		ID:                   utID,
		PropertyID:           propID,
		Code:                 "bell-tent",
		Name:                 "Furnished Bell Tent",
		Category:             CategoryGlamping,
		BaseCapacityAdults:   2,
		MaxAdults:            2,
		MaxGuests:            4,
		BaseNightlyRatePence: 9500,
		MinNights:            2,
		MaxNights:            14,
	}
	plan := RatePlan{
		ID:         planID,
		PropertyID: propID,
		Code:       "standard",
		Name:       "Standard",
	}

	minThree := 3
	rules := []RateRule{
		{
			ID:               uuid.New(),
			PropertyID:       propID,
			RatePlanID:       planID,
			UnitTypeID:       utID,
			Name:             "Summer Midweek",
			StartDate:        time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
			EndDate:          time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			DaysOfWeek:       DayAll,
			NightlyRatePence: 11000,
			Priority:         100,
			IsActive:         true,
		},
		{
			ID:                uuid.New(),
			PropertyID:        propID,
			RatePlanID:        planID,
			UnitTypeID:        utID,
			Name:              "Peak Summer Weekend",
			StartDate:         time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
			EndDate:           time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			DaysOfWeek:        DayFri | DaySat,
			NightlyRatePence:  13500,
			MinNightsOverride: &minThree,
			Priority:          200,
			IsActive:          true,
		},
	}

	// Thu 2026-07-16 (Summer Midweek 11000), Fri 2026-07-17 (Weekend 13500), Sat 2026-07-18 (Weekend 13500)
	// Departure Sun 2026-07-19 (exclusive)
	preview, err := CalculateRatePreview(
		ut, plan, rules, "GBP",
		time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preview.Nights != 3 {
		t.Fatalf("expected 3 nights, got %d", preview.Nights)
	}
	if preview.TotalPence != 11000+13500+13500 {
		t.Fatalf("expected total 38000, got %d", preview.TotalPence)
	}
	if preview.EffectiveMinNights != 3 {
		t.Fatalf("expected effective min nights 3, got %d", preview.EffectiveMinNights)
	}
	if !preview.MeetsStayRules {
		t.Fatalf("expected stay rules to pass for 3 nights")
	}

	// Boundary test: departure day 2026-09-01 is outside [2026-07-15, 2026-09-01) -> falls back to base rate 9500
	sepPreview, err := CalculateRatePreview(
		ut, plan, rules, "GBP",
		time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sepPreview.Breakdown[0].RatePence != 11000 || sepPreview.Breakdown[1].RatePence != 9500 {
		t.Fatalf("unexpected boundary rates: %+v", sepPreview.Breakdown)
	}
}

func TestFormatPence(t *testing.T) {
	if got := FormatPence(9500, "GBP"); got != "£95.00" {
		t.Fatalf("got %q, want £95.00", got)
	}
	if got := FormatPence(505, "GBP"); got != "£5.05" {
		t.Fatalf("got %q, want £5.05", got)
	}
}
