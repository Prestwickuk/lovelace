package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
)

type CalendarQuery struct {
	PropertyID  uuid.UUID
	StartDate   time.Time
	EndDate     time.Time // Exclusive
	View        string    // month, week, day, 7d, 14d, 30d
	Horizon     string    // optional PWE-2 horizon alias
	Grouping    string    // unit (default) or unit_type
	UnitTypeID  *uuid.UUID
	ChannelCode string
	Status      string
}

type CalendarDayCell struct {
	Date              string     `json:"date"`
	Status            string     `json:"status"` // AVAILABLE, BOOKED, PENDING, BLOCKED, UNRELEASED
	ClaimID           *uuid.UUID `json:"claim_id,omitempty"`
	ClaimType         string     `json:"claim_type,omitempty"`
	ReservationID     *uuid.UUID `json:"reservation_id,omitempty"`
	BlockoutGroupID   *uuid.UUID `json:"blockout_group_id,omitempty"`
	Reference         string     `json:"reference,omitempty"`
	GuestName         string     `json:"guest_name,omitempty"`
	ReservationStatus string     `json:"reservation_status,omitempty"`
	ChannelCode       string     `json:"channel_code,omitempty"`
	Reason            string     `json:"reason,omitempty"`
	IsCheckIn         bool       `json:"is_check_in,omitempty"`
	IsCheckOut        bool       `json:"is_check_out,omitempty"`
}

type CalendarUnitRow struct {
	Unit  domain.Unit       `json:"unit"`
	Cells []CalendarDayCell `json:"cells"`
}

type CalendarSummary struct {
	TotalUnitNights      int `json:"total_unit_nights"`
	BookedNights         int `json:"booked_nights"`
	PendingNights        int `json:"pending_nights"`
	AllocatedNights      int `json:"allocated_nights"`
	BlockedNights        int `json:"blocked_nights"`
	UnreleasedNights     int `json:"unreleased_nights"`
	AvailableNights      int `json:"available_nights"`
	ActiveReservations   int `json:"active_reservations"`
	ActiveBlockoutGroups int `json:"active_blockout_groups"`
}

type CalendarViewData struct {
	Property       domain.Property                  `json:"property"`
	View           string                           `json:"view"`
	Horizon        string                           `json:"horizon"`
	Grouping       string                           `json:"grouping"`
	StartDate      time.Time                        `json:"start_date"`
	EndDate        time.Time                        `json:"end_date"`
	Dates          []time.Time                      `json:"dates"`
	UnitTypes      []domain.UnitType                `json:"unit_types"`
	Channels       []domain.BookingChannel          `json:"channels"`
	Units          []CalendarUnitRow                `json:"units"`
	ActiveReleases []domain.AvailabilityReleaseRule `json:"active_releases"`
	Summary        CalendarSummary                  `json:"summary"`
}

