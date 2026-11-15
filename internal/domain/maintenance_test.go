package domain

import (
	"testing"
	"time"
)

func dUTC(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestAdvanceDueDateCalendarTransitions(t *testing.T) {
	cases := []struct {
		name      string
		freq      MaintenanceFrequency
		from      time.Time
		want      time.Time
		wantValid bool
	}{
		// 31 January
		{name: "monthly_jan31_non_leap", freq: MaintFreqMonthly, from: dUTC(2026, time.January, 31), want: dUTC(2026, time.February, 28), wantValid: true},
		{name: "monthly_jan31_leap", freq: MaintFreqMonthly, from: dUTC(2024, time.January, 31), want: dUTC(2024, time.February, 29), wantValid: true},
		{name: "quarterly_jan31", freq: MaintFreqQuarterly, from: dUTC(2026, time.January, 31), want: dUTC(2026, time.April, 30), wantValid: true},
		{name: "six_monthly_jan31", freq: MaintFreqSixMonthly, from: dUTC(2026, time.January, 31), want: dUTC(2026, time.July, 31), wantValid: true},

		// 29 February
		{name: "monthly_feb29", freq: MaintFreqMonthly, from: dUTC(2024, time.February, 29), want: dUTC(2024, time.March, 29), wantValid: true},
		{name: "quarterly_feb29", freq: MaintFreqQuarterly, from: dUTC(2024, time.February, 29), want: dUTC(2024, time.May, 29), wantValid: true},
		{name: "six_monthly_feb29", freq: MaintFreqSixMonthly, from: dUTC(2024, time.February, 29), want: dUTC(2024, time.August, 29), wantValid: true},
		{name: "annually_feb29_to_non_leap", freq: MaintFreqAnnually, from: dUTC(2024, time.February, 29), want: dUTC(2025, time.February, 28), wantValid: true},
		{name: "annually_2099_to_century_non_leap_2100", freq: MaintFreqAnnually, from: dUTC(2099, time.February, 28), want: dUTC(2100, time.February, 28), wantValid: true},

		// End of March
		{name: "monthly_mar31", freq: MaintFreqMonthly, from: dUTC(2026, time.March, 31), want: dUTC(2026, time.April, 30), wantValid: true},
		{name: "weekly_mar31", freq: MaintFreqWeekly, from: dUTC(2026, time.March, 31), want: dUTC(2026, time.April, 7), wantValid: true},

		// Quarterly & six-monthly end-of-month clamping
		{name: "quarterly_nov30_to_feb28", freq: MaintFreqQuarterly, from: dUTC(2025, time.November, 30), want: dUTC(2026, time.February, 28), wantValid: true},
		{name: "quarterly_nov30_leap_feb29", freq: MaintFreqQuarterly, from: dUTC(2023, time.November, 30), want: dUTC(2024, time.February, 29), wantValid: true},
		{name: "six_monthly_aug31_to_feb28", freq: MaintFreqSixMonthly, from: dUTC(2025, time.August, 31), want: dUTC(2026, time.February, 28), wantValid: true},
		{name: "six_monthly_aug31_leap_feb29", freq: MaintFreqSixMonthly, from: dUTC(2023, time.August, 31), want: dUTC(2024, time.February, 29), wantValid: true},
		{name: "six_monthly_mar31_to_sep30", freq: MaintFreqSixMonthly, from: dUTC(2026, time.March, 31), want: dUTC(2026, time.September, 30), wantValid: true},

		// Once
		{name: "once_stops", freq: MaintFreqOnce, from: dUTC(2026, time.January, 31), wantValid: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.freq.AdvanceDueDate(tc.from)
			if ok != tc.wantValid {
				t.Fatalf("AdvanceDueDate(%v) valid=%v, want %v", tc.from, ok, tc.wantValid)
			}
			if ok && !got.Equal(tc.want) {
				t.Fatalf("AdvanceDueDate(%v) = %s, want %s", tc.from, got.Format("2006-01-02"), tc.want.Format("2006-01-02"))
			}
		})
	}
}

