package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/auth"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

// GetOperationsCentre assembles the live Operations Centre dashboard, arrivals/departures,
// daily occupancy snapshot, attention items, and 5-stage changeover board for opDate.
func (s *Service) GetOperationsCentre(
	ctx context.Context,
	actor Actor,
	propertyID uuid.UUID,
	opDate *time.Time,
	focusRaw string,
) (domain.OperationsCentreData, error) {
	if err := s.requirePerm(actor, auth.PermOpsRead); err != nil {
		return domain.OperationsCentreData{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.OperationsCentreData{}, err
	}

	prop, err := s.store.GetPropertyByID(ctx, nil, propertyID)
	if err != nil {
		return domain.OperationsCentreData{}, err
	}
	loc, err := time.LoadLocation(strings.TrimSpace(prop.Timezone))
	if err != nil {
		loc = time.UTC
	}
	nowLocal := s.nowUTC().In(loc)
	todayDate := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, time.UTC)

	selectedDate := todayDate
	if opDate != nil && !opDate.IsZero() {
		selectedDate = time.Date(opDate.Year(), opDate.Month(), opDate.Day(), 0, 0, 0, 0, time.UTC)
	}

	unitsRows, err := s.store.ListOperationsUnits(ctx, propertyID)
	if err != nil {
		return domain.OperationsCentreData{}, err
	}
	unitTypes, err := s.store.ListUnitTypes(ctx, propertyID, false)
	if err != nil {
		return domain.OperationsCentreData{}, err
	}
	claims, err := s.store.ListActiveClaimsForDate(ctx, propertyID, selectedDate)
	if err != nil {
		return domain.OperationsCentreData{}, err
	}
	stays, err := s.store.ListOperationsRelevantStays(ctx, propertyID, selectedDate, todayDate)
	if err != nil {
		return domain.OperationsCentreData{}, err
	}
	tasks, err := s.store.ListHousekeepingTasksForDate(ctx, propertyID, selectedDate)
	if err != nil {
		return domain.OperationsCentreData{}, err
	}
	canReadReservations := auth.HasPermission(actor.Role, auth.PermReservationsRead)
	canWriteReservations := auth.HasPermission(actor.Role, auth.PermReservationsWrite)
	canReadGuests := auth.HasPermission(actor.Role, auth.PermGuestsRead)
	canReadGuestNotes := auth.HasPermission(actor.Role, auth.PermGuestsNotes)
	canReadRates := auth.HasPermission(actor.Role, auth.PermRatesRead)
	var payExceptions []domain.PaymentException
	var payAnomalies []postgres.OperationsPaymentAnomalyRow
	if canReadReservations {
		payExceptions, err = s.store.ListPaymentExceptions(ctx, propertyID)
		if err != nil {
			return domain.OperationsCentreData{}, err
		}
		payAnomalies, err = s.store.ListOperationsPaymentAnomalies(ctx, propertyID)
		if err != nil {
			return domain.OperationsCentreData{}, err
		}
	}
	staffMembers, err := s.store.ListAssignableStaff(ctx, propertyID)
	if err != nil {
		return domain.OperationsCentreData{}, err
	}

	unitByID := make(map[uuid.UUID]postgres.OperationsUnitRow, len(unitsRows))
	for _, row := range unitsRows {
		unitByID[row.Unit.ID] = row
	}

	// Track claims overlapping selectedDate per unit without conflating housekeeping with inventory blocks.
	blockByUnit := make(map[uuid.UUID]postgres.OperationsClaimRow)
	stayClaimByUnit := make(map[uuid.UUID]postgres.OperationsClaimRow)
	for _, cl := range claims {
		switch cl.ClaimType {
		case domain.ClaimReservation, domain.ClaimPendingReservation, domain.ClaimExternalReservation, domain.ClaimHold:
			stayClaimByUnit[cl.UnitID] = cl
		case domain.ClaimChannelAllocation, domain.ClaimChannelRestriction:
			// Allocated/channel-restricted inventory is not a physical occupancy or site closure blockout.
		default:
			if _, exists := blockByUnit[cl.UnitID]; !exists {
				blockByUnit[cl.UnitID] = cl
			}
		}
	}

	// Build confirmed/checked-in arrival lookup per unit for selectedDate to identify same-day turnarounds.
	type arrivalInfo struct {
		ReservationID uuid.UUID
		Reference     string
		GuestName     string
		Status        domain.ReservationStatus
	}
	arrivalsByUnit := make(map[uuid.UUID]arrivalInfo)
	for _, st := range stays {
		r := st.Reservation
		if !r.ArrivalDate.Equal(selectedDate) {
			continue
		}
		if r.Status != domain.ResStatusConfirmed && r.Status != domain.ResStatusCheckedIn {
			continue
		}
		for _, uid := range r.UnitIDs {
			arrivalsByUnit[uid] = arrivalInfo{
				ReservationID: r.ID,
				Reference:     r.Reference,
				GuestName:     r.GuestName,
				Status:        r.Status,
			}
		}
	}

	stayByResID := make(map[uuid.UUID]domain.Reservation, len(stays))
	var arrivals, pendingArrivals, inquiries, departures, inHouse, attentionGuests []domain.OpsStayItem
	seenAttentionGuest := make(map[uuid.UUID]bool)

	for _, st := range stays {
		r := st.Reservation
		stayByResID[r.ID] = r

		nights := int(r.DepartureDate.Sub(r.ArrivalDate).Hours() / 24)
		if nights < 1 {
			nights = 1
		}

		unitLabels := make([]string, 0, len(r.Units))
		unitStageParts := make([]string, 0, len(r.Units))
		var hasUnreadyUnit, hasBlockedUnit, isBackToBack bool
		for _, ru := range r.Units {
			unitLabels = append(unitLabels, fmt.Sprintf("%s · %s", ru.UnitCode, ru.UnitName))
			if uRow, ok := unitByID[ru.UnitID]; ok {
				unitStageParts = append(unitStageParts, fmt.Sprintf("%s: %s", ru.UnitCode, uRow.HousekeepingStage.Label()))
				if uRow.HousekeepingStage != domain.StageReady {
					hasUnreadyUnit = true
				}
				if uRow.Unit.Status == domain.UnitStatusMaintenance {
					hasBlockedUnit = true
				}
			}
			if _, blocked := blockByUnit[ru.UnitID]; blocked {
				hasBlockedUnit = true
			}
			if arr, ok := arrivalsByUnit[ru.UnitID]; ok && arr.ReservationID != r.ID && r.DepartureDate.Equal(selectedDate) {
				isBackToBack = true
			}
		}

		var reasons []string
		if canReadReservations && r.CancellationRequestedAt != nil && r.Status != domain.ResStatusCancelled && r.Status != domain.ResStatusNoShow {
			reasons = append(reasons, "Cancellation requested")
		}
		if canReadReservations && r.PaymentStatus == "FAILED" {
			reasons = append(reasons, "Payment failed")
		} else if canReadReservations && r.BalanceDuePence > 0 && (r.PaymentStatus == "UNPAID" || r.PaymentStatus == "PARTIALLY_PAID") &&
			r.Status != domain.ResStatusCancelled && r.Status != domain.ResStatusNoShow && r.Status != domain.ResStatusInquiry {
			reasons = append(reasons, "Balance due")
		}
		if ((canReadReservations || canReadGuests) && st.SpecialRequests != "") || (canReadGuestNotes && strings.TrimSpace(r.InternalNotes) != "") {
			reasons = append(reasons, "Guest note / request")
		}
		if r.ArrivalDate.Equal(selectedDate) && hasUnreadyUnit && r.Status == domain.ResStatusConfirmed {
			reasons = append(reasons, "Assigned unit not ready")
		}
		if r.Status == domain.ResStatusCheckedIn && r.DepartureDate.Before(todayDate) {
			reasons = append(reasons, "Overdue departure")
		}
		if r.Status == domain.ResStatusConfirmed && r.ArrivalDate.Before(todayDate) {
			reasons = append(reasons, "Missed arrival")
		} else if canReadReservations && r.Status == domain.ResStatusPending && r.ArrivalDate.Before(todayDate) {
			reasons = append(reasons, "Unconfirmed provisional reservation past arrival date")
		}

		item := domain.OpsStayItem{
			ReservationID:           r.ID,
			Reference:               r.Reference,
			GuestID:                 r.GuestID,
			GuestName:               r.GuestName,
			GuestEmail:              r.GuestEmail,
			GuestPhone:              r.GuestPhone,
			ChannelCode:             r.ChannelCode,
			ArrivalDate:             r.ArrivalDate,
			DepartureDate:           r.DepartureDate,
			Nights:                  nights,
			Adults:                  r.Adults,
			Children:                r.Children,
			Infants:                 r.Infants,
			Status:                  r.Status,
			PaymentStatus:           r.PaymentStatus,
			TotalPence:              r.TotalPence,
			AmountPaidPence:         r.AmountPaidPence,
			BalanceDuePence:         r.BalanceDuePence,
			Currency:                firstNonEmpty(r.Currency, prop.Currency),
			UnitLabels:              unitLabels,
			UnitIDs:                 append([]uuid.UUID(nil), r.UnitIDs...),
			UnitReadinessSummary:    strings.Join(unitStageParts, "; "),
			HasUnreadyUnit:          hasUnreadyUnit,
			HasBlockedUnit:          hasBlockedUnit,
			IsBackToBack:            isBackToBack,
			SpecialRequests:         st.SpecialRequests,
			InternalNotes:           r.InternalNotes,
			CancellationRequestedAt: r.CancellationRequestedAt,
			CancellationRequestNote: r.CancellationRequestNote,
			RequiresAttention:       len(reasons) > 0,
			AttentionReason:         strings.Join(reasons, " · "),
			Version:                 r.Version,
		}
		if !canReadGuestNotes {
			item.InternalNotes = ""
		}
		if !canReadReservations && !canReadGuests {
			item.GuestEmail = ""
			item.GuestPhone = ""
			item.SpecialRequests = ""
			item.CancellationRequestedAt = nil
			item.CancellationRequestNote = ""
		}
		if !canReadReservations && !canReadRates {
			item.PaymentStatus = ""
			item.TotalPence = 0
			item.AmountPaidPence = 0
			item.BalanceDuePence = 0
		}

		isActiveStatus := r.Status != domain.ResStatusCancelled && r.Status != domain.ResStatusNoShow
		if r.ArrivalDate.Equal(selectedDate) {
			switch r.Status {
			case domain.ResStatusConfirmed, domain.ResStatusCheckedIn:
				arrivals = append(arrivals, item)
			case domain.ResStatusPending:
				if canReadReservations {
					pendingArrivals = append(pendingArrivals, item)
				}
			}
		}
		if canReadReservations && r.Status == domain.ResStatusInquiry && !r.ArrivalDate.Before(selectedDate.AddDate(0, 0, -7)) && !r.ArrivalDate.After(selectedDate.AddDate(0, 0, 14)) {
			inquiries = append(inquiries, item)
		}
		if isActiveStatus && r.DepartureDate.Equal(selectedDate) && (r.Status == domain.ResStatusCheckedIn || r.Status == domain.ResStatusCheckedOut || r.Status == domain.ResStatusConfirmed) {
			departures = append(departures, item)
		}
		if !r.ArrivalDate.After(selectedDate) && r.DepartureDate.After(selectedDate) && (r.Status == domain.ResStatusCheckedIn || (!selectedDate.Equal(todayDate) && r.Status == domain.ResStatusConfirmed)) {
			inHouse = append(inHouse, item)
		}
		if item.RequiresAttention && !seenAttentionGuest[r.ID] && r.Status != domain.ResStatusInquiry && (r.ArrivalDate.Equal(selectedDate) || r.DepartureDate.Equal(selectedDate) || (!r.ArrivalDate.After(selectedDate) && r.DepartureDate.After(selectedDate)) || (canReadReservations && (r.CancellationRequestedAt != nil || r.PaymentStatus == "FAILED"))) {
			seenAttentionGuest[r.ID] = true
			attentionGuests = append(attentionGuests, item)
		}
	}

	// Build 5-stage Daily Changeover Board.
	board := domain.OpsChangeoverBoard{
		GuestDeparting:     []domain.OpsChangeoverDepartingCard{},
		CleaningRequired:   []domain.HousekeepingTask{},
		CleaningInProgress: []domain.HousekeepingTask{},
		InspectionRequired: []domain.HousekeepingTask{},
		ReadyForGuests:     []domain.OpsReadyUnitCard{},
	}

	// Index active checkout changeover tasks so checked-out units don't duplicate stage 1.
	activeTaskByUnit := make(map[uuid.UUID]domain.HousekeepingTask)
	completedTaskByUnit := make(map[uuid.UUID]domain.HousekeepingTask)
	for i := range tasks {
		t := &tasks[i]
		if arr, ok := arrivalsByUnit[t.UnitID]; ok && (t.ReservationID == nil || *t.ReservationID != arr.ReservationID) {
			t.SameDayArrival = true
			t.NextArrivalGuest = arr.GuestName
			t.NextArrivalRef = arr.Reference
		}
		if bl, ok := blockByUnit[t.UnitID]; ok {
			t.HasActiveBlock = true
			t.BlockSummary = formatBlockSummary(bl)
		} else if uRow, ok := unitByID[t.UnitID]; ok && uRow.Unit.Status == domain.UnitStatusMaintenance {
			t.HasActiveBlock = true
			t.BlockSummary = "Unit status: maintenance"
		}
		if t.Stage != domain.StageReady {
			if t.ScheduledDate.Before(todayDate) || (t.DueAt != nil && nowLocal.After(t.DueAt.In(loc))) {
				t.Overdue = true
				board.OverdueCount++
			}
			if _, exists := activeTaskByUnit[t.UnitID]; !exists {
				activeTaskByUnit[t.UnitID] = *t
			}
		} else if _, exists := completedTaskByUnit[t.UnitID]; !exists {
			completedTaskByUnit[t.UnitID] = *t
		}

		switch t.Stage {
		case domain.StageGuestDeparting:
			// Explicit manually-staged departing task if present
			board.CleaningRequired = append(board.CleaningRequired, *t)
		case domain.StageCleaningRequired:
			board.CleaningRequired = append(board.CleaningRequired, *t)
		case domain.StageCleaningInProgress:
			board.CleaningInProgress = append(board.CleaningInProgress, *t)
		case domain.StageInspectionRequired:
			board.InspectionRequired = append(board.InspectionRequired, *t)
		case domain.StageReady:
			if t.ScheduledDate.Equal(selectedDate) {
				uRow := unitByID[t.UnitID]
				board.ReadyForGuests = append(board.ReadyForGuests, domain.OpsReadyUnitCard{
					UnitID:               t.UnitID,
					UnitCode:             t.UnitCode,
					UnitName:             t.UnitName,
					UnitTypeName:         t.UnitTypeName,
					UnitStatus:           string(uRow.Unit.Status),
					TaskID:               &t.ID,
					ReservationReference: t.ReservationReference,
					InspectedAt:          t.InspectedAt,
					InspectedByName:      t.InspectedByName,
					CompletedAt:          t.CompletedAt,
					SameDayArrival:       t.SameDayArrival,
					NextArrivalGuest:     t.NextArrivalGuest,
					HasActiveBlock:       t.HasActiveBlock,
					BlockSummary:         t.BlockSummary,
				})
			}
		}
	}

	// Stage 1: Checked-in guests scheduled to depart on selectedDate (or overdue departing).
	lateClockOut := selectedDate.Equal(todayDate) && pastPropertyClock(nowLocal, prop.CheckOutTime)
	departingUnitSeen := make(map[uuid.UUID]bool)
	for _, st := range stays {
		r := st.Reservation
		if r.Status != domain.ResStatusCheckedIn {
			continue
		}
		if !r.DepartureDate.Equal(selectedDate) && !(selectedDate.Equal(todayDate) && r.DepartureDate.Before(todayDate)) {
			continue
		}
		for _, ru := range r.Units {
			departingUnitSeen[ru.UnitID] = true
			arr, hasNextArr := arrivalsByUnit[ru.UnitID]
			sameDay := hasNextArr && arr.ReservationID != r.ID
			depBal := r.BalanceDuePence
			if !canReadReservations && !canReadRates {
				depBal = 0
			}
			board.GuestDeparting = append(board.GuestDeparting, domain.OpsChangeoverDepartingCard{
				ReservationID:        r.ID,
				ReservationReference: r.Reference,
				GuestName:            r.GuestName,
				DepartureDate:        r.DepartureDate,
				ExpectedCheckOutTime: prop.CheckOutTime,
				UnitID:               ru.UnitID,
				UnitCode:             ru.UnitCode,
				UnitName:             ru.UnitName,
				UnitTypeName:         ru.UnitTypeName,
				SameDayArrival:       sameDay,
				NextArrivalGuest:     arr.GuestName,
				NextArrivalRef:       arr.Reference,
				BalanceDuePence:      depBal,
				Currency:             firstNonEmpty(r.Currency, prop.Currency),
				LateDeparture:        r.DepartureDate.Before(todayDate) || (r.DepartureDate.Equal(todayDate) && lateClockOut),
				ReservationVersion:   r.Version,
			})
		}
	}

	// Add units currently in READY status that weren't already listed from completed today tasks.
	readyListedUnit := make(map[uuid.UUID]bool, len(board.ReadyForGuests))
	for _, card := range board.ReadyForGuests {
		readyListedUnit[card.UnitID] = true
	}
	for _, uRow := range unitsRows {
		if readyListedUnit[uRow.Unit.ID] || departingUnitSeen[uRow.Unit.ID] {
			continue
		}
		if _, hasOpen := activeTaskByUnit[uRow.Unit.ID]; hasOpen {
			continue
		}
		if uRow.HousekeepingStage != domain.StageReady {
			continue
		}
		arr, hasNextArr := arrivalsByUnit[uRow.Unit.ID]
		bl, hasBlock := blockByUnit[uRow.Unit.ID]
		blockSummary := ""
		if hasBlock {
			blockSummary = formatBlockSummary(bl)
		} else if uRow.Unit.Status == domain.UnitStatusMaintenance {
			hasBlock = true
			blockSummary = "Unit status: maintenance"
		}
		board.ReadyForGuests = append(board.ReadyForGuests, domain.OpsReadyUnitCard{
			UnitID:           uRow.Unit.ID,
			UnitCode:         uRow.Unit.Code,
			UnitName:         uRow.Unit.Name,
			UnitTypeName:     uRow.Unit.UnitTypeName,
			UnitStatus:       string(uRow.Unit.Status),
			SameDayArrival:   hasNextArr,
			NextArrivalGuest: arr.GuestName,
			HasActiveBlock:   hasBlock,
			BlockSummary:     blockSummary,
		})
	}

	board.GuestDepartingCount = len(board.GuestDeparting)
	board.CleaningRequiredCount = len(board.CleaningRequired)
	board.CleaningInProgressCount = len(board.CleaningInProgress)
	board.InspectionRequiredCount = len(board.InspectionRequired)
	board.ReadyForGuestsCount = len(board.ReadyForGuests)

	// Build Daily Occupancy Overview (strictly operational, no Phase 5 historical/revenue BI).
	occ := domain.OpsOccupancySummary{
		ArrivalsToday:   len(arrivals),
		DeparturesToday: len(departures),
	}
	utOccMap := make(map[uuid.UUID]*domain.OpsUnitTypeOccupancy, len(unitTypes))
	utOrder := make([]uuid.UUID, 0, len(unitTypes))
	for _, ut := range unitTypes {
		if ut.ArchivedAt != nil {
			continue
		}
		utOccMap[ut.ID] = &domain.OpsUnitTypeOccupancy{
			UnitTypeID:   ut.ID,
			UnitTypeCode: ut.Code,
			UnitTypeName: ut.Name,
			Category:     string(ut.Category),
		}
		utOrder = append(utOrder, ut.ID)
	}

	for _, uRow := range unitsRows {
		u := uRow.Unit
		occ.TotalUnits++
		utAgg := utOccMap[u.UnitTypeID]
		if utAgg != nil {
			utAgg.TotalUnits++
		}

		hkStage := uRow.HousekeepingStage
		if openTask, ok := activeTaskByUnit[u.ID]; ok {
			hkStage = openTask.Stage
		} else if departingUnitSeen[u.ID] {
			hkStage = domain.StageGuestDeparting
		}
		if hkStage == domain.StageReady {
			occ.ReadyUnits++
		} else {
			occ.UnreadyUnits++
			if utAgg != nil {
				utAgg.NeedingCleaning++
			}
		}

		bl, hasClaimBlock := blockByUnit[u.ID]
		isBlocked := hasClaimBlock || u.Status == domain.UnitStatusMaintenance
		blockReason := ""
		blockCategory := ""
		if hasClaimBlock {
			blockCategory = string(bl.ClaimType)
			blockReason = bl.Reason
		} else if u.Status == domain.UnitStatusMaintenance {
			blockCategory = "UNIT_MAINTENANCE"
			blockReason = "Unit marked in maintenance status"
		}

		occState := "AVAILABLE"
		var stayRef, stayGuest string
		var stayResID *uuid.UUID
		sc, hasStayClaim := stayClaimByUnit[u.ID]
		if hasStayClaim {
			occ.OccupiedUnits++
			if utAgg != nil {
				utAgg.OccupiedUnits++
			}
			stayResID = sc.ReservationID
			if sc.ReservationID != nil {
				if r, ok := stayByResID[*sc.ReservationID]; ok {
					stayRef = r.Reference
					stayGuest = r.GuestName
					switch r.Status {
					case domain.ResStatusCheckedIn:
						occState = "CHECKED_IN"
						occ.CheckedInUnits++
					case domain.ResStatusPending:
						occState = "PROVISIONAL"
					default:
						if r.ArrivalDate.Equal(selectedDate) {
							occState = "ARRIVING"
						} else {
							occState = "RESERVED"
						}
					}
				} else {
					occState = "RESERVED"
				}
			} else {
				occState = "HOLD"
			}
		} else if isBlocked {
			occState = "BLOCKED"
			occ.BlockedUnits++
			if utAgg != nil {
				utAgg.BlockedUnits++
			}
		} else {
			occ.AvailableUnits++
			if utAgg != nil {
				utAgg.AvailableUnits++
			}
		}

		occ.Units = append(occ.Units, domain.OpsUnitSnapshot{
			UnitID:              u.ID,
			UnitCode:            u.Code,
			UnitName:            u.Name,
			UnitTypeName:        u.UnitTypeName,
			Zone:                u.Zone,
			UnitStatus:          string(u.Status),
			HousekeepingStage:   hkStage,
			OccupancyState:      occState,
			StayReference:       stayRef,
			StayReservationID:   stayResID,
			StayGuestName:       stayGuest,
			HasMaintenanceBlock: isBlocked,
			BlockReason:         blockReason,
			BlockCategory:       blockCategory,
		})
	}
	for _, utID := range utOrder {
		if agg := utOccMap[utID]; agg != nil && agg.TotalUnits > 0 {
			occ.ByUnitType = append(occ.ByUnitType, *agg)
		}
	}

	// Build prioritized Operational Attention List ("What needs my attention?").
	var attention []domain.OpsAttentionItem
	for _, pe := range payExceptions {
		if pe.Status != "OPEN" {
			continue
		}
		targetURL := fmt.Sprintf("/payment-exceptions#ex-%s", pe.ID.String())
		if pe.ReservationID != nil {
			targetURL = "/reservations/" + pe.ReservationID.String()
		}
		actionLabel := "View exception"
		if canWriteReservations {
			actionLabel = "Resolve payment"
		}
		attention = append(attention, domain.OpsAttentionItem{
			Severity:      domain.SeverityUrgent,
			Category:      "PAYMENT_EXCEPTION",
			Title:         fmt.Sprintf("Open payment exception (%s)", pe.ExceptionType),
			Detail:        firstNonEmpty(pe.Details, "Customer payment requires immediate manual reconciliation."),
			ActionLabel:   actionLabel,
			ActionURL:     targetURL,
			ReservationID: pe.ReservationID,
			AmountPence:   pe.AmountPence,
			Currency:      pe.Currency,
		})
	}
	for _, anom := range payAnomalies {
		targetURL := "/payment-exceptions"
		actionLabel := "Review payment log"
		if anom.ReservationID != nil {
			targetURL = "/reservations/" + anom.ReservationID.String()
			actionLabel = "Review booking"
		}
		attention = append(attention, domain.OpsAttentionItem{
			Severity:      domain.SeverityUrgent,
			Category:      "FAILED_PAYMENT",
			Title:         fmt.Sprintf("Failed checkout / payment attempt (%s)", anom.Reference),
			Detail:        anom.Detail,
			ActionLabel:   actionLabel,
			ActionURL:     targetURL,
			ReservationID: anom.ReservationID,
			AmountPence:   anom.AmountPence,
			Currency:      anom.Currency,
		})
	}
	for _, st := range stays {
		r := st.Reservation
		resURL := "/reservations/" + r.ID.String()
		opsStayURL := resURL
		if !canReadReservations {
			opsStayURL = "#changeover-board"
		}
		curr := firstNonEmpty(r.Currency, prop.Currency)

		if canReadReservations && r.CancellationRequestedAt != nil && r.Status != domain.ResStatusCancelled && r.Status != domain.ResStatusNoShow {
			attention = append(attention, domain.OpsAttentionItem{
				Severity:      domain.SeverityUrgent,
				Category:      "CANCELLATION_REQUEST",
				Title:         fmt.Sprintf("Guest cancellation request · %s (%s)", r.Reference, r.GuestName),
				Detail:        firstNonEmpty(r.CancellationRequestNote, "Guest submitted a cancellation request via direct booking confirmation."),
				ActionLabel:   "Review request",
				ActionURL:     resURL,
				ReservationID: &r.ID,
			})
		}
		if canReadReservations && r.PaymentStatus == "FAILED" && r.Status != domain.ResStatusCancelled && r.Status != domain.ResStatusNoShow {
			attention = append(attention, domain.OpsAttentionItem{
				Severity:      domain.SeverityUrgent,
				Category:      "FAILED_PAYMENT",
				Title:         fmt.Sprintf("Failed reservation payment · %s (%s)", r.Reference, r.GuestName),
				Detail:        fmt.Sprintf("Stay %s -> %s currently marked payment_status=FAILED.", domain.FormatDate(r.ArrivalDate), domain.FormatDate(r.DepartureDate)),
				ActionLabel:   "Open reservation",
				ActionURL:     resURL,
				ReservationID: &r.ID,
				AmountPence:   r.BalanceDuePence,
				Currency:      curr,
			})
		}
		if r.Status == domain.ResStatusCheckedIn && (r.DepartureDate.Before(todayDate) || (selectedDate.Equal(todayDate) && r.DepartureDate.Equal(todayDate) && lateClockOut)) {
			attention = append(attention, domain.OpsAttentionItem{
				Severity:      domain.SeverityUrgent,
				Category:      "LATE_DEPARTURE",
				Title:         fmt.Sprintf("Late departure · %s (%s)", r.Reference, r.GuestName),
				Detail:        fmt.Sprintf("Due out %s at %s; guest still checked in.", domain.FormatDate(r.DepartureDate), prop.CheckOutTime),
				ActionLabel:   "Check out / view",
				ActionURL:     opsStayURL,
				ReservationID: &r.ID,
			})
		}
		if r.Status == domain.ResStatusConfirmed && r.ArrivalDate.Before(todayDate) {
			attention = append(attention, domain.OpsAttentionItem{
				Severity:      domain.SeverityHigh,
				Category:      "MISSED_ARRIVAL",
				Title:         fmt.Sprintf("Missed / overdue arrival · %s (%s)", r.Reference, r.GuestName),
				Detail:        fmt.Sprintf("Confirmed arrival %s has not checked in yet.", domain.FormatDate(r.ArrivalDate)),
				ActionLabel:   "Check in or No-Show",
				ActionURL:     opsStayURL,
				ReservationID: &r.ID,
			})
		}
		if r.ArrivalDate.Equal(selectedDate) && r.Status == domain.ResStatusConfirmed {
			for _, ru := range r.Units {
				if uRow, ok := unitByID[ru.UnitID]; ok && uRow.HousekeepingStage != domain.StageReady {
					attention = append(attention, domain.OpsAttentionItem{
						Severity:      domain.SeverityUrgent,
						Category:      "UNREADY_ARRIVAL_UNIT",
						Title:         fmt.Sprintf("Arrival today on unready unit %s · %s", ru.UnitCode, r.Reference),
						Detail:        fmt.Sprintf("%s expects check-in from %s; unit is %s.", r.GuestName, prop.CheckInTime, uRow.HousekeepingStage.Label()),
						ActionLabel:   "Open changeover",
						ActionURL:     "#changeover-board",
						ReservationID: &r.ID,
						UnitID:        &ru.UnitID,
					})
				}
			}
		}
		if canReadReservations && r.BalanceDuePence > 0 && (r.PaymentStatus == "UNPAID" || r.PaymentStatus == "PARTIALLY_PAID") &&
			r.Status != domain.ResStatusCancelled && r.Status != domain.ResStatusNoShow && r.Status != domain.ResStatusInquiry {
			attention = append(attention, domain.OpsAttentionItem{
				Severity:      domain.SeverityMedium,
				Category:      "OUTSTANDING_BALANCE",
				Title:         fmt.Sprintf("Outstanding balance %s · %s (%s)", domain.FormatPence(r.BalanceDuePence, curr), r.Reference, r.GuestName),
				Detail:        fmt.Sprintf("Arrival %s · %s of %s paid.", domain.FormatDate(r.ArrivalDate), domain.FormatPence(r.AmountPaidPence, curr), domain.FormatPence(r.TotalPence, curr)),
				ActionLabel:   "Manage balance",
				ActionURL:     resURL,
				ReservationID: &r.ID,
				AmountPence:   r.BalanceDuePence,
				Currency:      curr,
			})
		}
		if canReadReservations && r.Status == domain.ResStatusPending && !r.ArrivalDate.After(selectedDate.AddDate(0, 0, 7)) {
			sev := domain.SeverityMedium
			if !r.ArrivalDate.After(selectedDate) {
				sev = domain.SeverityHigh
			}
			attention = append(attention, domain.OpsAttentionItem{
				Severity:      sev,
				Category:      "PENDING_CONFIRMATION",
				Title:         fmt.Sprintf("Pending provisional reservation · %s (%s)", domain.FormatDate(r.ArrivalDate), r.Reference),
				Detail:        fmt.Sprintf("Guest %s holds a provisional allocation awaiting confirmation or expiry.", r.GuestName),
				ActionLabel:   "Review hold",
				ActionURL:     resURL,
				ReservationID: &r.ID,
			})
		}
		if canReadReservations && r.Status == domain.ResStatusInquiry && !r.ArrivalDate.Before(selectedDate.AddDate(0, 0, -7)) && !r.ArrivalDate.After(selectedDate.AddDate(0, 0, 14)) {
			attention = append(attention, domain.OpsAttentionItem{
				Severity:      domain.SeverityMedium,
				Category:      "INQUIRY_FOLLOW_UP",
				Title:         fmt.Sprintf("Unconfirmed inquiry · %s (%s)", domain.FormatDate(r.ArrivalDate), r.Reference),
				Detail:        fmt.Sprintf("Guest %s (%s -> %s) awaits follow-up; inventory is not held until confirmed.", r.GuestName, domain.FormatDate(r.ArrivalDate), domain.FormatDate(r.DepartureDate)),
				ActionLabel:   "Follow up inquiry",
				ActionURL:     resURL,
				ReservationID: &r.ID,
			})
		}
	}

	for i := range tasks {
		t := tasks[i]
		if t.Stage == domain.StageReady {
			continue
		}
		if t.Overdue || t.Priority == domain.TaskPriorityUrgent {
			sev := domain.SeverityHigh
			if t.Overdue && t.SameDayArrival {
				sev = domain.SeverityUrgent
			}
			tid := t.ID
			attention = append(attention, domain.OpsAttentionItem{
				Severity:    sev,
				Category:    "HOUSEKEEPING_DUE",
				Title:       fmt.Sprintf("%s · %s (%s)", t.Stage.Label(), t.UnitCode, t.UnitName),
				Detail:      firstNonEmpty(t.Notes, t.Title),
				ActionLabel: "Update task",
				ActionURL:   "#changeover-board",
				UnitID:      &t.UnitID,
				TaskID:      &tid,
			})
		}
	}

	for _, uRow := range unitsRows {
		u := uRow.Unit
		if bl, ok := blockByUnit[u.ID]; ok && (bl.ClaimType == domain.ClaimMaintenance || bl.ClaimType == domain.ClaimWeatherClosure) {
			attention = append(attention, domain.OpsAttentionItem{
				Severity:    domain.SeverityHigh,
				Category:    "MAINTENANCE_BLOCK",
				Title:       fmt.Sprintf("Active %s on %s (%s)", strings.ReplaceAll(strings.ToLower(string(bl.ClaimType)), "_", " "), u.Code, u.Name),
				Detail:      fmt.Sprintf("%s (%s -> %s)", firstNonEmpty(bl.Reason, "Operational block"), domain.FormatDate(bl.StartDate), domain.FormatDate(bl.EndDate)),
				ActionLabel: "Manage blockouts",
				ActionURL:   "/blocks",
				UnitID:      &u.ID,
			})
		} else if u.Status == domain.UnitStatusMaintenance {
			attention = append(attention, domain.OpsAttentionItem{
				Severity:    domain.SeverityHigh,
				Category:    "MAINTENANCE_UNIT",
				Title:       fmt.Sprintf("Unit %s (%s) in maintenance status", u.Code, u.Name),
				Detail:      firstNonEmpty(u.OperationalNotes, "Excluded from live public availability until restored to active."),
				ActionLabel: "Edit unit",
				ActionURL:   "/units?edit=" + u.ID.String(),
				UnitID:      &u.ID,
			})
		}
	}

	maintSummary, err := s.store.GetOpsMaintenanceSummary(ctx, nil, propertyID, selectedDate, todayDate)
	if err != nil {
		return domain.OperationsCentreData{}, err
	}
	var affectedBookings []postgres.MaintenanceAffectedBooking
	if canReadReservations {
		affectedBookings, err = s.store.ListMaintenanceAffectedBookings(ctx, nil, propertyID, todayDate, todayDate.AddDate(0, 0, 7))
		if err != nil {
			return domain.OperationsCentreData{}, err
		}
	}

	seenMaintAlert := make(map[string]bool)
	addMaintAlert := func(key string, item domain.OpsAttentionItem) {
		if seenMaintAlert[key] {
			return
		}
		seenMaintAlert[key] = true
		attention = append(attention, item)
	}

	for _, r := range maintSummary.PriorityJobs {
		maintURL := "/maintenance/" + r.ID.String()
		switch {
		case r.SafetyImpact:
			addMaintAlert("SAFETY:"+r.ID.String(), domain.OpsAttentionItem{
				Severity:    domain.SeverityUrgent,
				Category:    "MAINTENANCE_SAFETY",
				Title:       fmt.Sprintf("Safety maintenance · %s (%s)", r.Reference, r.Title),
				Detail:      fmt.Sprintf("%s · %s", r.LocationLabel(), r.Status.Label()),
				ActionLabel: "Open job",
				ActionURL:   maintURL,
				UnitID:      r.UnitID,
			})
		case r.Priority == domain.MaintPriorityUrgent:
			addMaintAlert("URGENT:"+r.ID.String(), domain.OpsAttentionItem{
				Severity:    domain.SeverityUrgent,
				Category:    "MAINTENANCE_URGENT",
				Title:       fmt.Sprintf("Urgent maintenance · %s (%s)", r.Reference, r.Title),
				Detail:      fmt.Sprintf("%s · %s", r.LocationLabel(), r.AssigneeLabel()),
				ActionLabel: "Open job",
				ActionURL:   maintURL,
				UnitID:      r.UnitID,
			})
		}
		if r.HasActiveRestriction {
			addMaintAlert("RESTRICT:"+r.ID.String(), domain.OpsAttentionItem{
				Severity:    domain.SeverityHigh,
				Category:    "MAINTENANCE_UNIT_RESTRICTED",
				Title:       fmt.Sprintf("Unit restricted by maintenance · %s (%s)", r.Reference, firstNonEmpty(r.UnitCode, r.LocationLabel())),
				Detail:      fmt.Sprintf("%s · Explicit release required after repair", r.Title),
				ActionLabel: "Manage restriction",
				ActionURL:   maintURL,
				UnitID:      r.UnitID,
			})
		}
		if r.Overdue {
			sev := domain.SeverityHigh
			if r.Priority == domain.MaintPriorityUrgent || r.SafetyImpact {
				sev = domain.SeverityUrgent
			}
			dueLabel := "n/a"
			if r.TargetDate != nil {
				dueLabel = domain.FormatDate(*r.TargetDate)
			}
			addMaintAlert("OVERDUE:"+r.ID.String(), domain.OpsAttentionItem{
				Severity:    sev,
				Category:    "MAINTENANCE_OVERDUE",
				Title:       fmt.Sprintf("Overdue maintenance · %s (%s)", r.Reference, r.Title),
				Detail:      fmt.Sprintf("%s · Due %s", r.LocationLabel(), dueLabel),
				ActionLabel: "Review job",
				ActionURL:   maintURL,
				UnitID:      r.UnitID,
			})
		}
	}
	for _, r := range maintSummary.AwaitingInspection {
		addMaintAlert("INSPECT:"+r.ID.String(), domain.OpsAttentionItem{
			Severity:    domain.SeverityMedium,
			Category:    "MAINTENANCE_INSPECTION_OVERDUE",
			Title:       fmt.Sprintf("Maintenance awaiting inspection · %s (%s)", r.Reference, r.Title),
			Detail:      fmt.Sprintf("%s · Repair marked completed, sign-off required", r.LocationLabel()),
			ActionLabel: "Inspect repair",
			ActionURL:   "/maintenance/" + r.ID.String(),
			UnitID:      r.UnitID,
		})
	}
	for _, ab := range affectedBookings {
		resID := ab.ReservationID
		uID := ab.UnitID
		addMaintAlert("BOOKING:"+ab.RequestID.String()+":"+ab.ReservationID.String(), domain.OpsAttentionItem{
			Severity:      domain.SeverityUrgent,
			Category:      "MAINTENANCE_BOOKING_IMPACT",
			Title:         fmt.Sprintf("Booking impacted by maintenance · %s on %s (%s)", ab.BookingRef, ab.UnitCode, ab.Reference),
			Detail:        fmt.Sprintf("Guest %s arrives %s while \"%s\" remains open.", ab.GuestName, domain.FormatDate(ab.CheckInDate), ab.Title),
			ActionLabel:   "View maintenance",
			ActionURL:     "/maintenance/" + ab.RequestID.String(),
			ReservationID: &resID,
			UnitID:        &uID,
		})
	}

	var guestCRMSummary domain.OpsGuestCRMSummary
	if auth.HasPermission(actor.Role, auth.PermGuestsRead) || auth.HasPermission(actor.Role, auth.PermCommsRead) {
		var crmAlerts []domain.OpsAttentionItem
		var crmErr error
		guestCRMSummary, crmAlerts, crmErr = s.store.GetOpsGuestCRMAlerts(ctx, propertyID, selectedDate)
		if crmErr != nil {
			return domain.OperationsCentreData{}, crmErr
		}
		attention = append(attention, crmAlerts...)
	}

	sort.SliceStable(attention, func(i, j int) bool {
		return severityRank(attention[i].Severity) < severityRank(attention[j].Severity)
	})

	var arrivalsPending, arrivalsCheckedIn int
	for _, a := range arrivals {
		if a.Status == domain.ResStatusCheckedIn {
			arrivalsCheckedIn++
		} else if a.Status == domain.ResStatusConfirmed {
			arrivalsPending++
		}
	}
	var departuresPending, departuresOut int
	for _, d := range departures {
		if d.Status == domain.ResStatusCheckedOut {
			departuresOut++
		} else {
			departuresPending++
		}
	}
	var urgentAttention int
	for _, item := range attention {
		if item.Severity == domain.SeverityUrgent {
			urgentAttention++
		}
	}

	summary := domain.OpsDailySummary{
		InHouseStays:           len(inHouse),
		ArrivalsTotal:          len(arrivals),
		ArrivalsPendingCheckIn: arrivalsPending,
		ArrivalsCheckedIn:      arrivalsCheckedIn,
		PendingArrivalsCount:   len(pendingArrivals),
		InquiriesCount:         len(inquiries),
		DeparturesTotal:        len(departures),
		DeparturesPendingOut:   departuresPending,
		DeparturesCheckedOut:   departuresOut,
		GuestsNeedingAttention: len(attentionGuests),
		TasksOpenCount:         board.CleaningRequiredCount + board.CleaningInProgressCount + board.InspectionRequiredCount,
		TasksOverdueCount:      board.OverdueCount,
		AttentionCount:         len(attention),
		UrgentAttentionCount:   urgentAttention,
	}

	return domain.OperationsCentreData{
		Property:        prop,
		OperationalDate: selectedDate,
		TodayDate:       todayDate,
		IsToday:         selectedDate.Equal(todayDate),
		LocalTimeLabel:  nowLocal.Format("15:04 MST"),
		Focus:           domain.ParseOpsFocus(focusRaw),
		Summary:         summary,
		Arrivals:        arrivals,
		PendingArrivals: pendingArrivals,
		Inquiries:       inquiries,
		Departures:      departures,
		InHouse:         inHouse,
		AttentionGuests: attentionGuests,
		AttentionItems:  attention,
		ChangeoverBoard: board,
		Occupancy:       occ,
		Maintenance:     maintSummary,
		GuestCRM:        guestCRMSummary,
		StaffMembers:    staffMembers,
	}, nil
}