func (s *Service) GetCalendarView(ctx context.Context, actor Actor, q CalendarQuery) (CalendarViewData, error) {
	if err := s.requirePerm(actor, auth.PermReservationsRead); err != nil {
		return CalendarViewData{}, err
	}
	if err := s.requirePropertyScope(actor, q.PropertyID); err != nil {
		return CalendarViewData{}, err
	}

	prop, err := s.store.GetPropertyByID(ctx, nil, q.PropertyID)
	if err != nil {
		return CalendarViewData{}, err
	}

	now := s.nowUTC()
	rawHorizon := strings.TrimSpace(q.Horizon)
	if rawHorizon == "" {
		rawHorizon = strings.TrimSpace(q.View)
	}
	horizon, ok := domain.IsAllowedHorizon(rawHorizon, false)
	if !ok {
		horizon = domain.HorizonMonth
	}
	view := horizon
	if view == domain.Horizon7Days {
		view = "week"
	}
	grouping, ok := domain.IsAllowedGrouping(q.Grouping, false)
	if !ok {
		grouping = domain.GroupingUnit
	}

	startDate := q.StartDate
	if startDate.IsZero() {
		loc, err := time.LoadLocation(prop.Timezone)
		if err != nil {
			loc = time.UTC
		}
		localNow := now.In(loc)
		switch horizon {
		case domain.HorizonDay, domain.Horizon14Days, domain.Horizon30Days:
			startDate = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)
		case domain.Horizon7Days:
			offset := (int(localNow.Weekday()) + 6) % 7 // Monday start
			d := localNow.AddDate(0, 0, -offset)
			startDate = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
		default:
			startDate = time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, time.UTC)
		}
	}
	endDate := q.EndDate
	if endDate.IsZero() || !endDate.After(startDate) {
		switch horizon {
		case domain.HorizonDay:
			endDate = startDate.AddDate(0, 0, 1)
		case domain.Horizon7Days:
			endDate = startDate.AddDate(0, 0, 7)
		case domain.Horizon14Days:
			endDate = startDate.AddDate(0, 0, 14)
		case domain.Horizon30Days:
			endDate = startDate.AddDate(0, 0, 30)
		default:
			endDate = startDate.AddDate(0, 1, 0)
		}
	}
	// Cap window at 90 days to protect DB/rendering.
	if endDate.Sub(startDate) > 90*24*time.Hour {
		endDate = startDate.AddDate(0, 0, 90)
	}

	var dates []time.Time
	for cur := startDate; cur.Before(endDate); cur = cur.AddDate(0, 0, 1) {
		dates = append(dates, cur)
	}

	unitTypes, err := s.store.ListUnitTypes(ctx, q.PropertyID, false)
	if err != nil {
		return CalendarViewData{}, err
	}
	channels, err := s.store.ListBookingChannels(ctx, nil, q.PropertyID, true)
	if err != nil {
		return CalendarViewData{}, err
	}
	allUnits, err := s.store.ListUnits(ctx, q.PropertyID, q.UnitTypeID, false)
	if err != nil {
		return CalendarViewData{}, err
	}
	var units []domain.Unit
	for _, u := range allUnits {
		if u.Status != domain.UnitStatusRetired {
			units = append(units, u)
		}
	}
	if grouping == domain.GroupingUnitType {
		sort.SliceStable(units, func(i, j int) bool {
			if units[i].UnitTypeName != units[j].UnitTypeName {
				return units[i].UnitTypeName < units[j].UnitTypeName
			}
			return units[i].Code < units[j].Code
		})
	}

	claims, err := s.store.ListActiveClaimsInRange(ctx, q.PropertyID, startDate, endDate)
	if err != nil {
		return CalendarViewData{}, err
	}
	releaseRules, err := s.store.ListActiveReleaseRulesInRange(ctx, nil, q.PropertyID, startDate, endDate)
	if err != nil {
		return CalendarViewData{}, err
	}

	claimsByUnit := make(map[uuid.UUID][]domain.InventoryClaim)
	seenRes := make(map[uuid.UUID]struct{})
	seenGrp := make(map[uuid.UUID]struct{})
	channelFilter := strings.ToUpper(strings.TrimSpace(q.ChannelCode))

	for _, c := range claims {
		claimsByUnit[c.UnitID] = append(claimsByUnit[c.UnitID], c)
		if c.ReservationID != nil {
			seenRes[*c.ReservationID] = struct{}{}
		}
		if c.BlockoutGroupID != nil {
			seenGrp[*c.BlockoutGroupID] = struct{}{}
		}
	}

	evalChannel := channelFilter
	if evalChannel == "" {
		evalChannel = "ALL"
	}

	summary := CalendarSummary{
		ActiveReservations:   len(seenRes),
		ActiveBlockoutGroups: len(seenGrp),
	}

	rows := make([]CalendarUnitRow, 0, len(units))
	for _, u := range units {
		unitClaims := claimsByUnit[u.ID]
		cells := make([]CalendarDayCell, 0, len(dates))
		for _, day := range dates {
			summary.TotalUnitNights++
			cell := CalendarDayCell{
				Date:   domain.FormatDate(day),
				Status: "AVAILABLE",
				Reason: "Open for booking",
			}

			var physicalClaim *domain.InventoryClaim
			var channelRestrictions []*domain.InventoryClaim
			for i := range unitClaims {
				c := &unitClaims[i]
				if !day.Before(c.StartDate) && day.Before(c.EndDate) {
					if c.ClaimType == domain.ClaimChannelRestriction {
						channelRestrictions = append(channelRestrictions, c)
					} else if physicalClaim == nil {
						physicalClaim = c
					}
				}
			}

			if physicalClaim != nil {
				c := physicalClaim
				cid := c.ID
				cell.ClaimID = &cid
				cell.ClaimType = string(c.ClaimType)
				cell.ReservationID = c.ReservationID
				cell.BlockoutGroupID = c.BlockoutGroupID
				cell.Reference = c.ReservationRef
				cell.GuestName = c.GuestName
				cell.ReservationStatus = string(c.ReservationStat)
				cell.ChannelCode = c.ChannelScope
				cell.IsCheckIn = day.Equal(c.StartDate)
				cell.IsCheckOut = day.AddDate(0, 0, 1).Equal(c.EndDate)

				if c.ReservationID != nil {
					if c.ClaimType == domain.ClaimPendingReservation {
						cell.Status = "PENDING"
						cell.Reason = fmt.Sprintf("Pending reservation %s (%s)", c.ReservationRef, c.GuestName)
						summary.PendingNights++
					} else {
						cell.Status = "BOOKED"
						cell.Reason = fmt.Sprintf("Reservation %s (%s, %s)", c.ReservationRef, c.GuestName, c.ReservationStat)
						summary.BookedNights++
					}
				} else if c.ClaimType == domain.ClaimChannelAllocation {
					if evalChannel != "ALL" && c.ChannelScope != evalChannel {
						cell.Status = "BLOCKED"
						cell.Reason = fmt.Sprintf("Reserved under %s channel allocation (closed to %s)", c.ChannelScope, evalChannel)
						summary.BlockedNights++
					} else {
						cell.Status = "ALLOCATED"
						if c.Reason != "" {
							cell.Reason = fmt.Sprintf("Allocated exclusively to %s: %s", c.ChannelScope, c.Reason)
						} else {
							cell.Reason = fmt.Sprintf("Allocated exclusively to %s", c.ChannelScope)
						}
						summary.AllocatedNights++
					}
				} else {
					cell.Status = "BLOCKED"
					if c.Reason != "" {
						cell.Reason = fmt.Sprintf("%s: %s", c.ClaimType, c.Reason)
					} else {
						cell.Reason = string(c.ClaimType)
					}
					summary.BlockedNights++
				}
			} else {
				var matchedRestriction *domain.InventoryClaim
				restrictedChannels := make([]string, 0, len(channelRestrictions))
				for _, cr := range channelRestrictions {
					restrictedChannels = append(restrictedChannels, cr.ChannelScope)
					if evalChannel != "ALL" && cr.ChannelScope == evalChannel {
						matchedRestriction = cr
					}
				}
				if matchedRestriction != nil {
					cid := matchedRestriction.ID
					cell.ClaimID = &cid
					cell.ClaimType = string(matchedRestriction.ClaimType)
					cell.BlockoutGroupID = matchedRestriction.BlockoutGroupID
					cell.ChannelCode = matchedRestriction.ChannelScope
					cell.Status = "BLOCKED"
					if matchedRestriction.Reason != "" {
						cell.Reason = fmt.Sprintf("Stop-sell on %s: %s", matchedRestriction.ChannelScope, matchedRestriction.Reason)
					} else {
						cell.Reason = fmt.Sprintf("Stop-sell restriction on %s", matchedRestriction.ChannelScope)
					}
					summary.BlockedNights++
				} else {
					nextDay := day.AddDate(0, 0, 1)
					allowed, _, relReason := domain.EvaluateReleaseForStay(releaseRules, u, evalChannel, day, nextDay, now)
					if !allowed {
						cell.Status = "UNRELEASED"
						cell.Reason = relReason
						summary.UnreleasedNights++
					} else {
						if len(restrictedChannels) > 0 {
							cell.Reason = fmt.Sprintf("Open (stop-sell active on: %s)", strings.Join(restrictedChannels, ", "))
						}
						summary.AvailableNights++
					}
				}
			}

			cells = append(cells, cell)
		}
		rows = append(rows, CalendarUnitRow{
			Unit:  u,
			Cells: cells,
		})
	}

	return CalendarViewData{
		Property:       prop,
		View:           view,
		Horizon:        horizon,
		Grouping:       grouping,
		StartDate:      startDate,
		EndDate:        endDate,
		Dates:          dates,
		UnitTypes:      unitTypes,
		Channels:       channels,
		Units:          rows,
		ActiveReleases: releaseRules,
		Summary:        summary,
	}, nil
}
