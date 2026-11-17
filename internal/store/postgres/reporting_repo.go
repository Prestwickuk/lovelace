package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/domain"
)

// ProrateIntegerPence deterministically splits totalPence across totalNights without rounding loss,
// returning the slice of per-night amounts (length == totalNights).
func ProrateIntegerPence(totalPence int64, totalNights int) []int64 {
	if totalNights <= 0 {
		return nil
	}
	out := make([]int64, totalNights)
	base := totalPence / int64(totalNights)
	rem := int(totalPence % int64(totalNights))
	step := 1
	if rem < 0 {
		step = -1
		rem = -rem
	}
	for i := 0; i < totalNights; i++ {
		val := base
		if i < rem {
			val += int64(step)
		}
		out[i] = val
	}
	return out
}

type reportBookingRow struct {
	ID                     uuid.UUID
	Reference              string
	ChannelCode            string
	ChannelName            string
	ChannelCategory        string
	Status                 domain.ReservationStatus
	PaymentStatus          string
	ArrivalDate            time.Time
	DepartureDate          time.Time
	UnitCount              int64
	UnitTypeCode           string
	Currency               string
	ReservationTotalPence  int64
	ReservationPaidPence   int64
	ReservationBalDuePence int64
	CreatedAt              time.Time
	HasQuote               bool
	QuoteAccommodation     int64
	QuoteSurcharge         int64
	QuoteExtras            int64
	QuoteDiscount          int64
	QuoteTax               int64
	QuoteTotal             int64
}

type reportCashRow struct {
	LocalDate   time.Time
	Currency    string
	ChannelCode string
	AmountPence int64
}

func (s *Store) GetPropertyReportMetadata(ctx context.Context, propertyID uuid.UUID) (domain.Property, error) {
	return s.GetPropertyByID(ctx, s.pool, propertyID)
}