// CreateHousekeepingTask creates a manual cleaning/inspection changeover task for a physical unit.
func (s *Service) CreateHousekeepingTask(
	ctx context.Context,
	actor Actor,
	propertyID, unitID uuid.UUID,
	taskType domain.HousekeepingTaskType,
	stage domain.ChangeoverStage,
	priority domain.HousekeepingPriority,
	scheduledDate time.Time,
	title, notes string,
	assignedTo *uuid.UUID,
) (domain.HousekeepingTask, error) {
	if err := s.requirePerm(actor, auth.PermHousekeepingWrite); err != nil {
		return domain.HousekeepingTask{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.HousekeepingTask{}, err
	}
	id, err := postgres.NewUUID()
	if err != nil {
		return domain.HousekeepingTask{}, err
	}
	task := domain.HousekeepingTask{
		ID:            id,
		PropertyID:    propertyID,
		UnitID:        unitID,
		TaskType:      taskType,
		Stage:         stage,
		Priority:      priority,
		ScheduledDate: scheduledDate,
		AssignedTo:    assignedTo,
		Title:         title,
		Notes:         notes,
	}
	if task.Stage == "" || task.Stage == domain.StageGuestDeparting || task.Stage == domain.StageReady {
		task.Stage = domain.StageCleaningRequired
	}
	if err := task.Validate(); err != nil {
		return domain.HousekeepingTask{}, err
	}

	var created domain.HousekeepingTask
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		prop, err := s.store.GetPropertyByID(ctx, tx, propertyID)
		if err != nil {
			return err
		}
		if _, err := s.store.GetUnit(ctx, tx, propertyID, unitID); err != nil {
			return err
		}
		if assignedTo != nil && *assignedTo != uuid.Nil {
			u, err := s.store.GetAdminUserByID(ctx, tx, *assignedTo)
			if err != nil {
				return domain.NewValidationError(map[string]string{"assigned_to": "selected staff member not found"})
			}
			if !u.IsActive || (u.PropertyID != nil && *u.PropertyID != propertyID) {
				return domain.NewValidationError(map[string]string{"assigned_to": "selected staff member is not active for this property"})
			}
		}
		if loc, lerr := time.LoadLocation(prop.Timezone); lerr == nil {
			if parsed, perr := time.Parse("15:04", prop.CheckInTime); perr == nil {
				dt := time.Date(scheduledDate.Year(), scheduledDate.Month(), scheduledDate.Day(), parsed.Hour(), parsed.Minute(), 0, 0, loc).UTC()
				task.DueAt = &dt
			}
		}
		var txErr error
		created, txErr = s.store.CreateHousekeepingTask(ctx, tx, task, actor.UserID)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "housekeeping_task.created", "housekeeping_task", created.ID.String(), nil, created)
	})
	return created, err
}