func TestAdvanceDueDateWithAnchorPreservesIntendedDay(t *testing.T) {
	// Monthly schedule anchored on day 31: Jan 31 -> Feb 28 -> Mar 31 -> Apr 30 -> May 31
	cur := dUTC(2026, time.January, 31)
	expected := []time.Time{
		dUTC(2026, time.February, 28),
		dUTC(2026, time.March, 31),
		dUTC(2026, time.April, 30),
		dUTC(2026, time.May, 31),
	}
	for i, want := range expected {
		next, ok := MaintFreqMonthly.AdvanceDueDateWithAnchor(cur, 31)
		if !ok || !next.Equal(want) {
			t.Fatalf("step %d from %s: got %s (ok=%v), want %s", i, cur.Format("2006-01-02"), next.Format("2006-01-02"), ok, want.Format("2006-01-02"))
		}
		cur = next
	}

	// Annual schedule anchored on Feb 29: 2024-02-29 -> 2025-02-28 -> 2026-02-28 -> 2027-02-28 -> 2028-02-29 -> 2100-02-28
	cur = dUTC(2024, time.February, 29)
	annualWant := []time.Time{
		dUTC(2025, time.February, 28),
		dUTC(2026, time.February, 28),
		dUTC(2027, time.February, 28),
		dUTC(2028, time.February, 29),
	}
	for i, want := range annualWant {
		next, ok := MaintFreqAnnually.AdvanceDueDateWithAnchor(cur, 29)
		if !ok || !next.Equal(want) {
			t.Fatalf("annual step %d from %s: got %s (ok=%v), want %s", i, cur.Format("2006-01-02"), next.Format("2006-01-02"), ok, want.Format("2006-01-02"))
		}
		cur = next
	}
	// Leap century check: 2096-02-29 anchored on 29 advances over 2100 (non-leap) to 2100-02-28.
	nextCentury, ok := MaintFreqAnnually.AdvanceDueDateWithAnchor(dUTC(2099, time.February, 28), 29)
	if !ok || !nextCentury.Equal(dUTC(2100, time.February, 28)) {
		t.Fatalf("expected 2100-02-28 (century non-leap), got %s", nextCentury.Format("2006-01-02"))
	}
}

func TestResolveDueCatchUpPolicy(t *testing.T) {
	// Not due yet
	if _, _, _, due := MaintFreqMonthly.ResolveDueCatchUp(dUTC(2026, time.June, 1), dUTC(2026, time.May, 31), 1); due {
		t.Fatal("expected due=false for future next_due_date")
	}

	// Multi-year monthly schedule missed since 2023-01-31 as of 2026-05-15 -> consolidated to 2026-04-30, next future 2026-05-31
	occ, nextFuture, skipped, due := MaintFreqMonthly.ResolveDueCatchUp(dUTC(2023, time.January, 31), dUTC(2026, time.May, 15), 31)
	if !due {
		t.Fatal("expected overdue schedule to be due")
	}
	if !occ.Equal(dUTC(2026, time.April, 30)) {
		t.Fatalf("expected consolidated occDate 2026-04-30, got %s", occ.Format("2006-01-02"))
	}
	if nextFuture == nil || !nextFuture.Equal(dUTC(2026, time.May, 31)) {
		t.Fatalf("expected nextFuture 2026-05-31, got %v", nextFuture)
	}
	if skipped != 39 {
		t.Fatalf("expected 39 skipped monthly intervals (Feb 2023 .. Apr 2026), got %d", skipped)
	}

	// Weekly schedule missed by 104 weeks (2 years) + 3 days
	start := dUTC(2024, time.January, 1)
	asOf := start.AddDate(0, 0, 104*7+3)
	occW, nextW, skippedW, dueW := MaintFreqWeekly.ResolveDueCatchUp(start, asOf, 0)
	if !dueW || skippedW != 104 || !occW.Equal(start.AddDate(0, 0, 104*7)) || nextW == nil || !nextW.Equal(start.AddDate(0, 0, 105*7)) {
		t.Fatalf("unexpected weekly catch-up: due=%v skipped=%d occ=%s next=%v", dueW, skippedW, occW.Format("2006-01-02"), nextW)
	}

	// ONCE schedule due cleanly with no nextFuture
	occOnce, nextOnce, skippedOnce, dueOnce := MaintFreqOnce.ResolveDueCatchUp(dUTC(2024, time.May, 1), dUTC(2026, time.May, 15), 0)
	if !dueOnce || !occOnce.Equal(dUTC(2024, time.May, 1)) || nextOnce != nil || skippedOnce != 0 {
		t.Fatalf("unexpected ONCE catch-up: due=%v occ=%s next=%v skipped=%d", dueOnce, occOnce.Format("2006-01-02"), nextOnce, skippedOnce)
	}
}
