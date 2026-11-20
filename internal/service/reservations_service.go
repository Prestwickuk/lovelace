package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/auth"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

func (s *Service) ListBookingChannels(ctx context.Context, actor Actor, propertyID uuid.UUID, activeOnly bool) ([]domain.BookingChannel, error) {
	if err := s.requirePerm(actor, auth.PermReservationsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListBookingChannels(ctx, nil, propertyID, activeOnly)
}

func (s *Service) CreateBookingChannel(ctx context.Context, actor Actor, c domain.BookingChannel) (domain.BookingChannel, error) {
	if err := s.requirePerm(actor, auth.PermReservationsWrite); err != nil {
		return domain.BookingChannel{}, err
	}
	if err := s.requirePropertyScope(actor, c.PropertyID); err != nil {
		return domain.BookingChannel{}, err
	}
	if err := c.Validate(); err != nil {
		return domain.BookingChannel{}, err
	}
	id, err := postgres.NewUUID()
	if err != nil {
		return domain.BookingChannel{}, err
	}
	c.ID = id
	c.IsActive = true

	var created domain.BookingChannel
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.store.GetPropertyByID(ctx, tx, c.PropertyID); err != nil {
			return err
		}
		var txErr error
		created, txErr = s.store.CreateBookingChannel(ctx, tx, c)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &c.PropertyID, "booking_channel.created", "booking_channel", created.ID.String(), nil, created)
	})
	return created, err
}

func (s *Service) ListReservations(ctx context.Context, actor Actor, propertyID uuid.UUID, filter postgres.ReservationFilter) ([]domain.Reservation, error) {
	if err := s.requirePerm(actor, auth.PermReservationsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListReservations(ctx, propertyID, filter)
}

func (s *Service) GetReservation(ctx context.Context, actor Actor, propertyID, id uuid.UUID) (domain.Reservation, error) {
	if err := s.requirePerm(actor, auth.PermReservationsRead); err != nil {
		return domain.Reservation{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.Reservation{}, err
	}
	return s.store.GetReservation(ctx, nil, propertyID, id)
}

func (s *Service) CreateReservation(ctx context.Context, actor Actor, r domain.Reservation) (domain.Reservation, error) {
	if err := s.requirePerm(actor, auth.PermReservationsWrite); err != nil {
		return domain.Reservation{}, err
	}
	if err := s.requirePropertyScope(actor, r.PropertyID); err != nil {
		return domain.Reservation{}, err
	}
	if r.Status == "" {
		r.Status = domain.ResStatusConfirmed
	}
	if err := r.Validate(); err != nil {
		return domain.Reservation{}, err
	}
	switch r.Status {
	case domain.ResStatusInquiry, domain.ResStatusPending, domain.ResStatusConfirmed, domain.ResStatusCheckedIn:
	default:
		return domain.Reservation{}, domain.NewValidationError(map[string]string{
			"status": "initial reservation status must be INQUIRY, PENDING, CONFIRMED, or CHECKED_IN",
		})
	}

	id, err := postgres.NewUUID()
	if err != nil {
		return domain.Reservation{}, err
	}
	r.ID = id
	uid := actor.UserID
	r.CreatedBy = &uid
	r.UpdatedBy = &uid

	var created domain.Reservation
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		prop, err := s.store.GetPropertyByID(ctx, tx, r.PropertyID)
		if err != nil {
			return err
		}
		if err := s.ensureActiveChannel(ctx, tx, r.PropertyID, r.ChannelCode); err != nil {
			return err
		}
		units, err := s.resolveAndValidateUnits(ctx, tx, &r)
		if err != nil {
			return err
		}
		if r.Status.BlocksInventory() {
			if err := s.checkReleaseRules(ctx, tx, r.PropertyID, units, r.ChannelCode, r.ArrivalDate, r.DepartureDate); err != nil {
				return err
			}
		}

		guest, err := s.store.FindOrCreateGuest(ctx, tx, r.PropertyID, r.GuestName, r.GuestEmail, r.GuestPhone)
		if err != nil {
			return err
		}
		r.GuestID = guest.ID

		created, err = s.store.CreateReservation(ctx, tx, r)
		if err != nil {
			return err
		}
		if err := s.store.SyncReservationAutomationJobs(ctx, tx, prop, created, time.Now().UTC()); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "reservation.created", "reservation", created.ID.String(), nil, auditReservationSnapshot(created))
	})
	return created, err
}