// TransitionHousekeepingTaskStage advances or returns a changeover task across the 5-stage workflow.
// Completing a task updates unit housekeeping readiness without altering inventory claims or blockouts.
func (s *Service) TransitionHousekeepingTaskStage(
	ctx context.Context,
	actor Actor,
	propertyID, taskID uuid.UUID,
	nextStage domain.ChangeoverStage,
	notes string,
	version int,
) (domain.HousekeepingTask, error) {
	if err := s.requirePerm(actor, auth.PermHousekeepingWrite); err != nil {
		return domain.HousekeepingTask{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.HousekeepingTask{}, err
	}
	nextStage = domain.ChangeoverStage(strings.ToUpper(strings.TrimSpace(string(nextStage))))
	if !nextStage.Valid() {
		return domain.HousekeepingTask{}, domain.NewValidationError(map[string]string{
			"stage": "invalid changeover stage",
		})
	}

	var updated domain.HousekeepingTask
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetHousekeepingTask(ctx, tx, propertyID, taskID)
		if err != nil {
			return err
		}
		var txErr error
		updated, txErr = s.store.TransitionHousekeepingTaskStage(ctx, tx, propertyID, taskID, nextStage, notes, actor.UserID, version)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "housekeeping_task.stage_changed", "housekeeping_task", updated.ID.String(), before, updated)
	})
	return updated, err
}