// GetOccupancyReport computes historical/forward physical, closure/out-of-order, owner/admin,
// sellable, accepted occupied, and provisional hold unit-nights cleanly from inventory_claims & reservation_units.
func (s *Store) GetOccupancyReport(ctx context.Context, prop domain.Property, startDate, endDate time.Time) (domain.OccupancyReport, error) {
	startStr := domain.FormatDate(startDate)
	endStr := domain.FormatDate(endDate)
	daysCount := int(endDate.Sub(startDate).Hours() / 24)

	report := domain.OccupancyReport{
		PropertyID:   prop.ID,
		PropertyName: prop.Name,
		Period: domain.ReportPeriod{
			StartDate: startStr,
			EndDate:   endStr,
			Days:      daysCount,
			Timezone:  prop.Timezone,
		},
		Definitions: occupancyMetricDefinitions(),
	}

	// Daily inventory & occupancy rollup across effective-dated physical units from unit_status_history.
	dailyRows, err := s.pool.Query(ctx, `
		WITH days AS (
			SELECT gs::date AS day_date
			FROM generate_series($2::date, ($3::date - INTERVAL '1 day')::date, INTERVAL '1 day') AS gs
		),
		unit_day_state AS (
			SELECT
				d.day_date,
				u.id AS unit_id,
				ush.status AS effective_status
			FROM days d
			JOIN units u
			  ON u.property_id = $1
			CROSS JOIN LATERAL (
				SELECT h.status, h.is_archived
				FROM unit_status_history h
				WHERE h.property_id = $1
				  AND h.unit_id = u.id
				  AND h.effective_from < ((d.day_date + 1)::timestamp AT TIME ZONE $4)
				ORDER BY h.effective_from DESC, h.created_at DESC, h.id DESC
				LIMIT 1
			) ush
			WHERE NOT ush.is_archived
			  AND ush.status IN ('active', 'maintenance')
		),
		unit_day AS (
			SELECT
				d.day_date,
				uds.unit_id,
				(
					uds.unit_id IS NOT NULL AND (
						uds.effective_status = 'maintenance'
						OR EXISTS (
							SELECT 1 FROM inventory_claims ic
							WHERE ic.property_id = $1
							  AND ic.unit_id = uds.unit_id
							  AND ic.status = 'ACTIVE'
							  AND ic.claim_type IN ('MAINTENANCE', 'SEASONAL_CLOSURE', 'WEATHER_CLOSURE')
							  AND ic.claim_range @> d.day_date
						)
					)
				) AS is_ooo,
				(
					uds.unit_id IS NOT NULL AND EXISTS (
						SELECT 1 FROM inventory_claims ic
						WHERE ic.property_id = $1
						  AND ic.unit_id = uds.unit_id
						  AND ic.status = 'ACTIVE'
						  AND ic.claim_type IN ('OWNER_USE', 'ADMINISTRATIVE_BLOCK', 'OPERATIONAL_BUFFER')
						  AND ic.claim_range @> d.day_date
					)
				) AS is_owner_admin,
				(
					uds.unit_id IS NOT NULL AND EXISTS (
						SELECT 1
						FROM reservation_units ru
						JOIN reservations r ON r.id = ru.reservation_id
						WHERE r.property_id = $1
						  AND ru.unit_id = uds.unit_id
						  AND r.status IN ('CONFIRMED', 'CHECKED_IN', 'CHECKED_OUT')
						  AND r.stay_range @> d.day_date
					)
				) AS is_occupied,
				(
					uds.unit_id IS NOT NULL AND (
						EXISTS (
							SELECT 1
							FROM reservation_units ru
							JOIN reservations r ON r.id = ru.reservation_id
							WHERE r.property_id = $1
							  AND ru.unit_id = uds.unit_id
							  AND r.status = 'PENDING'
							  AND r.stay_range @> d.day_date
						)
						OR EXISTS (
							SELECT 1 FROM inventory_claims ic
							WHERE ic.property_id = $1
							  AND ic.unit_id = uds.unit_id
							  AND ic.status = 'ACTIVE'
							  AND ic.claim_type = 'HOLD'
							  AND ic.claim_range @> d.day_date
						)
					)
				) AS is_pending_hold
			FROM days d
			LEFT JOIN unit_day_state uds ON uds.day_date = d.day_date
		)
		SELECT
			day_date,
			COUNT(unit_id)::bigint AS physical_unit_nights,
			COUNT(unit_id) FILTER (WHERE is_ooo)::bigint AS ooo_nights,
			COUNT(unit_id) FILTER (WHERE NOT is_ooo AND is_owner_admin)::bigint AS owner_admin_nights,
			COUNT(unit_id) FILTER (WHERE NOT is_ooo AND NOT is_owner_admin)::bigint AS sellable_unit_nights,
			COUNT(unit_id) FILTER (WHERE is_occupied)::bigint AS occupied_unit_nights,
			COUNT(unit_id) FILTER (WHERE NOT is_occupied AND is_pending_hold)::bigint AS pending_hold_nights
		FROM unit_day
		GROUP BY day_date
		ORDER BY day_date ASC
	`, prop.ID, startStr, endStr, prop.Timezone)
	if err != nil {
		return domain.OccupancyReport{}, MapErr(err)
	}
	defer dailyRows.Close()

	monthAccum := map[string]*domain.OccupancyMetrics{}
	var monthKeys []string

	for dailyRows.Next() {
		var dayDt time.Time
		var phys, ooo, ownerAdm, sellable, occ, pend int64
		if err := dailyRows.Scan(&dayDt, &phys, &ooo, &ownerAdm, &sellable, &occ, &pend); err != nil {
			return domain.OccupancyReport{}, MapErr(err)
		}
		m := buildOccupancyMetrics(phys, ooo, ownerAdm, sellable, occ, pend)
		report.Daily = append(report.Daily, domain.OccupancyDailyPoint{
			Date:             domain.FormatDate(dayDt),
			OccupancyMetrics: m,
		})
		report.Summary.PhysicalUnitNights += phys
		report.Summary.OutOfOrderNights += ooo
		report.Summary.OwnerAdminBlockNights += ownerAdm
		report.Summary.SellableUnitNights += sellable
		report.Summary.OccupiedUnitNights += occ
		report.Summary.PendingHoldNights += pend

		monthKey := dayDt.Format("2006-01")
		acc, ok := monthAccum[monthKey]
		if !ok {
			acc = &domain.OccupancyMetrics{}
			monthAccum[monthKey] = acc
			monthKeys = append(monthKeys, monthKey)
		}
		acc.PhysicalUnitNights += phys
		acc.OutOfOrderNights += ooo
		acc.OwnerAdminBlockNights += ownerAdm
		acc.SellableUnitNights += sellable
		acc.OccupiedUnitNights += occ
		acc.PendingHoldNights += pend
	}
	if err := dailyRows.Err(); err != nil {
		return domain.OccupancyReport{}, MapErr(err)
	}

	report.Summary = buildOccupancyMetrics(
		report.Summary.PhysicalUnitNights,
		report.Summary.OutOfOrderNights,
		report.Summary.OwnerAdminBlockNights,
		report.Summary.SellableUnitNights,
		report.Summary.OccupiedUnitNights,
		report.Summary.PendingHoldNights,
	)

	sort.Strings(monthKeys)
	for _, mk := range monthKeys {
		acc := monthAccum[mk]
		report.Monthly = append(report.Monthly, domain.OccupancyMonthlyPoint{
			Month: mk,
			OccupancyMetrics: buildOccupancyMetrics(
				acc.PhysicalUnitNights,
				acc.OutOfOrderNights,
				acc.OwnerAdminBlockNights,
				acc.SellableUnitNights,
				acc.OccupiedUnitNights,
				acc.PendingHoldNights,
			),
		})
	}

	// Unit-type breakdown using exact same effective-dated unit_status_history supply rules.
	utRows, err := s.pool.Query(ctx, `
		WITH days AS (
			SELECT gs::date AS day_date
			FROM generate_series($2::date, ($3::date - INTERVAL '1 day')::date, INTERVAL '1 day') AS gs
		),
		unit_day_state AS (
			SELECT
				d.day_date,
				u.id AS unit_id,
				ush.status AS effective_status,
				ut.id AS unit_type_id,
				ut.code AS unit_type_code,
				ut.name AS unit_type_name,
				ut.category
			FROM days d
			JOIN units u
			  ON u.property_id = $1
			CROSS JOIN LATERAL (
				SELECT h.unit_type_id, h.status, h.is_archived
				FROM unit_status_history h
				WHERE h.property_id = $1
				  AND h.unit_id = u.id
				  AND h.effective_from < ((d.day_date + 1)::timestamp AT TIME ZONE $4)
				ORDER BY h.effective_from DESC, h.created_at DESC, h.id DESC
				LIMIT 1
			) ush
			JOIN unit_types ut ON ut.id = ush.unit_type_id
			WHERE NOT ush.is_archived
			  AND ush.status IN ('active', 'maintenance')
		)
		SELECT
			au.unit_type_id,
			au.unit_type_code,
			au.unit_type_name,
			au.category,
			COUNT(DISTINCT au.unit_id)::bigint AS unit_count,
			COUNT(*)::bigint AS physical_unit_nights,
			COUNT(*) FILTER (WHERE
				au.effective_status = 'maintenance'
				OR EXISTS (
					SELECT 1 FROM inventory_claims ic
					WHERE ic.property_id = $1 AND ic.unit_id = au.unit_id AND ic.status = 'ACTIVE'
					  AND ic.claim_type IN ('MAINTENANCE', 'SEASONAL_CLOSURE', 'WEATHER_CLOSURE')
					  AND ic.claim_range @> au.day_date
				)
			)::bigint AS ooo_nights,
			COUNT(*) FILTER (WHERE
				NOT (
					au.effective_status = 'maintenance'
					OR EXISTS (
						SELECT 1 FROM inventory_claims ic
						WHERE ic.property_id = $1 AND ic.unit_id = au.unit_id AND ic.status = 'ACTIVE'
						  AND ic.claim_type IN ('MAINTENANCE', 'SEASONAL_CLOSURE', 'WEATHER_CLOSURE')
						  AND ic.claim_range @> au.day_date
					)
				)
				AND EXISTS (
					SELECT 1 FROM inventory_claims ic
					WHERE ic.property_id = $1 AND ic.unit_id = au.unit_id AND ic.status = 'ACTIVE'
					  AND ic.claim_type IN ('OWNER_USE', 'ADMINISTRATIVE_BLOCK', 'OPERATIONAL_BUFFER')
					  AND ic.claim_range @> au.day_date
				)
			)::bigint AS owner_admin_nights,
			COUNT(*) FILTER (WHERE EXISTS (
				SELECT 1
				FROM reservation_units ru
				JOIN reservations r ON r.id = ru.reservation_id
				WHERE r.property_id = $1 AND ru.unit_id = au.unit_id
				  AND r.status IN ('CONFIRMED', 'CHECKED_IN', 'CHECKED_OUT')
				  AND r.stay_range @> au.day_date
			))::bigint AS occupied_unit_nights,
			COUNT(*) FILTER (WHERE
				NOT EXISTS (
					SELECT 1
					FROM reservation_units ru
					JOIN reservations r ON r.id = ru.reservation_id
					WHERE r.property_id = $1 AND ru.unit_id = au.unit_id
					  AND r.status IN ('CONFIRMED', 'CHECKED_IN', 'CHECKED_OUT')
					  AND r.stay_range @> au.day_date
				)
				AND (
					EXISTS (
						SELECT 1
						FROM reservation_units ru
						JOIN reservations r ON r.id = ru.reservation_id
						WHERE r.property_id = $1 AND ru.unit_id = au.unit_id
						  AND r.status = 'PENDING'
						  AND r.stay_range @> au.day_date
					)
					OR EXISTS (
						SELECT 1 FROM inventory_claims ic
						WHERE ic.property_id = $1 AND ic.unit_id = au.unit_id AND ic.status = 'ACTIVE'
						  AND ic.claim_type = 'HOLD'
						  AND ic.claim_range @> au.day_date
					)
				)
			)::bigint AS pending_hold_nights
		FROM unit_day_state au
		GROUP BY au.unit_type_id, au.unit_type_code, au.unit_type_name, au.category
		ORDER BY au.unit_type_code ASC
	`, prop.ID, startStr, endStr, prop.Timezone)
	if err != nil {
		return domain.OccupancyReport{}, MapErr(err)
	}
	defer utRows.Close()

	for utRows.Next() {
		var ut domain.OccupancyUnitTypeBreakdown
		var phys, ooo, ownerAdm, occ, pend int64
		if err := utRows.Scan(&ut.UnitTypeID, &ut.UnitTypeCode, &ut.UnitTypeName, &ut.Category, &ut.UnitCount, &phys, &ooo, &ownerAdm, &occ, &pend); err != nil {
			return domain.OccupancyReport{}, MapErr(err)
		}
		sellable := phys - ooo - ownerAdm
		if sellable < 0 {
			sellable = 0
		}
		ut.OccupancyMetrics = buildOccupancyMetrics(phys, ooo, ownerAdm, sellable, occ, pend)
		report.ByUnitType = append(report.ByUnitType, ut)
	}
	if err := utRows.Err(); err != nil {
		return domain.OccupancyReport{}, MapErr(err)
	}

	var gapIncomplete bool
	var gapReliableFrom *time.Time
	if err := s.pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM inventory_history_gaps g
				WHERE g.property_id = $1
				  AND (g.reliable_from AT TIME ZONE $3)::date > $2::date
			),
			MAX((g.reliable_from AT TIME ZONE $3)::date) FILTER (WHERE (g.reliable_from AT TIME ZONE $3)::date > $2::date)
		FROM inventory_history_gaps g
		WHERE g.property_id = $1
	`, prop.ID, startStr, prop.Timezone).Scan(&gapIncomplete, &gapReliableFrom); err != nil {
		return domain.OccupancyReport{}, MapErr(err)
	}
	var gapReliableStr *string
	if gapIncomplete && gapReliableFrom != nil {
		s := domain.FormatDate(*gapReliableFrom)
		gapReliableStr = &s
	}

	comp, err := s.GetReportCompleteness(ctx, prop.ID, &completenessOverrides{
		invHistoryIncomplete:   gapIncomplete,
		invHistoryReliableFrom: gapReliableStr,
	})
	if err != nil {
		return domain.OccupancyReport{}, err
	}
	report.Completeness = comp
	return report, nil
}

// GetRevenueReport separates booked stay-date prorated revenue (accommodation, extras, discounts, tax)
// from cash-flow collected payments and refunds using property-local timezone dates, strictly grouped by currency.
func (s *Store) GetRevenueReport(ctx context.Context, prop domain.Property, startDate, endDate time.Time, currencyFilter string) (domain.RevenueReport, error) {
	occ, err := s.GetOccupancyReport(ctx, prop, startDate, endDate)
	if err != nil {
		return domain.RevenueReport{}, err
	}
	bookings, err := s.fetchPeriodBookings(ctx, prop, startDate, endDate, currencyFilter)
	if err != nil {
		return domain.RevenueReport{}, err
	}
	payments, err := s.fetchPeriodPayments(ctx, prop, startDate, endDate, currencyFilter)
	if err != nil {
		return domain.RevenueReport{}, err
	}
	refunds, err := s.fetchPeriodRefunds(ctx, prop, startDate, endDate, currencyFilter)
	if err != nil {
		return domain.RevenueReport{}, err
	}

	byCurrency := map[string]*domain.CurrencyRevenueMetrics{}
	dailyByKey := map[string]*domain.DailyRevenuePoint{}
	ensureCurrency := func(cur string) *domain.CurrencyRevenueMetrics {
		m, ok := byCurrency[cur]
		if !ok {
			m = &domain.CurrencyRevenueMetrics{
				Currency:           cur,
				SellableUnitNights: occ.Summary.SellableUnitNights,
			}
			byCurrency[cur] = m
		}
		return m
	}
	ensureDaily := func(dayStr, cur string) *domain.DailyRevenuePoint {
		k := dayStr + "|" + cur
		d, ok := dailyByKey[k]
		if !ok {
			d = &domain.DailyRevenuePoint{Date: dayStr, Currency: cur}
			dailyByKey[k] = d
		}
		return d
	}

	var manualUnquotedCount, zeroValueAcceptedCount int64
	seenBookedRes := map[uuid.UUID]bool{}

	for _, b := range bookings {
		stayNights := int(b.DepartureDate.Sub(b.ArrivalDate).Hours() / 24)
		if stayNights <= 0 {
			continue
		}
		unitCount := b.UnitCount
		if unitCount < 0 {
			unitCount = 0
		}

		// Calculate overlap values across stay nights.
		var accNetTotal, taxTotal, extrasTotal, discountTotal, grossTotal, manualFallback int64
		var quoteAcc, quoteSur int64
		if b.HasQuote {
			quoteAcc = b.QuoteAccommodation
			quoteSur = b.QuoteSurcharge
			discountTotal = b.QuoteDiscount
			accNetTotal = quoteAcc + quoteSur - discountTotal
			extrasTotal = b.QuoteExtras
			taxTotal = b.QuoteTax
			grossTotal = b.QuoteTotal
		} else {
			grossTotal = b.ReservationTotalPence
			manualFallback = b.ReservationTotalPence
			accNetTotal = b.ReservationTotalPence
			if b.Status == domain.ResStatusConfirmed || b.Status == domain.ResStatusCheckedIn || b.Status == domain.ResStatusCheckedOut {
				manualUnquotedCount++
			}
		}

		overlapNights := 0
		var overlapGross, overlapAccNet, overlapQuoteAcc, overlapQuoteSur, overlapExtras, overlapDiscount, overlapTax, overlapFallback int64

		accSlices := ProrateIntegerPence(quoteAcc, stayNights)
		surSlices := ProrateIntegerPence(quoteSur, stayNights)
		discSlices := ProrateIntegerPence(discountTotal, stayNights)
		accNetSlices := ProrateIntegerPence(accNetTotal, stayNights)
		extSlices := ProrateIntegerPence(extrasTotal, stayNights)
		taxSlices := ProrateIntegerPence(taxTotal, stayNights)
		grossSlices := ProrateIntegerPence(grossTotal, stayNights)
		fbSlices := ProrateIntegerPence(manualFallback, stayNights)

		for idx := 0; idx < stayNights; idx++ {
			curDay := b.ArrivalDate.AddDate(0, 0, idx)
			if curDay.Before(startDate) || !curDay.Before(endDate) {
				continue
			}
			overlapNights++
			overlapQuoteAcc += accSlices[idx]
			overlapQuoteSur += surSlices[idx]
			overlapDiscount += discSlices[idx]
			overlapAccNet += accNetSlices[idx]
			overlapExtras += extSlices[idx]
			overlapTax += taxSlices[idx]
			overlapGross += grossSlices[idx]
			overlapFallback += fbSlices[idx]

			if b.Status == domain.ResStatusConfirmed || b.Status == domain.ResStatusCheckedIn || b.Status == domain.ResStatusCheckedOut {
				dp := ensureDaily(domain.FormatDate(curDay), b.Currency)
				dp.OccupiedUnitNights += unitCount
				dp.AccommodationNetPence += accNetSlices[idx]
				dp.GrossBookedTotalPence += grossSlices[idx]
			}
		}
		if overlapNights == 0 {
			continue
		}

		cm := ensureCurrency(b.Currency)
		switch b.Status {
		case domain.ResStatusConfirmed, domain.ResStatusCheckedIn, domain.ResStatusCheckedOut:
			if !seenBookedRes[b.ID] {
				seenBookedRes[b.ID] = true
				cm.AcceptedBookingsCount++
				cm.OutstandingBalancePence += b.ReservationBalDuePence
				if grossTotal == 0 {
					zeroValueAcceptedCount++
				}
			}
			cm.OccupiedUnitNights += int64(overlapNights) * unitCount
			cm.QuotedAccommodationPence += overlapQuoteAcc
			cm.OccupancySurchargePence += overlapQuoteSur
			cm.DiscountPence += overlapDiscount
			cm.AccommodationNetPence += overlapAccNet
			cm.ExtrasSubtotalPence += overlapExtras
			cm.TaxPence += overlapTax
			cm.ManualFallbackTotalPence += overlapFallback
			cm.GrossBookedTotalPence += overlapGross
		case domain.ResStatusPending, domain.ResStatusInquiry:
			cm.ProvisionalPendingPence += overlapGross
		case domain.ResStatusCancelled, domain.ResStatusNoShow:
			cm.CancelledNoShowValuePence += overlapGross
		}
	}

	for _, p := range payments {
		cm := ensureCurrency(p.Currency)
		cm.GrossCollectedPaymentsPence += p.AmountPence
		dp := ensureDaily(domain.FormatDate(p.LocalDate), p.Currency)
		dp.GrossCollectedPaymentsPence += p.AmountPence
	}
	for _, rf := range refunds {
		cm := ensureCurrency(rf.Currency)
		cm.RefundedPence += rf.AmountPence
		dp := ensureDaily(domain.FormatDate(rf.LocalDate), rf.Currency)
		dp.RefundedPence += rf.AmountPence
	}

	var currencies []string
	for cur, cm := range byCurrency {
		cm.NetCollectedCashPence = cm.GrossCollectedPaymentsPence - cm.RefundedPence
		adr, adrFmt := domain.SafeDividePence(cm.AccommodationNetPence, cm.OccupiedUnitNights, cur)
		cm.ADRPence = adr
		cm.ADRFormatted = adrFmt
		cm.ZeroOccupancyForADR = (cm.OccupiedUnitNights == 0)

		revpar, revparFmt := domain.SafeDividePence(cm.AccommodationNetPence, cm.SellableUnitNights, cur)
		cm.RevPARPence = revpar
		cm.RevPARFormatted = revparFmt
		cm.ZeroAvailabilityForRevPAR = (cm.SellableUnitNights == 0)
		currencies = append(currencies, cur)
	}
	sort.Strings(currencies)

	outByCurrency := make([]domain.CurrencyRevenueMetrics, 0, len(currencies))
	for _, cur := range currencies {
		outByCurrency = append(outByCurrency, *byCurrency[cur])
	}

	dailyKeys := make([]string, 0, len(dailyByKey))
	for k, dp := range dailyByKey {
		dp.NetCollectedCashPence = dp.GrossCollectedPaymentsPence - dp.RefundedPence
		dailyKeys = append(dailyKeys, k)
	}
	sort.Strings(dailyKeys)
	outDaily := make([]domain.DailyRevenuePoint, 0, len(dailyKeys))
	for _, k := range dailyKeys {
		outDaily = append(outDaily, *dailyByKey[k])
	}

	comp, err := s.GetReportCompleteness(ctx, prop.ID, &completenessOverrides{
		manualUnquoted:         manualUnquotedCount,
		zeroValueAccepted:      zeroValueAcceptedCount,
		invHistoryIncomplete:   occ.Completeness.InventoryHistoryIncomplete,
		invHistoryReliableFrom: occ.Completeness.InventoryHistoryReliableFrom,
		currencyFilter:         currencyFilter,
		currencies:             currencies,
	})
	if err != nil {
		return domain.RevenueReport{}, err
	}

	return domain.RevenueReport{
		PropertyID:   prop.ID,
		PropertyName: prop.Name,
		Period: domain.ReportPeriod{
			StartDate: domain.FormatDate(startDate),
			EndDate:   domain.FormatDate(endDate),
			Days:      int(endDate.Sub(startDate).Hours() / 24),
			Timezone:  prop.Timezone,
		},
		ByCurrency:   outByCurrency,
		DailySeries:  outDaily,
		Completeness: comp,
		Definitions:  revenueMetricDefinitions(),
	}, nil
}

// GetChannelComparison compares performance across booking_channels and surfaces unmapped/conflict OTA imports.
func (s *Store) GetChannelComparison(ctx context.Context, prop domain.Property, startDate, endDate time.Time, currencyFilter string) (domain.ChannelComparisonReport, error) {
	bookings, err := s.fetchPeriodBookings(ctx, prop, startDate, endDate, currencyFilter)
	if err != nil {
		return domain.ChannelComparisonReport{}, err
	}
	payments, err := s.fetchPeriodPayments(ctx, prop, startDate, endDate, currencyFilter)
	if err != nil {
		return domain.ChannelComparisonReport{}, err
	}

	type chanKey struct {
		code     string
		currency string
	}
	type leadAccum struct {
		sumDays int64
		count   int64
	}
	rowsMap := map[chanKey]*domain.ChannelPerformanceRow{}
	leadsMap := map[chanKey]*leadAccum{}
	seenRes := map[uuid.UUID]bool{}
	totalAcceptedNights := map[string]int64{}

	ensureRow := func(code, name, cat, cur string) *domain.ChannelPerformanceRow {
		k := chanKey{code: code, currency: cur}
		r, ok := rowsMap[k]
		if !ok {
			if name == "" {
				name = code
			}
			if cat == "" {
				cat = "other"
			}
			r = &domain.ChannelPerformanceRow{
				ChannelCode: code,
				ChannelName: name,
				Category:    cat,
				Currency:    cur,
			}
			rowsMap[k] = r
			leadsMap[k] = &leadAccum{}
		}
		return r
	}

	tzLoc := loadPropertyLoc(prop.Timezone)
	for _, b := range bookings {
		stayNights := int(b.DepartureDate.Sub(b.ArrivalDate).Hours() / 24)
		if stayNights <= 0 {
			continue
		}
		unitCount := b.UnitCount
		if unitCount < 0 {
			unitCount = 0
		}
		var accNetTotal, grossTotal int64
		if b.HasQuote {
			accNetTotal = b.QuoteAccommodation + b.QuoteSurcharge - b.QuoteDiscount
			grossTotal = b.QuoteTotal
		} else {
			accNetTotal = b.ReservationTotalPence
			grossTotal = b.ReservationTotalPence
		}
		accSlices := ProrateIntegerPence(accNetTotal, stayNights)
		grossSlices := ProrateIntegerPence(grossTotal, stayNights)

		overlapNights := 0
		var overlapAccNet, overlapGross int64
		for idx := 0; idx < stayNights; idx++ {
			curDay := b.ArrivalDate.AddDate(0, 0, idx)
			if curDay.Before(startDate) || !curDay.Before(endDate) {
				continue
			}
			overlapNights++
			overlapAccNet += accSlices[idx]
			overlapGross += grossSlices[idx]
		}
		if overlapNights == 0 {
			continue
		}

		row := ensureRow(b.ChannelCode, b.ChannelName, b.ChannelCategory, b.Currency)
		k := chanKey{code: b.ChannelCode, currency: b.Currency}
		switch b.Status {
		case domain.ResStatusConfirmed, domain.ResStatusCheckedIn, domain.ResStatusCheckedOut:
			if !seenRes[b.ID] {
				seenRes[b.ID] = true
				row.AcceptedBookings++
				createdLocal := b.CreatedAt.In(tzLoc)
				createdDate := time.Date(createdLocal.Year(), createdLocal.Month(), createdLocal.Day(), 0, 0, 0, 0, time.UTC)
				leadDays := int64(b.ArrivalDate.Sub(createdDate).Hours() / 24)
				if leadDays < 0 {
					leadDays = 0
				}
				leadsMap[k].sumDays += leadDays
				leadsMap[k].count++
			}
			nightsAdd := int64(overlapNights) * unitCount
			row.OccupiedUnitNights += nightsAdd
			row.AccommodationNetPence += overlapAccNet
			row.GrossBookedTotalPence += overlapGross
			totalAcceptedNights[b.Currency] += nightsAdd
		case domain.ResStatusPending, domain.ResStatusInquiry:
			row.PendingPipelineCount++
		case domain.ResStatusCancelled, domain.ResStatusNoShow:
			row.CancelledOrNoShowCount++
		}
	}

	for _, p := range payments {
		chCode := p.ChannelCode
		if chCode == "" {
			chCode = "DIRECT_WEB"
		}
		row := ensureRow(chCode, chCode, "", p.Currency)
		row.GrossCollectedCashPence += p.AmountPence
	}

	// Overlay Phase 4 external reservation import diagnostics per channel code / provider.
	extRows, err := s.pool.Query(ctx, `
		SELECT
			UPPER(cer.provider_code) AS ch_code,
			COUNT(*) FILTER (WHERE cer.import_status IN ('IMPORTED', 'UPDATED'))::bigint AS mapped_cnt,
			COUNT(*) FILTER (WHERE cer.import_status = 'UNMAPPED')::bigint AS unmapped_cnt,
			COUNT(*) FILTER (WHERE cer.import_status = 'CONFLICT_OVERBOOKED')::bigint AS conflict_cnt
		FROM channel_external_reservations cer
		WHERE cer.property_id = $1
		GROUP BY UPPER(cer.provider_code)
	`, prop.ID)
	if err != nil {
		return domain.ChannelComparisonReport{}, MapErr(err)
	}
	defer extRows.Close()

	extByProvider := map[string][3]int64{}
	for extRows.Next() {
		var code string
		var mapped, unmapped, conflict int64
		if err := extRows.Scan(&code, &mapped, &unmapped, &conflict); err != nil {
			return domain.ChannelComparisonReport{}, MapErr(err)
		}
		extByProvider[code] = [3]int64{mapped, unmapped, conflict}
	}
	if err := extRows.Err(); err != nil {
		return domain.ChannelComparisonReport{}, MapErr(err)
	}

	var keys []chanKey
	var curList []string
	curSeen := map[string]bool{}
	for k, r := range rowsMap {
		keys = append(keys, k)
		if !curSeen[k.currency] {
			curSeen[k.currency] = true
			curList = append(curList, k.currency)
		}
		if stats, ok := extByProvider[strings.ToUpper(r.ChannelCode)]; ok {
			r.ExternalMappedCount = stats[0]
			r.ExternalUnmappedCount = stats[1]
			r.ExternalConflictCount = stats[2]
		}
		adr, adrFmt := domain.SafeDividePence(r.AccommodationNetPence, r.OccupiedUnitNights, r.Currency)
		r.ADRPence = adr
		r.ADRFormatted = adrFmt
		share, _ := domain.SafeBPS(r.OccupiedUnitNights, totalAcceptedNights[r.Currency])
		r.UnitNightShareBPS = share
		if la := leadsMap[k]; la != nil && la.count > 0 {
			avg := fmt.Sprintf("%.1f", float64(la.sumDays)/float64(la.count))
			r.AvgLeadTimeDays = &avg
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].currency != keys[j].currency {
			return keys[i].currency < keys[j].currency
		}
		ri := rowsMap[keys[i]]
		rj := rowsMap[keys[j]]
		if ri.GrossBookedTotalPence != rj.GrossBookedTotalPence {
			return ri.GrossBookedTotalPence > rj.GrossBookedTotalPence
		}
		return keys[i].code < keys[j].code
	})

	out := make([]domain.ChannelPerformanceRow, 0, len(keys))
	for _, k := range keys {
		out = append(out, *rowsMap[k])
	}
	sort.Strings(curList)
	comp, err := s.GetReportCompleteness(ctx, prop.ID, &completenessOverrides{currencies: curList})
	if err != nil {
		return domain.ChannelComparisonReport{}, err
	}

	return domain.ChannelComparisonReport{
		PropertyID:   prop.ID,
		PropertyName: prop.Name,
		Period: domain.ReportPeriod{
			StartDate: domain.FormatDate(startDate),
			EndDate:   domain.FormatDate(endDate),
			Days:      int(endDate.Sub(startDate).Hours() / 24),
			Timezone:  prop.Timezone,
		},
		Rows:         out,
		Completeness: comp,
		Definitions:  channelMetricDefinitions(),
	}, nil
}

// GetForecastReport provides strictly read-only forward projections separated cleanly into
// On-The-Books (CONFIRMED/CHECKED_IN + HOLD/PENDING pipeline) and a conservative trailing-90d weekday model.
func (s *Store) GetForecastReport(ctx context.Context, prop domain.Property, asOfDate time.Time, horizonDays int, currencyFilter string) (domain.ForecastReport, error) {
	windowEnd := asOfDate.AddDate(0, 0, horizonDays)
	histStart := asOfDate.AddDate(0, 0, -90)

	futureOcc, err := s.GetOccupancyReport(ctx, prop, asOfDate, windowEnd)
	if err != nil {
		return domain.ForecastReport{}, err
	}
	futureRev, err := s.GetRevenueReport(ctx, prop, asOfDate, windowEnd, currencyFilter)
	if err != nil {
		return domain.ForecastReport{}, err
	}
	histOcc, err := s.GetOccupancyReport(ctx, prop, histStart, asOfDate)
	if err != nil {
		return domain.ForecastReport{}, err
	}
	histRev, err := s.GetRevenueReport(ctx, prop, histStart, asOfDate, currencyFilter)
	if err != nil {
		return domain.ForecastReport{}, err
	}

	// Trailing occupancy ratio grouped by Go Weekday (0..6) for same-weekday pace blending.
	type dowStat struct {
		occ      int64
		sellable int64
	}
	dowMap := map[time.Weekday]*dowStat{}
	for _, dp := range histOcc.Daily {
		dt, err := domain.ParseDate(dp.Date)
		if err != nil {
			continue
		}
		st, ok := dowMap[dt.Weekday()]
		if !ok {
			st = &dowStat{}
			dowMap[dt.Weekday()] = st
		}
		st.occ += dp.OccupiedUnitNights
		st.sellable += dp.SellableUnitNights
	}

	var projectedAdditionalNights int64
	for _, fdp := range futureOcc.Daily {
		dt, err := domain.ParseDate(fdp.Date)
		if err != nil {
			continue
		}
		remainingSellable := fdp.SellableUnitNights - fdp.OccupiedUnitNights
		if remainingSellable <= 0 {
			continue
		}
		var targetOcc float64
		if st := dowMap[dt.Weekday()]; st != nil && st.sellable > 0 {
			targetOcc = float64(st.occ) / float64(st.sellable)
		} else if histOcc.Summary.SellableUnitNights > 0 {
			targetOcc = float64(histOcc.Summary.OccupiedUnitNights) / float64(histOcc.Summary.SellableUnitNights)
		}
		expectedDayOcc := int64(float64(fdp.SellableUnitNights)*targetOcc + 0.5)
		if expectedDayOcc > fdp.OccupiedUnitNights {
			add := expectedDayOcc - fdp.OccupiedUnitNights
			if add > remainingSellable {
				add = remainingSellable
			}
			projectedAdditionalNights += add
		}
	}

	otbNights := futureOcc.Summary.OccupiedUnitNights
	projTotalNights := otbNights + projectedAdditionalNights
	if projTotalNights > futureOcc.Summary.SellableUnitNights {
		projTotalNights = futureOcc.Summary.SellableUnitNights
	}
	lowAdd := (projectedAdditionalNights * 75) / 100
	highAdd := (projectedAdditionalNights*125 + 50) / 100
	confLow := otbNights + lowAdd
	confHigh := otbNights + highAdd
	if confHigh > futureOcc.Summary.SellableUnitNights {
		confHigh = futureOcc.Summary.SellableUnitNights
	}

	trailingOccBPS, _ := domain.SafeBPS(histOcc.Summary.OccupiedUnitNights, histOcc.Summary.SellableUnitNights)
	projOccBPS, _ := domain.SafeBPS(projTotalNights, futureOcc.Summary.SellableUnitNights)

	// Compute property-wide currency mix across trailing-90d + OTB accepted stay nights
	// so projectedAdditionalNights is conserved (sum across all currencies == projectedAdditionalNights)
	// and filtered single-currency queries receive only their proportional share.
	var futureRevAll, histRevAll domain.RevenueReport
	if currencyFilter == "" {
		futureRevAll = futureRev
		histRevAll = histRev
	} else {
		futureRevAll, err = s.GetRevenueReport(ctx, prop, asOfDate, windowEnd, "")
		if err != nil {
			return domain.ForecastReport{}, err
		}
		histRevAll, err = s.GetRevenueReport(ctx, prop, histStart, asOfDate, "")
		if err != nil {
			return domain.ForecastReport{}, err
		}
	}

	otbByCur := map[string]domain.CurrencyRevenueMetrics{}
	histByCur := map[string]domain.CurrencyRevenueMetrics{}
	curSet := map[string]bool{}
	weights := map[string]int64{}
	var totalWeight int64
	for _, r := range futureRevAll.ByCurrency {
		otbByCur[r.Currency] = r
		curSet[r.Currency] = true
		if r.OccupiedUnitNights > 0 {
			weights[r.Currency] += r.OccupiedUnitNights
			totalWeight += r.OccupiedUnitNights
		}
	}
	for _, h := range histRevAll.ByCurrency {
		histByCur[h.Currency] = h
		curSet[h.Currency] = true
		if h.OccupiedUnitNights > 0 {
			weights[h.Currency] += h.OccupiedUnitNights
			totalWeight += h.OccupiedUnitNights
		}
	}
	if len(curSet) == 0 && prop.Currency != "" && (currencyFilter == "" || currencyFilter == prop.Currency) {
		curSet[prop.Currency] = true
		weights[prop.Currency] = 0
	}
	var allCurrencies []string
	for c := range curSet {
		if _, ok := weights[c]; !ok {
			weights[c] = 0
		}
		allCurrencies = append(allCurrencies, c)
	}
	sort.Strings(allCurrencies)

	allocatedNights := domain.AllocateProjectedAdditionalNights(projectedAdditionalNights, weights)
	insufficientCurrencyMix := len(allCurrencies) > 1 && totalWeight == 0 && projectedAdditionalNights > 0

	var curProjections []domain.ForecastCurrencyProjection
	for _, c := range allCurrencies {
		if currencyFilter != "" && c != currencyFilter {
			continue
		}
		otb := otbByCur[c]
		hist := histByCur[c]
		var benchADR *int64
		if hist.ADRPence != nil {
			v := *hist.ADRPence
			benchADR = &v
		} else if otb.ADRPence != nil {
			v := *otb.ADRPence
			benchADR = &v
		}
		allocNights := allocatedNights[c]
		shareBPS, _ := domain.SafeBPS(weights[c], totalWeight)
		var projIncr int64
		if benchADR != nil {
			projIncr = allocNights * (*benchADR)
		}
		lowIncr := (projIncr * 75) / 100
		highIncr := (projIncr*125 + 50) / 100

		note := ""
		if insufficientCurrencyMix {
			note = "Projected additional revenue set to unavailable/0 because multiple currencies exist with zero historical or OTB stay-night weights."
		} else if len(allCurrencies) > 1 && projectedAdditionalNights > 0 {
			note = fmt.Sprintf("Allocated %d of %d projected property additional nights from trailing-90d + OTB accepted unit-night currency mix.", allocNights, projectedAdditionalNights)
		}

		curProjections = append(curProjections, domain.ForecastCurrencyProjection{
			Currency:                     c,
			OnTheBooksAccommodationPence: otb.AccommodationNetPence,
			OnTheBooksGrossTotalPence:    otb.GrossBookedTotalPence,
			ProvisionalPendingPence:      otb.ProvisionalPendingPence,
			TrailingBenchmarkADRPence:    benchADR,
			AllocatedAdditionalNights:    allocNights,
			NightShareBPS:                shareBPS,
			ProjectedIncrementalPence:    projIncr,
			ProjectedTotalPence:          otb.AccommodationNetPence + projIncr,
			ProjectedLowPence:            otb.AccommodationNetPence + lowIncr,
			ProjectedHighPence:           otb.AccommodationNetPence + highIncr,
			ProjectionNote:               note,
		})
	}

	comp := futureRev.Completeness
	if histOcc.Completeness.InventoryHistoryIncomplete && !comp.InventoryHistoryIncomplete {
		comp.InventoryHistoryIncomplete = true
		comp.InventoryHistoryReliableFrom = histOcc.Completeness.InventoryHistoryReliableFrom
		for _, w := range histOcc.Completeness.Warnings {
			if strings.Contains(w, "Historical inventory state") {
				comp.Warnings = append(comp.Warnings, w)
			}
		}
	}
	if insufficientCurrencyMix {
		comp.Warnings = append(comp.Warnings, fmt.Sprintf("%d projected additional unit-nights could not be allocated across multiple currencies (%s) due to insufficient stay-night currency weights.", projectedAdditionalNights, strings.Join(allCurrencies, ", ")))
	}

	return domain.ForecastReport{
		PropertyID:                 prop.ID,
		PropertyName:               prop.Name,
		AsOfDate:                   domain.FormatDate(asOfDate),
		HorizonDays:                horizonDays,
		Window:                     futureOcc.Period,
		ReadOnlyGuarantee:          "Strict read-only calculation; forecast execution performs zero mutations to inventory, rates, holds, or reservations.",
		Method:                     "OTB (On-The-Books Confirmed/Checked-In) + Trailing 90-Day Same-Weekday Occupancy & Benchmark ADR Pace Projection (75%-125% uncertainty band; multi-currency nights apportioned proportionally without duplication)",
		HistoricalBaselineWindow:   fmt.Sprintf("%s to %s (exclusive)", domain.FormatDate(histStart), domain.FormatDate(asOfDate)),
		HistoricalSampleUnitNights: histOcc.Summary.OccupiedUnitNights,
		TrailingOccupancyBPS:       trailingOccBPS,
		FutureSellableUnitNights:   futureOcc.Summary.SellableUnitNights,
		OnTheBooksUnitNights:       otbNights,
		PendingHoldUnitNights:      futureOcc.Summary.PendingHoldNights,
		ProjectedAdditionalNights:  projectedAdditionalNights,
		ProjectedTotalUnitNights:   projTotalNights,
		ProjectedOccupancyBPS:      projOccBPS,
		ConfidenceLowNights:        confLow,
		ConfidenceHighNights:       confHigh,
		Exclusions: []string{
			"Excludes INQUIRY, CANCELLED, and NO_SHOW reservations from On-The-Books.",
			"Excludes PENDING reservations and ACTIVE HOLD claims from OTB (surfaced separately as PendingHoldUnitNights).",
			"Excludes MAINTENANCE, SEASONAL_CLOSURE, WEATHER_CLOSURE, OWNER_USE, and ADMINISTRATIVE_BLOCK nights from future sellable inventory.",
		},
		ByCurrency:   curProjections,
		Completeness: comp,
		Definitions:  forecastMetricDefinitions(),
	}, nil
}

// GetGuestRetentionReport calculates strictly aggregated returning-guest counts across canonical
// merged guest identities (`merged_into_guest_id` recursive resolution), returning ZERO guest PII or IDs.
func (s *Store) GetGuestRetentionReport(ctx context.Context, prop domain.Property, startDate, endDate time.Time) (domain.GuestRetentionReport, error) {
	startStr := domain.FormatDate(startDate)
	endStr := domain.FormatDate(endDate)

	var completedCount, distinctCanonical, singleStay, returningGuests, returningStays, anonStays, unlinkedStays, aliasResolved int64
	err := s.pool.QueryRow(ctx, `
		WITH RECURSIVE guest_lineage AS (
			-- Base: root canonical guests (not merged into anyone else)
			SELECT id AS guest_id, id AS canonical_id, ARRAY[id] AS path, (status = 'ANONYMIZED' OR anonymized_at IS NOT NULL) AS canonical_anonymized
			FROM guests
			WHERE property_id = $1 AND merged_into_guest_id IS NULL
			UNION ALL
			-- Recursive: follow child guests merged into canonical tree, cycle-guarded
			SELECT child.id, gl.canonical_id, gl.path || child.id, gl.canonical_anonymized
			FROM guests child
			JOIN guest_lineage gl ON child.merged_into_guest_id = gl.guest_id
			WHERE child.property_id = $1
			  AND NOT (child.id = ANY(gl.path))
		),
		completed_period AS (
			SELECT
				r.id AS reservation_id,
				r.guest_id,
				COALESCE(gl.canonical_id, r.guest_id) AS resolved_guest_id,
				COALESCE(gl.canonical_anonymized, FALSE) AS is_anonymized
			FROM reservations r
			LEFT JOIN guest_lineage gl ON gl.guest_id = r.guest_id
			WHERE r.property_id = $1
			  AND r.status = 'CHECKED_OUT'
			  AND r.stay_range && daterange($2::date, $3::date, '[)')
		),
		cumulative_counts AS (
			SELECT
				COALESCE(gl.canonical_id, r.guest_id) AS resolved_guest_id,
				COUNT(DISTINCT r.id)::bigint AS total_completed_stays_to_date
			FROM reservations r
			LEFT JOIN guest_lineage gl ON gl.guest_id = r.guest_id
			WHERE r.property_id = $1
			  AND r.status = 'CHECKED_OUT'
			  AND r.guest_id IS NOT NULL
			  AND upper(r.stay_range) <= $3::date
			GROUP BY COALESCE(gl.canonical_id, r.guest_id)
		),
		linked_period_guests AS (
			SELECT
				cp.resolved_guest_id,
				BOOL_OR(cp.is_anonymized) AS is_anonymized,
				COUNT(DISTINCT cp.reservation_id)::bigint AS stays_in_period,
				GREATEST(MAX(COALESCE(cc.total_completed_stays_to_date, 0)), COUNT(DISTINCT cp.reservation_id)::bigint) AS cumulative_stays
			FROM completed_period cp
			LEFT JOIN cumulative_counts cc ON cc.resolved_guest_id = cp.resolved_guest_id
			WHERE cp.guest_id IS NOT NULL
			GROUP BY cp.resolved_guest_id
		)
		SELECT
			(SELECT COUNT(*) FROM completed_period)::bigint AS completed_stays_count,
			(SELECT COUNT(*) FROM linked_period_guests WHERE NOT is_anonymized)::bigint AS distinct_canonical_guests_count,
			(SELECT COUNT(*) FROM linked_period_guests WHERE NOT is_anonymized AND cumulative_stays = 1)::bigint AS single_stay_guests_count,
			(SELECT COUNT(*) FROM linked_period_guests WHERE NOT is_anonymized AND cumulative_stays >= 2)::bigint AS returning_guests_count,
			(SELECT COALESCE(SUM(stays_in_period) FILTER (WHERE NOT is_anonymized AND cumulative_stays >= 2), 0) FROM linked_period_guests)::bigint AS returning_guest_stays_count,
			(SELECT COALESCE(SUM(stays_in_period) FILTER (WHERE is_anonymized), 0) FROM linked_period_guests)::bigint AS anonymized_guest_stays_count,
			(SELECT COUNT(*) FROM completed_period WHERE guest_id IS NULL)::bigint AS unlinked_completed_stays_count,
			(SELECT COUNT(*) FROM completed_period WHERE guest_id IS NOT NULL AND resolved_guest_id <> guest_id)::bigint AS merged_alias_stays_resolved
	`, prop.ID, startStr, endStr).Scan(
		&completedCount,
		&distinctCanonical,
		&singleStay,
		&returningGuests,
		&returningStays,
		&anonStays,
		&unlinkedStays,
		&aliasResolved,
	)
	if err != nil {
		return domain.GuestRetentionReport{}, MapErr(err)
	}

	repeatRateBPS, repeatRatePct := domain.SafeBPS(returningGuests, distinctCanonical)
	avgStays := "0.00"
	linkedNonAnonStays := completedCount - anonStays - unlinkedStays
	if distinctCanonical > 0 {
		avgStays = fmt.Sprintf("%.2f", float64(linkedNonAnonStays)/float64(distinctCanonical))
	}

	return domain.GuestRetentionReport{
		PropertyID:   prop.ID,
		PropertyName: prop.Name,
		Period: domain.ReportPeriod{
			StartDate: startStr,
			EndDate:   endStr,
			Days:      int(endDate.Sub(startDate).Hours() / 24),
			Timezone:  prop.Timezone,
		},
		PrivacyNotice:                "Aggregate counts only. Zero guest names, emails, phones, notes, consent details, or guest_id identifiers are returned; ANONYMIZED stays are segregated into anonymized_guest_stays_count.",
		CompletedStaysCount:          completedCount,
		DistinctCanonicalGuestsCount: distinctCanonical,
		SingleStayGuestsCount:        singleStay,
		ReturningGuestsCount:         returningGuests,
		ReturningGuestStaysCount:     returningStays,
		AnonymizedGuestStaysCount:    anonStays,
		UnlinkedCompletedStaysCount:  unlinkedStays,
		MergedAliasStaysResolved:     aliasResolved,
		RepeatGuestRateBPS:           repeatRateBPS,
		RepeatGuestRatePct:           repeatRatePct,
		AvgStaysPerGuest:             avgStays,
		Definitions:                  retentionMetricDefinitions(),
	}, nil
}

// GetAccountingExport produces repeatable, deterministic, PII-free accounting feed rows.
func (s *Store) GetAccountingExport(
	ctx context.Context,
	prop domain.Property,
	exportType domain.ReportExportType,
	startDate, endDate time.Time,
	currencyFilter string,
) (domain.AccountingExportResult, error) {
	result := domain.AccountingExportResult{
		SchemaVersion:     domain.AccountingExportSchemaVer,
		PropertyID:        prop.ID,
		PropertyName:      prop.Name,
		ExportType:        exportType,
		CurrencyFilter:    currencyFilter,
		UnsupportedFields: domain.AccountingUnsupportedFields(),
		Period: domain.ReportPeriod{
			StartDate: domain.FormatDate(startDate),
			EndDate:   domain.FormatDate(endDate),
			Days:      int(endDate.Sub(startDate).Hours() / 24),
			Timezone:  prop.Timezone,
		},
	}

	switch exportType {
	case domain.ReportExportBookingLineItems:
		result.Headers = []string{
			"schema_version", "export_type", "property_code", "property_timezone",
			"reservation_id", "reservation_reference", "booking_status", "payment_status",
			"channel_code", "channel_category", "unit_type_code", "arrival_date",
			"departure_date", "nights", "unit_nights", "currency", "quote_source",
			"accommodation_pence", "occupancy_surcharge_pence", "extras_pence", "discount_pence",
			"tax_pence", "gross_total_pence", "amount_paid_pence", "balance_due_pence",
			"ota_commission_pence", "payout_settlement_date",
		}
		bookings, err := s.fetchPeriodBookings(ctx, prop, startDate, endDate, currencyFilter)
		if err != nil {
			return domain.AccountingExportResult{}, err
		}
		for _, b := range bookings {
			if len(result.Rows) >= domain.MaxAccountingExportRows {
				result.Truncated = true
				break
			}
			nights := int(b.DepartureDate.Sub(b.ArrivalDate).Hours() / 24)
			uc := b.UnitCount
			if uc <= 0 {
				uc = 1
			}
			quoteSource := "manual_reservation_total"
			acc, sur, ext, disc, tax, gross := b.ReservationTotalPence, int64(0), int64(0), int64(0), int64(0), b.ReservationTotalPence
			if b.HasQuote {
				quoteSource = "frozen_booking_quote"
				acc = b.QuoteAccommodation
				sur = b.QuoteSurcharge
				ext = b.QuoteExtras
				disc = b.QuoteDiscount
				tax = b.QuoteTax
				gross = b.QuoteTotal
			}
			result.Rows = append(result.Rows, map[string]string{
				"schema_version":            domain.AccountingExportSchemaVer,
				"export_type":               string(exportType),
				"property_code":             prop.Slug,
				"property_timezone":         prop.Timezone,
				"reservation_id":            b.ID.String(),
				"reservation_reference":     b.Reference,
				"booking_status":            string(b.Status),
				"payment_status":            b.PaymentStatus,
				"channel_code":              b.ChannelCode,
				"channel_category":          b.ChannelCategory,
				"unit_type_code":            b.UnitTypeCode,
				"arrival_date":              domain.FormatDate(b.ArrivalDate),
				"departure_date":            domain.FormatDate(b.DepartureDate),
				"nights":                    fmt.Sprintf("%d", nights),
				"unit_nights":               fmt.Sprintf("%d", int64(nights)*uc),
				"currency":                  b.Currency,
				"quote_source":              quoteSource,
				"accommodation_pence":       fmt.Sprintf("%d", acc),
				"occupancy_surcharge_pence": fmt.Sprintf("%d", sur),
				"extras_pence":              fmt.Sprintf("%d", ext),
				"discount_pence":            fmt.Sprintf("%d", disc),
				"tax_pence":                 fmt.Sprintf("%d", tax),
				"gross_total_pence":         fmt.Sprintf("%d", gross),
				"amount_paid_pence":         fmt.Sprintf("%d", b.ReservationPaidPence),
				"balance_due_pence":         fmt.Sprintf("%d", b.ReservationBalDuePence),
				"ota_commission_pence":      "NOT IMPLEMENTED",
				"payout_settlement_date":    "NOT IMPLEMENTED",
			})
		}

	case domain.ReportExportPaymentLedger:
		result.Headers = []string{
			"schema_version", "export_type", "property_code", "property_timezone",
			"payment_id", "paid_local_date", "paid_at_utc", "reservation_reference",
			"channel_code", "provider", "payment_type", "payment_status",
			"currency", "amount_pence", "provider_payment_intent_id",
			"payout_settlement_date", "net_of_fee_settlement_pence",
		}
		rows, err := s.pool.Query(ctx, `
			SELECT
				p.id,
				(p.paid_at AT TIME ZONE $4)::date AS paid_local_date,
				p.paid_at,
				r.reference,
				r.channel_code,
				p.provider,
				p.payment_type,
				p.status,
				p.currency,
				p.amount_pence,
				p.provider_payment_intent_id
			FROM payments p
			JOIN reservations r ON r.id = p.reservation_id
			WHERE p.property_id = $1
			  AND (p.paid_at AT TIME ZONE $4)::date >= $2::date
			  AND (p.paid_at AT TIME ZONE $4)::date < $3::date
			  AND ($5 = '' OR p.currency = $5)
			ORDER BY p.paid_at ASC, r.reference ASC, p.id ASC
			LIMIT $6
		`, prop.ID, domain.FormatDate(startDate), domain.FormatDate(endDate), prop.Timezone, currencyFilter, domain.MaxAccountingExportRows+1)
		if err != nil {
			return domain.AccountingExportResult{}, MapErr(err)
		}
		defer rows.Close()
		for rows.Next() {
			if len(result.Rows) >= domain.MaxAccountingExportRows {
				result.Truncated = true
				break
			}
			var pid uuid.UUID
			var localDt, paidUTC time.Time
			var ref, chCode, provider, payType, payStatus, cur, piID string
			var amt int64
			if err := rows.Scan(&pid, &localDt, &paidUTC, &ref, &chCode, &provider, &payType, &payStatus, &cur, &amt, &piID); err != nil {
				return domain.AccountingExportResult{}, MapErr(err)
			}
			result.Rows = append(result.Rows, map[string]string{
				"schema_version":              domain.AccountingExportSchemaVer,
				"export_type":                 string(exportType),
				"property_code":               prop.Slug,
				"property_timezone":           prop.Timezone,
				"payment_id":                  pid.String(),
				"paid_local_date":             domain.FormatDate(localDt),
				"paid_at_utc":                 paidUTC.UTC().Format(time.RFC3339),
				"reservation_reference":       ref,
				"channel_code":                chCode,
				"provider":                    provider,
				"payment_type":                payType,
				"payment_status":              payStatus,
				"currency":                    cur,
				"amount_pence":                fmt.Sprintf("%d", amt),
				"provider_payment_intent_id":  piID,
				"payout_settlement_date":      "NOT IMPLEMENTED",
				"net_of_fee_settlement_pence": "NOT IMPLEMENTED",
			})
		}
		if err := rows.Err(); err != nil {
			return domain.AccountingExportResult{}, MapErr(err)
		}

	case domain.ReportExportRefundLedger:
		result.Headers = []string{
			"schema_version", "export_type", "property_code", "property_timezone",
			"refund_id", "refund_local_date", "refund_created_at_utc", "reservation_reference",
			"channel_code", "provider", "refund_status", "reason",
			"currency", "amount_pence", "source",
		}
		rows, err := s.pool.Query(ctx, `
			SELECT
				rf.id,
				(COALESCE(rf.occurred_at, rf.created_at) AT TIME ZONE $4)::date AS refund_local_date,
				COALESCE(rf.occurred_at, rf.created_at) AS occurred_utc,
				COALESCE(r.reference, '') AS reservation_reference,
				COALESCE(r.channel_code, '') AS channel_code,
				rf.provider,
				rf.status,
				rf.reason,
				rf.currency,
				rf.amount_pence,
				'refunds_table' AS source
			FROM refunds rf
			LEFT JOIN reservations r ON r.id = rf.reservation_id
			WHERE rf.property_id = $1
			  AND (COALESCE(rf.occurred_at, rf.created_at) AT TIME ZONE $4)::date >= $2::date
			  AND (COALESCE(rf.occurred_at, rf.created_at) AT TIME ZONE $4)::date < $3::date
			  AND ($5 = '' OR rf.currency = $5)
			ORDER BY 3 ASC, 4 ASC, 1 ASC
			LIMIT $6
		`, prop.ID, domain.FormatDate(startDate), domain.FormatDate(endDate), prop.Timezone, currencyFilter, domain.MaxAccountingExportRows+1)
		if err != nil {
			return domain.AccountingExportResult{}, MapErr(err)
		}
		defer rows.Close()
		for rows.Next() {
			if len(result.Rows) >= domain.MaxAccountingExportRows {
				result.Truncated = true
				break
			}
			var rfID uuid.UUID
			var localDt, createdUTC time.Time
			var ref, chCode, provider, rfStatus, reason, cur, src string
			var amt int64
			if err := rows.Scan(&rfID, &localDt, &createdUTC, &ref, &chCode, &provider, &rfStatus, &reason, &cur, &amt, &src); err != nil {
				return domain.AccountingExportResult{}, MapErr(err)
			}
			result.Rows = append(result.Rows, map[string]string{
				"schema_version":        domain.AccountingExportSchemaVer,
				"export_type":           string(exportType),
				"property_code":         prop.Slug,
				"property_timezone":     prop.Timezone,
				"refund_id":             rfID.String(),
				"refund_local_date":     domain.FormatDate(localDt),
				"refund_created_at_utc": createdUTC.UTC().Format(time.RFC3339),
				"reservation_reference": ref,
				"channel_code":          chCode,
				"provider":              provider,
				"refund_status":         rfStatus,
				"reason":                reason,
				"currency":              cur,
				"amount_pence":          fmt.Sprintf("%d", amt),
				"source":                src,
			})
		}
		if err := rows.Err(); err != nil {
			return domain.AccountingExportResult{}, MapErr(err)
		}
		comp, cErr := s.GetReportCompleteness(ctx, prop.ID, &completenessOverrides{currencyFilter: currencyFilter})
		if cErr != nil {
			return domain.AccountingExportResult{}, cErr
		}
		result.UnresolvedLegacyRefundPaymentsCount = comp.UnresolvedLegacyRefundPaymentsCount
		if comp.UnresolvedLegacyRefundPaymentsCount > 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%d REFUNDED/PARTIALLY_REFUNDED payment(s) lack a persisted refunds row; excluded from dated refund_ledger totals rather than backdating onto payment paid_at.", comp.UnresolvedLegacyRefundPaymentsCount))
		}
	}

	result.RowCount = len(result.Rows)
	return result, nil
}

type completenessOverrides struct {
	manualUnquoted         int64
	zeroValueAccepted      int64
	invHistoryIncomplete   bool
	invHistoryReliableFrom *string
	currencyFilter         string
	currencies             []string
}

func (s *Store) GetReportCompleteness(ctx context.Context, propertyID uuid.UUID, ov *completenessOverrides) (domain.ReportDataCompleteness, error) {
	var comp domain.ReportDataCompleteness
	curFilter := ""
	if ov != nil {
		curFilter = ov.currencyFilter
	}
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM channel_external_reservations WHERE property_id = $1 AND import_status = 'UNMAPPED')::bigint,
			(SELECT COUNT(*) FROM channel_external_reservations WHERE property_id = $1 AND import_status = 'CONFLICT_OVERBOOKED')::bigint,
			(SELECT COUNT(*) FROM channel_external_reservations WHERE property_id = $1 AND import_status = 'FAILED')::bigint,
			(SELECT COUNT(*) FROM channel_sync_jobs WHERE property_id = $1 AND status IN ('PENDING', 'RUNNING', 'FAILED_RETRYABLE'))::bigint,
			(SELECT COUNT(*) FROM payment_exceptions WHERE property_id = $1 AND status = 'OPEN')::bigint,
			(SELECT COUNT(*) FROM payments p WHERE p.property_id = $1 AND p.status = 'PARTIALLY_REFUNDED' AND ($2 = '' OR p.currency = $2) AND NOT EXISTS (SELECT 1 FROM refunds rf WHERE rf.payment_attempt_id = p.payment_attempt_id AND rf.status = 'SUCCEEDED'))::bigint,
			(SELECT COUNT(*) FROM payments p WHERE p.property_id = $1 AND p.status IN ('REFUNDED', 'PARTIALLY_REFUNDED') AND ($2 = '' OR p.currency = $2) AND NOT EXISTS (SELECT 1 FROM refunds rf WHERE rf.payment_attempt_id = p.payment_attempt_id AND rf.status = 'SUCCEEDED'))::bigint
	`, propertyID, curFilter).Scan(
		&comp.UnmappedExternalReservations,
		&comp.ConflictExternalReservations,
		&comp.FailedExternalReservations,
		&comp.PendingChannelSyncJobs,
		&comp.OpenPaymentExceptions,
		&comp.UnresolvedPartialRefundCount,
		&comp.UnresolvedLegacyRefundPaymentsCount,
	)
	if err != nil {
		return domain.ReportDataCompleteness{}, MapErr(err)
	}
	if ov != nil {
		comp.ManualUnquotedBookingsCount = ov.manualUnquoted
		comp.ZeroValueAcceptedStaysCount = ov.zeroValueAccepted
		comp.InventoryHistoryIncomplete = ov.invHistoryIncomplete
		comp.InventoryHistoryReliableFrom = ov.invHistoryReliableFrom
		comp.Currencies = append([]string(nil), ov.currencies...)
		comp.MultiCurrencyDetected = len(ov.currencies) > 1
	}
	if comp.UnmappedExternalReservations > 0 {
		comp.Warnings = append(comp.Warnings, fmt.Sprintf("%d external OTA reservation(s) are UNMAPPED and excluded from unit-level inventory until mapped.", comp.UnmappedExternalReservations))
	}
	if comp.ConflictExternalReservations > 0 {
		comp.Warnings = append(comp.Warnings, fmt.Sprintf("%d external OTA reservation(s) have CONFLICT_OVERBOOKED status awaiting manager resolution.", comp.ConflictExternalReservations))
	}
	if comp.FailedExternalReservations > 0 {
		comp.Warnings = append(comp.Warnings, fmt.Sprintf("%d external OTA reservation import(s) failed; channel totals may undercount.", comp.FailedExternalReservations))
	}
	if comp.OpenPaymentExceptions > 0 {
		comp.Warnings = append(comp.Warnings, fmt.Sprintf("%d OPEN payment exception(s) require financial review.", comp.OpenPaymentExceptions))
	}
	if comp.ManualUnquotedBookingsCount > 0 {
		comp.Warnings = append(comp.Warnings, fmt.Sprintf("%d accepted reservation(s) have no frozen booking quote and use reservation.total_pence fallback.", comp.ManualUnquotedBookingsCount))
	}
	if comp.ZeroValueAcceptedStaysCount > 0 {
		comp.Warnings = append(comp.Warnings, fmt.Sprintf("%d accepted reservation(s) carry total_pence = 0; ADR/RevPAR reflect only recorded monetary values.", comp.ZeroValueAcceptedStaysCount))
	}
	if comp.UnresolvedLegacyRefundPaymentsCount > 0 {
		comp.Warnings = append(comp.Warnings, fmt.Sprintf("%d REFUNDED/PARTIALLY_REFUNDED payment(s) lack a persisted refunds row; refund date and/or amount cannot be established reliably and are excluded from dated refund totals rather than backdated onto payment paid_at.", comp.UnresolvedLegacyRefundPaymentsCount))
	}
	if comp.InventoryHistoryIncomplete {
		sinceLabel := "migration tracking activation"
		if comp.InventoryHistoryReliableFrom != nil {
			sinceLabel = *comp.InventoryHistoryReliableFrom
		}
		comp.Warnings = append(comp.Warnings, fmt.Sprintf("Historical inventory state is incomplete prior to %s for units with unrecorded pre-0021 status/archive changes; pre-boundary capacity is not guessed.", sinceLabel))
	}
	if comp.MultiCurrencyDetected {
		comp.Warnings = append(comp.Warnings, fmt.Sprintf("Multiple currencies detected (%s); totals are reported strictly per currency and never combined.", strings.Join(comp.Currencies, ", ")))
	}
	return comp, nil
}