func (s *Service) UpdateReservation(ctx context.Context, actor Actor, r domain.Reservation) (domain.Reservation, error) {
	if err := s.requirePerm(actor, auth.PermReservationsWrite); err != nil {
		return domain.Reservation{}, err
	}
	if err := s.requirePropertyScope(actor, r.PropertyID); err != nil {
		return domain.Reservation{}, err
	}

	uid := actor.UserID
	r.UpdatedBy = &uid

	var updated domain.Reservation
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetReservation(ctx, tx, r.PropertyID, r.ID)
		if err != nil {
			return err
		}
		switch before.Status {
		case domain.ResStatusCancelled, domain.ResStatusCheckedOut, domain.ResStatusNoShow:
			return domain.NewValidationError(map[string]string{
				"status": fmt.Sprintf("cannot modify reservation in terminal status %s", before.Status),
			})
		}

		// Preserve status on edit; status transitions go through TransitionReservationStatus.
		r.Status = before.Status
		if err := r.Validate(); err != nil {
			return err
		}
		if err := s.ensureActiveChannel(ctx, tx, r.PropertyID, r.ChannelCode); err != nil {
			return err
		}
		units, err := s.resolveAndValidateUnits(ctx, tx, &r)
		if err != nil {
			return err
		}
		if r.Status.BlocksInventory() {
			if err := s.checkReleaseRules(ctx, tx, r.PropertyID, units, r.ChannelCode, r.ArrivalDate, r.DepartureDate); err != nil {
				return err
			}
		}
		if r.GuestEmail == "" && before.GuestID != uuid.Nil && strings.TrimSpace(r.GuestName) == strings.TrimSpace(before.GuestName) && strings.TrimSpace(r.GuestPhone) == strings.TrimSpace(before.GuestPhone) {
			r.GuestID = before.GuestID
		} else {
			guest, err := s.store.FindOrCreateGuest(ctx, tx, r.PropertyID, r.GuestName, r.GuestEmail, r.GuestPhone)
			if err != nil {
				return err
			}
			r.GuestID = guest.ID
		}

		updated, err = s.store.UpdateReservation(ctx, tx, r)
		if err != nil {
			return err
		}
		if prop, pErr := s.store.GetPropertyByID(ctx, tx, updated.PropertyID); pErr == nil {
			if jErr := s.store.SyncReservationAutomationJobs(ctx, tx, prop, updated, s.nowUTC()); jErr != nil {
				return jErr
			}
		}
		afterSnap := auditReservationSnapshot(updated)
		afterSnap["changed_fields"] = auditReservationChangedFields(before, updated)
		return s.recordAudit(ctx, tx, actor, &updated.PropertyID, "reservation.updated", "reservation", updated.ID.String(), auditReservationSnapshot(before), afterSnap)
	})
	return updated, err
}