// AssignHousekeepingTask assigns or unassigns a housekeeping task to a staff member.
func (s *Service) AssignHousekeepingTask(
	ctx context.Context,
	actor Actor,
	propertyID, taskID uuid.UUID,
	assigneeID *uuid.UUID,
	version int,
) (domain.HousekeepingTask, error) {
	if err := s.requirePerm(actor, auth.PermHousekeepingWrite); err != nil {
		return domain.HousekeepingTask{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.HousekeepingTask{}, err
	}

	var updated domain.HousekeepingTask
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetHousekeepingTask(ctx, tx, propertyID, taskID)
		if err != nil {
			return err
		}
		if assigneeID != nil && *assigneeID != uuid.Nil {
			u, err := s.store.GetAdminUserByID(ctx, tx, *assigneeID)
			if err != nil {
				return domain.NewValidationError(map[string]string{"assigned_to": "staff user not found"})
			}
			if !u.IsActive || (u.PropertyID != nil && *u.PropertyID != propertyID) {
				return domain.NewValidationError(map[string]string{"assigned_to": "staff user cannot be assigned to this property"})
			}
		} else {
			assigneeID = nil
		}
		var txErr error
		updated, txErr = s.store.AssignHousekeepingTask(ctx, tx, propertyID, taskID, assigneeID, actor.UserID, version)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "housekeeping_task.assigned", "housekeeping_task", updated.ID.String(), before, updated)
	})
	return updated, err
}

func severityRank(s domain.OpsAttentionSeverity) int {
	switch s {
	case domain.SeverityUrgent:
		return 1
	case domain.SeverityHigh:
		return 2
	default:
		return 3
	}
}

func pastPropertyClock(nowLocal time.Time, hhmm string) bool {
	var t time.Time
	var err error
	if t, err = time.Parse("15:04:05", strings.TrimSpace(hhmm)); err != nil {
		if t, err = time.Parse("15:04", strings.TrimSpace(hhmm)); err != nil {
			return false
		}
	}
	cutoff := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), t.Hour(), t.Minute(), 0, 0, nowLocal.Location())
	return nowLocal.After(cutoff)
}

func formatBlockSummary(c postgres.OperationsClaimRow) string {
	label := strings.ReplaceAll(strings.ToLower(string(c.ClaimType)), "_", " ")
	if strings.TrimSpace(c.Reason) != "" {
		return fmt.Sprintf("%s: %s", label, c.Reason)
	}
	return label
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