func (s *Store) fetchPeriodBookings(ctx context.Context, prop domain.Property, startDate, endDate time.Time, currencyFilter string) ([]reportBookingRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			r.id,
			r.reference,
			r.channel_code,
			COALESCE(bc.name, r.channel_code) AS channel_name,
			COALESCE(bc.category, 'other') AS channel_category,
			r.status,
			r.payment_status,
			lower(r.stay_range) AS arrival_date,
			upper(r.stay_range) AS departure_date,
			COALESCE(ru_agg.unit_count, 0)::bigint AS unit_count,
			COALESCE(ru_agg.unit_type_code, '') AS unit_type_code,
			COALESCE(NULLIF(q.currency, ''), r.currency, $4) AS effective_currency,
			r.total_pence,
			r.amount_paid_pence,
			r.balance_due_pence,
			r.created_at,
			(q.id IS NOT NULL) AS has_quote,
			COALESCE(q.accommodation_subtotal_pence, 0),
			COALESCE(q.occupancy_surcharge_pence, 0),
			COALESCE(q.extras_subtotal_pence, 0),
			COALESCE(q.discount_pence, 0),
			COALESCE(q.tax_pence, 0),
			COALESCE(q.total_pence, 0)
		FROM reservations r
		LEFT JOIN booking_channels bc
			ON bc.property_id = r.property_id AND bc.code = r.channel_code
		LEFT JOIN LATERAL (
			SELECT
				COUNT(*)::bigint AS unit_count,
				MIN(ut.code) AS unit_type_code
			FROM reservation_units ru
			JOIN units u ON u.id = ru.unit_id
			JOIN unit_types ut ON ut.id = u.unit_type_id
			WHERE ru.reservation_id = r.id
		) ru_agg ON TRUE
		LEFT JOIN LATERAL (
			SELECT
				bq.id, bq.currency, bq.accommodation_subtotal_pence, bq.occupancy_surcharge_pence,
				bq.extras_subtotal_pence, bq.discount_pence, bq.tax_pence, bq.total_pence
			FROM booking_quotes bq
			WHERE bq.property_id = r.property_id
			  AND bq.reservation_id = r.id
			ORDER BY bq.created_at DESC
			LIMIT 1
		) q ON TRUE
		WHERE r.property_id = $1
		  AND r.stay_range && daterange($2::date, $3::date, '[)')
		  AND ($5 = '' OR COALESCE(NULLIF(q.currency, ''), r.currency, $4) = $5)
		ORDER BY lower(r.stay_range) ASC, r.reference ASC
	`, prop.ID, domain.FormatDate(startDate), domain.FormatDate(endDate), prop.Currency, currencyFilter)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []reportBookingRow
	for rows.Next() {
		var r reportBookingRow
		if err := rows.Scan(
			&r.ID, &r.Reference, &r.ChannelCode, &r.ChannelName, &r.ChannelCategory,
			&r.Status, &r.PaymentStatus, &r.ArrivalDate, &r.DepartureDate,
			&r.UnitCount, &r.UnitTypeCode, &r.Currency, &r.ReservationTotalPence,
			&r.ReservationPaidPence, &r.ReservationBalDuePence, &r.CreatedAt,
			&r.HasQuote, &r.QuoteAccommodation, &r.QuoteSurcharge, &r.QuoteExtras,
			&r.QuoteDiscount, &r.QuoteTax, &r.QuoteTotal,
		); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, r)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) fetchPeriodPayments(ctx context.Context, prop domain.Property, startDate, endDate time.Time, currencyFilter string) ([]reportCashRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			(p.paid_at AT TIME ZONE $4)::date AS local_date,
			p.currency,
			r.channel_code,
			COALESCE(SUM(p.amount_pence), 0)::bigint AS amount_pence
		FROM payments p
		JOIN reservations r ON r.id = p.reservation_id
		WHERE p.property_id = $1
		  AND p.status IN ('SUCCEEDED', 'PARTIALLY_REFUNDED', 'REFUNDED')
		  AND (p.paid_at AT TIME ZONE $4)::date >= $2::date
		  AND (p.paid_at AT TIME ZONE $4)::date < $3::date
		  AND ($5 = '' OR p.currency = $5)
		GROUP BY 1, 2, 3
		ORDER BY 1 ASC, 2 ASC, 3 ASC
	`, prop.ID, domain.FormatDate(startDate), domain.FormatDate(endDate), prop.Timezone, currencyFilter)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []reportCashRow
	for rows.Next() {
		var r reportCashRow
		if err := rows.Scan(&r.LocalDate, &r.Currency, &r.ChannelCode, &r.AmountPence); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, r)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) fetchPeriodRefunds(ctx context.Context, prop domain.Property, startDate, endDate time.Time, currencyFilter string) ([]reportCashRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			(COALESCE(rf.occurred_at, rf.created_at) AT TIME ZONE $4)::date AS local_date,
			rf.currency,
			COALESCE(r.channel_code, '') AS channel_code,
			COALESCE(SUM(rf.amount_pence), 0)::bigint AS amount_pence
		FROM refunds rf
		LEFT JOIN reservations r ON r.id = rf.reservation_id
		WHERE rf.property_id = $1
		  AND rf.status = 'SUCCEEDED'
		  AND (COALESCE(rf.occurred_at, rf.created_at) AT TIME ZONE $4)::date >= $2::date
		  AND (COALESCE(rf.occurred_at, rf.created_at) AT TIME ZONE $4)::date < $3::date
		  AND ($5 = '' OR rf.currency = $5)
		GROUP BY 1, 2, 3
	`, prop.ID, domain.FormatDate(startDate), domain.FormatDate(endDate), prop.Timezone, currencyFilter)
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []reportCashRow
	for rows.Next() {
		var r reportCashRow
		if err := rows.Scan(&r.LocalDate, &r.Currency, &r.ChannelCode, &r.AmountPence); err != nil {
			return nil, MapErr(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, MapErr(err)
	}
	return out, nil
}

func buildOccupancyMetrics(phys, ooo, ownerAdm, sellable, occ, pend int64) domain.OccupancyMetrics {
	bps, pct := domain.SafeBPS(occ, sellable)
	return domain.OccupancyMetrics{
		PhysicalUnitNights:    phys,
		OutOfOrderNights:      ooo,
		OwnerAdminBlockNights: ownerAdm,
		SellableUnitNights:    sellable,
		OccupiedUnitNights:    occ,
		PendingHoldNights:     pend,
		OccupancyRateBPS:      bps,
		OccupancyRatePct:      pct,
		ZeroAvailability:      sellable == 0,
	}
}

func loadPropertyLoc(tzName string) *time.Location {
	if loc, err := time.LoadLocation(tzName); err == nil {
		return loc
	}
	return time.UTC
}

func occupancyMetricDefinitions() []domain.ReportMetricDefinition {
	return []domain.ReportMetricDefinition{
		{
			Metric:     "Sellable Unit-Nights",
			Formula:    "Effective-dated active + maintenance physical unit-nights from unit_status_history - (Out-of-Order [maintenance unit status, MAINTENANCE, SEASONAL_CLOSURE, WEATHER_CLOSURE] + Owner/Admin [OWNER_USE, ADMINISTRATIVE_BLOCK, OPERATIONAL_BUFFER])",
			Exclusions: "Units not yet commissioned, archived/inactive/retired on the target date, or pre-0021 units with incomplete transition history are excluded without guessing.",
			TimeBasis:  "Property calendar night date [start_date, end_date)",
		},
		{
			Metric:     "Occupied Unit-Nights & Occupancy %",
			Formula:    "Accepted reservation_units nights (CONFIRMED, CHECKED_IN, CHECKED_OUT) / Sellable Unit-Nights",
			Exclusions: "INQUIRY, PENDING, CANCELLED, NO_SHOW, and unconfirmed HOLD claims are excluded (PENDING/HOLD surfaced separately).",
			TimeBasis:  "Property calendar night date [start_date, end_date)",
		},
	}
}

func revenueMetricDefinitions() []domain.ReportMetricDefinition {
	return []domain.ReportMetricDefinition{
		{
			Metric:     "Booked Accommodation, Extras, Tax & ADR / RevPAR",
			Formula:    "Frozen booking_quotes (or fallback reservations.total_pence) prorated deterministically across stay_range nights; ADR = AccommodationNet / OccupiedUnitNights; RevPAR = AccommodationNet / SellableUnitNights",
			Exclusions: "Unconfirmed booking_quotes, expired holds, INQUIRY, CANCELLED, NO_SHOW excluded from realized revenue; mixed currencies never summed.",
			TimeBasis:  "Stay night calendar date [start_date, end_date)",
		},
		{
			Metric:     "Collected Cash & Refunds",
			Formula:    "payments (SUCCEEDED, PARTIALLY_REFUNDED, REFUNDED) minus persisted refunds (SUCCEEDED) attributed on refunds.occurred_at",
			Exclusions: "payment_attempts without settled payment rows are never counted; legacy refund-status payments lacking a persisted refunds row are excluded from dated refund totals and surfaced in completeness warnings rather than backdated to payments.paid_at.",
			TimeBasis:  "Payment paid_at / refund occurred_at converted to property local timezone DATE",
		},
	}
}

func channelMetricDefinitions() []domain.ReportMetricDefinition {
	return []domain.ReportMetricDefinition{
		{
			Metric:     "Channel Attribution & External Completeness",
			Formula:    "Accepted stay-nights, prorated booked value, ADR, and avg booking lead time grouped by booking_channels.code + currency",
			Exclusions: "OTA commission fees & bank settlement payouts are NOT IMPLEMENTED; UNMAPPED/CONFLICT external reservations surfaced separately.",
			TimeBasis:  "Stay night calendar date [start_date, end_date); lead_time uses arrival_date - local created_at date",
		},
	}
}

func forecastMetricDefinitions() []domain.ReportMetricDefinition {
	return []domain.ReportMetricDefinition{
		{
			Metric:     "On-The-Books (OTB) + Trailing Benchmark Projection",
			Formula:    "Confirmed/Checked-In future unit-nights + modeled incremental nights from trailing 90d same-weekday occupancy & ADR (75%-125% bounds)",
			Exclusions: "Read-only model; never mutates inventory or rates. Provisional PENDING/HOLD reported separately.",
			TimeBasis:  "Forward calendar window [as_of_date, as_of_date + horizon_days)",
		},
	}
}

func retentionMetricDefinitions() []domain.ReportMetricDefinition {
	return []domain.ReportMetricDefinition{
		{
			Metric:     "Canonical Repeat Guest Rate",
			Formula:    "Guests with CHECKED_OUT stay in period and >= 2 cumulative CHECKED_OUT stays via merged_into_guest_id recursive lineage / distinct canonical non-anonymized period guests",
			Exclusions: "No PII or guest_id exposed; ANONYMIZED stays counted solely in anonymized_guest_stays_count; cancelled/no-show/pending excluded.",
			TimeBasis:  "Completed CHECKED_OUT stays overlapping [start_date, end_date)",
		},
	}
}