func (s *Service) TransitionReservationStatus(
	ctx context.Context,
	actor Actor,
	propertyID, reservationID uuid.UUID,
	nextStatus domain.ReservationStatus,
	reason string,
	version int,
) (domain.Reservation, error) {
	if err := s.requirePerm(actor, auth.PermReservationsWrite); err != nil {
		return domain.Reservation{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.Reservation{}, err
	}
	nextStatus = domain.ReservationStatus(strings.TrimSpace(strings.ToUpper(string(nextStatus))))
	if !nextStatus.Valid() {
		return domain.Reservation{}, domain.NewValidationError(map[string]string{
			"status": "invalid reservation status",
		})
	}

	var updated domain.Reservation
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetReservation(ctx, tx, propertyID, reservationID)
		if err != nil {
			return err
		}
		if !before.Status.CanTransitionTo(nextStatus) {
			return domain.NewValidationError(map[string]string{
				"status": fmt.Sprintf("transition from %s to %s is not permitted", before.Status, nextStatus),
			})
		}

		// When transitioning from non-blocking INQUIRY to a blocking status, enforce occupancy and release rules.
		if !before.Status.BlocksInventory() && nextStatus.BlocksInventory() {
			units, err := s.resolveAndValidateUnits(ctx, tx, &before)
			if err != nil {
				return err
			}
			if err := s.checkReleaseRules(ctx, tx, propertyID, units, before.ChannelCode, before.ArrivalDate, before.DepartureDate); err != nil {
				return err
			}
		}

		var generatedTasks []domain.HousekeepingTask
		updated, generatedTasks, err = s.store.TransitionReservationStatusWithTasks(ctx, tx, propertyID, reservationID, nextStatus, reason, actor.UserID, version)
		if err != nil {
			return err
		}
		if prop, pErr := s.store.GetPropertyByID(ctx, tx, updated.PropertyID); pErr == nil {
			if jErr := s.store.SyncReservationAutomationJobs(ctx, tx, prop, updated, time.Now().UTC()); jErr != nil {
				return jErr
			}
		}
		if err := s.recordAudit(ctx, tx, actor, &propertyID, "reservation.status_changed", "reservation", updated.ID.String(), auditReservationSnapshot(before), auditReservationSnapshot(updated)); err != nil {
			return err
		}
		for _, task := range generatedTasks {
			if err := s.recordAudit(ctx, tx, actor, &propertyID, "housekeeping_task.auto_created", "housekeeping_task", task.ID.String(), nil, task); err != nil {
				return err
			}
		}
		return nil
	})
	return updated, err
}

func (s *Service) ensureActiveChannel(ctx context.Context, tx pgx.Tx, propertyID uuid.UUID, code string) error {
	channels, err := s.store.ListBookingChannels(ctx, tx, propertyID, true)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		if ch.Code == code {
			return nil
		}
	}
	return domain.NewValidationError(map[string]string{
		"channel_code": fmt.Sprintf("unknown or inactive booking channel %q", code),
	})
}

func (s *Service) resolveAndValidateUnits(ctx context.Context, tx pgx.Tx, r *domain.Reservation) ([]domain.Unit, error) {
	units := make([]domain.Unit, 0, len(r.UnitIDs))
	unitTypesByID := make(map[uuid.UUID]domain.UnitType, len(r.UnitIDs))
	for _, unitID := range r.UnitIDs {
		u, err := s.store.GetUnit(ctx, tx, r.PropertyID, unitID)
		if err != nil {
			if err == domain.ErrNotFound {
				return nil, domain.NewValidationError(map[string]string{
					"unit_ids": "one or more selected units do not exist, are archived, or do not belong to this property",
				})
			}
			return nil, err
		}
		if _, exists := unitTypesByID[u.UnitTypeID]; !exists {
			ut, err := s.store.GetUnitType(ctx, tx, r.PropertyID, u.UnitTypeID)
			if err != nil {
				if err == domain.ErrNotFound {
					return nil, domain.NewValidationError(map[string]string{
						"unit_ids": fmt.Sprintf("accommodation type for unit %s is archived or invalid", u.Code),
					})
				}
				return nil, err
			}
			unitTypesByID[u.UnitTypeID] = ut
		}
		units = append(units, u)
	}
	if err := r.ValidateOccupancy(units, unitTypesByID); err != nil {
		return nil, err
	}
	return units, nil
}

func (s *Service) checkReleaseRules(
	ctx context.Context,
	tx pgx.Tx,
	propertyID uuid.UUID,
	units []domain.Unit,
	channelCode string,
	startDate, endDate time.Time,
) error {
	rules, err := s.store.ListActiveReleaseRulesInRange(ctx, tx, propertyID, startDate, endDate)
	if err != nil {
		return err
	}
	now := s.nowUTC()
	for _, u := range units {
		allowed, _, reason := domain.EvaluateReleaseForStay(rules, u, channelCode, startDate, endDate, now)
		if !allowed {
			return fmt.Errorf("%w: unit %s (%s) — %s", domain.ErrReleaseRestricted, u.Code, u.Name, reason)
		}
	}
	return nil
}
