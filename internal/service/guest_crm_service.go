package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/mailer"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func (s *Service) todayUTC() time.Time {
	return s.nowUTC()
}

// --- Guest Directory, Profiles & Duplicate Merge ---

func (s *Service) ListGuests(ctx context.Context, actor Actor, propertyID uuid.UUID, filter domain.GuestDirectoryFilter) ([]domain.GuestListItem, error) {
	if err := s.requirePerm(actor, auth.PermGuestsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	items, err := s.store.ListGuests(ctx, propertyID, filter, s.nowUTC())
	if err != nil {
		return nil, err
	}
	canReadFin := auth.HasPermission(actor.Role, auth.PermRatesRead)
	canReadNotes := auth.HasPermission(actor.Role, auth.PermGuestsNotes)
	for i := range items {
		if !canReadFin {
			items[i].TotalRevenuePence = 0
		}
		if !canReadNotes {
			items[i].Notes = ""
		}
	}
	return items, nil
}

// GetGuestDetail loads the complete guest CRM profile filtered strictly by actor permissions.
func (s *Service) GetGuestDetail(ctx context.Context, actor Actor, propertyID, guestID uuid.UUID) (domain.GuestDetailBundle, error) {
	if err := s.requirePerm(actor, auth.PermGuestsRead); err != nil {
		return domain.GuestDetailBundle{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.GuestDetailBundle{}, err
	}

	canonicalID, err := s.store.ResolveCanonicalGuestID(ctx, nil, propertyID, guestID)
	if err != nil {
		return domain.GuestDetailBundle{}, err
	}
	item, err := s.store.GetGuestSummaryItem(ctx, nil, propertyID, canonicalID, s.nowUTC())
	if err != nil {
		return domain.GuestDetailBundle{}, err
	}
	reservations, err := s.store.ListGuestReservations(ctx, nil, propertyID, canonicalID)
	if err != nil {
		return domain.GuestDetailBundle{}, err
	}
	prefs, err := s.store.ListGuestPreferences(ctx, nil, propertyID, canonicalID)
	if err != nil {
		return domain.GuestDetailBundle{}, err
	}
	showFin := auth.HasPermission(actor.Role, auth.PermRatesRead)
	showNotes := auth.HasPermission(actor.Role, auth.PermGuestsNotes)
	showComms := auth.HasPermission(actor.Role, auth.PermCommsRead)
	showConsent := auth.HasPermission(actor.Role, auth.PermCommsRead) ||
		auth.HasPermission(actor.Role, auth.PermGuestsWrite) ||
		auth.HasPermission(actor.Role, auth.PermGuestsExport)
	showMerges := auth.HasPermission(actor.Role, auth.PermGuestsMerge)

	var enquiries []domain.GuestEnquiry
	if showComms {
		enquiries, err = s.store.ListGuestEnquiries(ctx, nil, propertyID, domain.GuestEnquiryFilter{GuestID: &canonicalID, Limit: 100})
		if err != nil {
			return domain.GuestDetailBundle{}, err
		}
	}

	consents, err := s.store.ListGuestConsentRecords(ctx, nil, propertyID, canonicalID)
	if err != nil {
		return domain.GuestDetailBundle{}, err
	}
	consentState := domain.ConsentNotSubscribed
	if len(consents) > 0 {
		consentState = consents[0].Status
	}
	if !showConsent {
		consents = nil
	}

	var merges []domain.GuestMergeEvent
	if showMerges {
		merges, err = s.store.ListGuestMergeHistory(ctx, nil, propertyID, canonicalID)
		if err != nil {
			return domain.GuestDetailBundle{}, err
		}
		if !showNotes {
			merges = redactMergeSnapshotNotes(merges)
		}
	}

	if !showFin {
		item.TotalRevenuePence = 0
		for i := range reservations {
			reservations[i].TotalPence = 0
			reservations[i].DepositPence = 0
			reservations[i].AmountPaidPence = 0
			reservations[i].BalanceDuePence = 0
		}
	}

	var notes []domain.GuestNote
	if showNotes {
		notes, err = s.store.ListGuestNotes(ctx, nil, propertyID, canonicalID)
		if err != nil {
			return domain.GuestDetailBundle{}, err
		}
	} else {
		item.Notes = ""
	}

	var comms []domain.GuestCommunication
	if showComms {
		comms, err = s.store.ListGuestCommunications(ctx, nil, propertyID, &canonicalID, nil, 150)
		if err != nil {
			return domain.GuestDetailBundle{}, err
		}
	}

	return domain.GuestDetailBundle{
		Guest:                 item,
		Reservations:          reservations,
		Preferences:           prefs,
		Notes:                 notes,
		Enquiries:             enquiries,
		Communications:        comms,
		ConsentHistory:        consents,
		CurrentMarketingState: consentState,
		MergeHistory:          merges,
		ShowFinancialTotals:   showFin,
		ShowInternalNotes:     showNotes,
		ShowCommunications:    showComms,
		ShowEnquiries:         showComms,
		ShowConsentHistory:    showConsent,
	}, nil
}

func redactMergeSnapshotNotes(events []domain.GuestMergeEvent) []domain.GuestMergeEvent {
	out := make([]domain.GuestMergeEvent, len(events))
	for i, ev := range events {
		out[i] = ev
		if len(ev.SourceSnap) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(ev.SourceSnap, &m); err != nil {
			continue
		}
		delete(m, "notes")
		if raw, err := json.Marshal(m); err == nil {
			out[i].SourceSnap = raw
		}
	}
	return out
}

func (s *Service) CreateGuest(ctx context.Context, actor Actor, g domain.Guest) (domain.Guest, error) {
	if err := s.requirePerm(actor, auth.PermGuestsWrite); err != nil {
		return domain.Guest{}, err
	}
	if err := s.requirePropertyScope(actor, g.PropertyID); err != nil {
		return domain.Guest{}, err
	}
	if _, _, full := domain.SplitFullName(g.FirstName, g.LastName, g.FullName); full == "" {
		return domain.Guest{}, domain.NewValidationError(map[string]string{"full_name": "guest name is required"})
	}
	if g.Status == "" {
		g.Status = domain.GuestStatusActive
	}
	if err := domain.ValidateOperationalGuestStatus(g.Status); err != nil {
		return domain.Guest{}, err
	}
	g.MergedIntoGuestID = nil
	g.MergedAt = nil
	g.AnonymizedAt = nil
	if strings.TrimSpace(g.Email) != "" {
		cleanEmail, err := domain.ValidateEmailRecipient(g.Email)
		if err != nil {
			return domain.Guest{}, domain.NewValidationError(map[string]string{"email": "must be a valid email address"})
		}
		g.Email = cleanEmail
	}
	uid := actor.UserID
	g.CreatedBy = &uid
	g.UpdatedBy = &uid

	var created domain.Guest
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.store.GetPropertyByID(ctx, tx, g.PropertyID); err != nil {
			return err
		}
		var txErr error
		created, txErr = s.store.CreateGuest(ctx, tx, g)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "guest.created", "guest", created.ID.String(), nil, auditGuestSnapshot(created))
	})
	return created, err
}

func (s *Service) UpdateGuest(ctx context.Context, actor Actor, g domain.Guest) (domain.Guest, error) {
	if err := s.requirePerm(actor, auth.PermGuestsWrite); err != nil {
		return domain.Guest{}, err
	}
	if err := s.requirePropertyScope(actor, g.PropertyID); err != nil {
		return domain.Guest{}, err
	}
	if _, _, full := domain.SplitFullName(g.FirstName, g.LastName, g.FullName); full == "" {
		return domain.Guest{}, domain.NewValidationError(map[string]string{"full_name": "guest name is required"})
	}
	if strings.TrimSpace(g.Email) != "" {
		cleanEmail, err := domain.ValidateEmailRecipient(g.Email)
		if err != nil {
			return domain.Guest{}, domain.NewValidationError(map[string]string{"email": "must be a valid email address"})
		}
		g.Email = cleanEmail
	}
	uid := actor.UserID
	g.UpdatedBy = &uid

	if g.Status != "" {
		if err := domain.ValidateOperationalGuestStatus(g.Status); err != nil {
			return domain.Guest{}, err
		}
	}

	var updated domain.Guest
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetGuest(ctx, tx, g.PropertyID, g.ID)
		if err != nil {
			return err
		}
		if before.Status == domain.GuestStatusMerged || before.Status == domain.GuestStatusAnonymized {
			return fmt.Errorf("%w: %s guest profile cannot be updated via generic edit", domain.ErrConflict, before.Status)
		}
		if g.Status == "" {
			g.Status = before.Status
		}
		if err := domain.ValidateOperationalGuestStatus(g.Status); err != nil {
			return err
		}
		updated, err = s.store.UpdateGuest(ctx, tx, g)
		if err != nil {
			return err
		}
		afterSnap := auditGuestSnapshot(updated)
		afterSnap["changed_fields"] = auditGuestChangedFields(before, updated)
		return s.recordAudit(ctx, tx, actor, &updated.PropertyID, "guest.updated", "guest", updated.ID.String(), auditGuestSnapshot(before), afterSnap)
	})
	return updated, err
}

func (s *Service) ListDuplicateCandidates(ctx context.Context, actor Actor, propertyID uuid.UUID) (domain.DuplicateScanResult, error) {
	if err := s.requirePerm(actor, auth.PermGuestsRead); err != nil {
		return domain.DuplicateScanResult{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.DuplicateScanResult{}, err
	}
	return s.store.DetectGuestDuplicates(ctx, propertyID, s.nowUTC())
}

func (s *Service) DismissGuestDuplicate(ctx context.Context, actor Actor, propertyID, guestA, guestB uuid.UUID, reason string) error {
	return s.DismissGuestDuplicateWithCategory(ctx, actor, propertyID, guestA, guestB, "NOT_DUPLICATE", reason)
}

func (s *Service) DismissGuestDuplicateWithCategory(ctx context.Context, actor Actor, propertyID, guestA, guestB uuid.UUID, category, reason string) error {
	if err := s.requirePerm(actor, auth.PermGuestsMerge); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.DismissGuestDuplicateWithCategory(ctx, tx, propertyID, guestA, guestB, category, reason, actor.UserID); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "guest.duplicate_dismissed", "guest", guestA.String(), nil, map[string]any{
			"guest_a_id":      guestA,
			"guest_b_id":      guestB,
			"reason_category": category,
			"has_reason":      strings.TrimSpace(reason) != "",
		})
	})
}

func (s *Service) MergeGuests(ctx context.Context, actor Actor, propertyID, sourceID, targetID uuid.UUID, notes string) (domain.Guest, domain.GuestMergeEvent, error) {
	if err := s.requirePerm(actor, auth.PermGuestsMerge); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.Guest{}, domain.GuestMergeEvent{}, err
	}
	var target domain.Guest
	var ev domain.GuestMergeEvent
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		target, ev, txErr = s.store.MergeGuests(ctx, tx, propertyID, sourceID, targetID, notes, actor.UserID)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "guest.merged", "guest", target.ID.String(), map[string]any{"source_guest_id": sourceID}, auditMergeEventSnapshot(ev))
	})
	return target, ev, err
}

// --- Preferences & Internal Notes ---

func (s *Service) AddGuestPreference(ctx context.Context, actor Actor, p domain.GuestPreference) (domain.GuestPreference, error) {
	if err := s.requirePerm(actor, auth.PermGuestsWrite); err != nil {
		return domain.GuestPreference{}, err
	}
	if err := s.requirePropertyScope(actor, p.PropertyID); err != nil {
		return domain.GuestPreference{}, err
	}
	if strings.TrimSpace(p.PreferenceKey) == "" {
		return domain.GuestPreference{}, domain.NewValidationError(map[string]string{"preference_key": "is required"})
	}
	if p.Category == "" {
		p.Category = domain.PrefCatOther
	}
	uid := actor.UserID
	p.CreatedBy = &uid
	p.UpdatedBy = &uid
	var out domain.GuestPreference
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		canonID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, p.PropertyID, p.GuestID)
		if err != nil {
			return err
		}
		if anonOrPending {
			return fmt.Errorf("%w: cannot modify preferences on an anonymized or erasure-pending guest", domain.ErrConflict)
		}
		p.GuestID = canonID
		var txErr error
		out, txErr = s.store.CreateGuestPreference(ctx, tx, p)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &p.PropertyID, "guest_preference.created", "guest_preference", out.ID.String(), nil, auditPreferenceSnapshot(out))
	})
	return out, err
}

func (s *Service) RemoveGuestPreference(ctx context.Context, actor Actor, propertyID, guestID, prefID uuid.UUID) error {
	if err := s.requirePerm(actor, auth.PermGuestsWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.DeleteGuestPreference(ctx, tx, propertyID, guestID, prefID); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "guest_preference.deleted", "guest_preference", prefID.String(), nil, nil)
	})
}

func (s *Service) AddGuestNote(ctx context.Context, actor Actor, n domain.GuestNote) (domain.GuestNote, error) {
	if err := s.requirePerm(actor, auth.PermGuestsNotes); err != nil {
		return domain.GuestNote{}, err
	}
	if err := s.requirePerm(actor, auth.PermGuestsWrite); err != nil {
		return domain.GuestNote{}, err
	}
	if err := s.requirePropertyScope(actor, n.PropertyID); err != nil {
		return domain.GuestNote{}, err
	}
	if strings.TrimSpace(n.Content) == "" {
		return domain.GuestNote{}, domain.NewValidationError(map[string]string{"content": "note content is required"})
	}
	if n.Category == "" {
		n.Category = domain.NoteCatGeneral
	}
	uid := actor.UserID
	n.CreatedBy = &uid
	n.UpdatedBy = &uid
	var out domain.GuestNote
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		canonicalID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, n.PropertyID, n.GuestID)
		if err != nil {
			return err
		}
		if anonOrPending {
			return fmt.Errorf("%w: cannot add notes to an anonymized or erasure-pending guest", domain.ErrConflict)
		}
		n.GuestID = canonicalID
		if err := s.store.ValidateGuestReservationLinkTx(ctx, tx, n.PropertyID, &n.GuestID, n.ReservationID); err != nil {
			return err
		}
		var txErr error
		out, txErr = s.store.CreateGuestNote(ctx, tx, n)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &n.PropertyID, "guest_note.created", "guest_note", out.ID.String(), nil, auditNoteSnapshot(out))
	})
	return out, err
}

func (s *Service) RemoveGuestNote(ctx context.Context, actor Actor, propertyID, guestID, noteID uuid.UUID) error {
	if err := s.requirePerm(actor, auth.PermGuestsNotes); err != nil {
		return err
	}
	if err := s.requirePerm(actor, auth.PermGuestsWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.DeleteGuestNote(ctx, tx, propertyID, guestID, noteID); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "guest_note.deleted", "guest_note", noteID.String(), nil, nil)
	})
}

// --- Guest Enquiries, Requests & Complaints ---

func (s *Service) ListGuestEnquiries(ctx context.Context, actor Actor, propertyID uuid.UUID, f domain.GuestEnquiryFilter) ([]domain.GuestEnquiry, error) {
	if err := s.requirePerm(actor, auth.PermCommsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListGuestEnquiries(ctx, nil, propertyID, f)
}

func (s *Service) GetGuestEnquiryDetail(ctx context.Context, actor Actor, propertyID, enquiryID uuid.UUID) (domain.GuestEnquiry, []domain.GuestEnquiryMessage, error) {
	if err := s.requirePerm(actor, auth.PermCommsRead); err != nil {
		return domain.GuestEnquiry{}, nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.GuestEnquiry{}, nil, err
	}
	enq, err := s.store.GetGuestEnquiry(ctx, nil, propertyID, enquiryID)
	if err != nil {
		return domain.GuestEnquiry{}, nil, err
	}
	msgs, err := s.store.ListGuestEnquiryMessages(ctx, nil, propertyID, enquiryID)
	if err != nil {
		return domain.GuestEnquiry{}, nil, err
	}
	if !auth.HasPermission(actor.Role, auth.PermGuestsNotes) {
		filtered := msgs[:0]
		for _, m := range msgs {
			if !m.IsInternal && m.Direction != domain.CommDirInternal {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}
	return enq, msgs, nil
}

func (s *Service) CreateGuestEnquiry(ctx context.Context, actor Actor, e domain.GuestEnquiry) (domain.GuestEnquiry, error) {
	if err := s.requirePerm(actor, auth.PermCommsWrite); err != nil {
		return domain.GuestEnquiry{}, err
	}
	if err := s.requirePropertyScope(actor, e.PropertyID); err != nil {
		return domain.GuestEnquiry{}, err
	}
	if strings.TrimSpace(e.Subject) == "" {
		return domain.GuestEnquiry{}, domain.NewValidationError(map[string]string{"subject": "enquiry subject is required"})
	}
	if e.Category == "" {
		e.Category = domain.EnqCatGeneral
	}
	uid := actor.UserID
	e.CreatedBy = &uid
	e.UpdatedBy = &uid

	var created domain.GuestEnquiry
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if e.GuestID == nil && e.ReservationID == nil && strings.TrimSpace(e.RequesterName) != "" && strings.TrimSpace(e.RequesterEmail) != "" {
			g, gErr := s.store.FindOrCreateGuest(ctx, tx, e.PropertyID, e.RequesterName, e.RequesterEmail, e.RequesterPhone)
			if gErr != nil {
				return gErr
			}
			e.GuestID = &g.ID
		}
		var lockGuestID uuid.UUID
		if e.GuestID != nil {
			canonID, _, anonOrPending, gErr := s.store.LockCanonicalGuestClusterTx(ctx, tx, e.PropertyID, *e.GuestID)
			if gErr != nil {
				return gErr
			}
			if anonOrPending {
				return fmt.Errorf("%w: cannot create enquiry for an anonymized or erasure-pending guest", domain.ErrConflict)
			}
			e.GuestID = &canonID
			lockGuestID = canonID
		} else if e.ReservationID != nil {
			resGuestID, rErr := s.store.ResolveGuestForReservationTx(ctx, tx, e.PropertyID, *e.ReservationID)
			if rErr != nil {
				return rErr
			}
			_, _, anonOrPending, gErr := s.store.LockCanonicalGuestClusterTx(ctx, tx, e.PropertyID, resGuestID)
			if gErr != nil {
				return gErr
			}
			if anonOrPending {
				return fmt.Errorf("%w: cannot create enquiry for an anonymized or erasure-pending guest reservation", domain.ErrConflict)
			}
			lockGuestID = resGuestID
		}
		_ = lockGuestID
		if err := s.store.ValidateGuestReservationLinkTx(ctx, tx, e.PropertyID, e.GuestID, e.ReservationID); err != nil {
			return err
		}
		var txErr error
		created, txErr = s.store.CreateGuestEnquiry(ctx, tx, e)
		if txErr != nil {
			return txErr
		}
		if created.GuestID != nil {
			intType := domain.InteractionWebsiteEnquiry
			if created.Category == domain.EnqCatComplaint {
				intType = domain.InteractionComplaint
			}
			if _, cErr := s.store.CreateGuestCommunication(ctx, tx, domain.GuestCommunication{
				PropertyID:      created.PropertyID,
				GuestID:         created.GuestID,
				ReservationID:   created.ReservationID,
				EnquiryID:       &created.ID,
				InteractionType: intType,
				Direction:       domain.CommDirInbound,
				Subject:         fmt.Sprintf("[%s] %s", created.Reference, created.Subject),
				BodyText:        created.Description,
				Summary:         fmt.Sprintf("%s logged via %s", created.Category, created.Source),
				DeliveryStatus:  domain.DeliveryLogged,
				StaffUserID:     &uid,
			}); cErr != nil {
				return cErr
			}
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "guest_enquiry.created", "guest_enquiry", created.ID.String(), nil, auditEnquirySnapshot(created))
	})
	return created, err
}

func (s *Service) UpdateGuestEnquiry(ctx context.Context, actor Actor, e domain.GuestEnquiry) (domain.GuestEnquiry, error) {
	if err := s.requirePerm(actor, auth.PermCommsWrite); err != nil {
		return domain.GuestEnquiry{}, err
	}
	if err := s.requirePropertyScope(actor, e.PropertyID); err != nil {
		return domain.GuestEnquiry{}, err
	}
	uid := actor.UserID
	e.UpdatedBy = &uid

	var updated domain.GuestEnquiry
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetGuestEnquiry(ctx, tx, e.PropertyID, e.ID)
		if err != nil {
			return err
		}
		var targetGuest uuid.UUID
		if e.GuestID != nil {
			canonID, _, anonOrPending, gErr := s.store.LockCanonicalGuestClusterTx(ctx, tx, e.PropertyID, *e.GuestID)
			if gErr != nil {
				return gErr
			}
			if anonOrPending {
				return fmt.Errorf("%w: cannot update enquiry on an anonymized or erasure-pending guest", domain.ErrConflict)
			}
			e.GuestID = &canonID
			targetGuest = canonID
		} else if e.ReservationID != nil {
			resGuestID, rErr := s.store.ResolveGuestForReservationTx(ctx, tx, e.PropertyID, *e.ReservationID)
			if rErr != nil {
				return rErr
			}
			targetGuest = resGuestID
		} else if before.GuestID != nil {
			targetGuest = *before.GuestID
		} else if before.ReservationID != nil {
			targetGuest, _ = s.store.ResolveGuestForReservationTx(ctx, tx, e.PropertyID, *before.ReservationID)
		}
		if targetGuest != uuid.Nil && e.GuestID == nil {
			_, _, anonOrPending, lErr := s.store.LockCanonicalGuestClusterTx(ctx, tx, e.PropertyID, targetGuest)
			if lErr != nil {
				return lErr
			}
			if anonOrPending {
				return fmt.Errorf("%w: cannot update enquiry on an anonymized or erasure-pending guest", domain.ErrConflict)
			}
		}
		if err := s.store.ValidateGuestReservationLinkTx(ctx, tx, e.PropertyID, e.GuestID, e.ReservationID); err != nil {
			return err
		}
		if e.Status == domain.EnqStatusResolved && before.ResolvedAt == nil {
			now := s.nowUTC()
			e.ResolvedAt = &now
			e.ResolvedBy = &uid
		}
		if e.Status == domain.EnqStatusClosed && before.ClosedAt == nil {
			now := s.nowUTC()
			e.ClosedAt = &now
			e.ClosedBy = &uid
		}
		if e.Status == domain.EnqStatusInProgress || e.Status == domain.EnqStatusTriaged || e.Status == domain.EnqStatusNew {
			e.ResolvedAt = nil
			e.ResolvedBy = nil
			e.ClosedAt = nil
			e.ClosedBy = nil
		}
		updated, err = s.store.UpdateGuestEnquiry(ctx, tx, e)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &updated.PropertyID, "guest_enquiry.updated", "guest_enquiry", updated.ID.String(), auditEnquirySnapshot(before), auditEnquirySnapshot(updated))
	})
	return updated, err
}

func (s *Service) AddGuestEnquiryMessage(ctx context.Context, actor Actor, m domain.GuestEnquiryMessage) (domain.GuestEnquiryMessage, error) {
	if err := s.requirePerm(actor, auth.PermCommsWrite); err != nil {
		return domain.GuestEnquiryMessage{}, err
	}
	if err := s.requirePropertyScope(actor, m.PropertyID); err != nil {
		return domain.GuestEnquiryMessage{}, err
	}
	if strings.TrimSpace(m.Body) == "" {
		return domain.GuestEnquiryMessage{}, domain.NewValidationError(map[string]string{"body": "message body is required"})
	}
	uid := actor.UserID
	m.StaffUserID = &uid
	if m.SenderName == "" {
		m.SenderName = actor.FullName
	}
	if m.IsInternal {
		if m.Direction != "" && m.Direction != domain.CommDirInternal {
			return domain.GuestEnquiryMessage{}, domain.NewValidationError(map[string]string{
				"direction": "internal enquiry notes must use INTERNAL direction",
			})
		}
		m.Direction = domain.CommDirInternal
	} else {
		if m.Direction == domain.CommDirInternal {
			return domain.GuestEnquiryMessage{}, domain.NewValidationError(map[string]string{
				"is_internal": "INTERNAL direction messages must set is_internal=true",
			})
		}
		if m.Direction == "" {
			m.Direction = domain.CommDirOutbound
		}
	}
	var out domain.GuestEnquiryMessage
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		anonOrPending, err := s.store.LockGuestByEnquiryTx(ctx, tx, m.PropertyID, m.EnquiryID)
		if err != nil {
			return err
		}
		if anonOrPending {
			return fmt.Errorf("%w: cannot add message to an anonymized or erasure-pending guest enquiry", domain.ErrConflict)
		}
		var txErr error
		out, txErr = s.store.AddGuestEnquiryMessage(ctx, tx, m)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &m.PropertyID, "guest_enquiry_message.created", "guest_enquiry", m.EnquiryID.String(), nil, auditEnquiryMessageSnapshot(out))
	})
	return out, err
}

// --- Manual Communication Logging, Sending & Delivery Lifecycle ---

func (s *Service) LogGuestInteraction(ctx context.Context, actor Actor, c domain.GuestCommunication) (domain.GuestCommunication, error) {
	if err := s.requirePerm(actor, auth.PermCommsWrite); err != nil {
		return domain.GuestCommunication{}, err
	}
	if err := s.requirePropertyScope(actor, c.PropertyID); err != nil {
		return domain.GuestCommunication{}, err
	}
	if strings.TrimSpace(c.Summary) == "" && strings.TrimSpace(c.BodyText) == "" {
		return domain.GuestCommunication{}, domain.NewValidationError(map[string]string{"summary": "summary or notes are required"})
	}
	uid := actor.UserID
	c.StaffUserID = &uid
	c.DeliveryStatus = domain.DeliveryLogged
	if c.InteractionType == "" {
		c.InteractionType = domain.InteractionPhoneCall
	}
	if c.Direction == "" {
		c.Direction = domain.CommDirInbound
	}
	var created domain.GuestCommunication
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if c.GuestID != nil {
			canonID, _, anonOrPending, gErr := s.store.LockCanonicalGuestClusterTx(ctx, tx, c.PropertyID, *c.GuestID)
			if gErr != nil {
				return gErr
			}
			if anonOrPending {
				return fmt.Errorf("%w: cannot log interaction for an anonymized or erasure-pending guest", domain.ErrConflict)
			}
			c.GuestID = &canonID
		} else if c.ReservationID != nil {
			resGuestID, rErr := s.store.ResolveGuestForReservationTx(ctx, tx, c.PropertyID, *c.ReservationID)
			if rErr != nil {
				return rErr
			}
			_, _, anonOrPending, gErr := s.store.LockCanonicalGuestClusterTx(ctx, tx, c.PropertyID, resGuestID)
			if gErr != nil {
				return gErr
			}
			if anonOrPending {
				return fmt.Errorf("%w: cannot log interaction for an anonymized or erasure-pending guest reservation", domain.ErrConflict)
			}
		} else if c.EnquiryID != nil {
			anonOrPending, eErr := s.store.LockGuestByEnquiryTx(ctx, tx, c.PropertyID, *c.EnquiryID)
			if eErr != nil {
				return eErr
			}
			if anonOrPending {
				return fmt.Errorf("%w: cannot log interaction for an anonymized or erasure-pending guest enquiry", domain.ErrConflict)
			}
		}
		if err := s.store.ValidateGuestReservationLinkTx(ctx, tx, c.PropertyID, c.GuestID, c.ReservationID); err != nil {
			return err
		}
		var txErr error
		created, txErr = s.store.CreateGuestCommunication(ctx, tx, c)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "guest_communication.logged", "guest_communication", created.ID.String(), nil, auditCommunicationSnapshot(created))
	})
	return created, err
}

func (s *Service) CompleteCommunicationFollowUp(ctx context.Context, actor Actor, propertyID, commID uuid.UUID) error {
	if err := s.requirePerm(actor, auth.PermCommsWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.CompleteCommunicationFollowUp(ctx, tx, propertyID, commID); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "guest_communication.followup_completed", "guest_communication", commID.String(), nil, nil)
	})
}

type SendGuestEmailInput struct {
	PropertyID    uuid.UUID
	GuestID       uuid.UUID
	ReservationID *uuid.UUID
	EnquiryID     *uuid.UUID
	TemplateID    *uuid.UUID
	Recipient     string
	Subject       string
	BodyText      string
	BodyHTML      string
}

// SendGuestEmail validates recipients, prevents SMTP header injection and internal note leaks,
// renders optional templates from their latest immutable version, and queues via email_outbox.
func (s *Service) SendGuestEmail(ctx context.Context, actor Actor, in SendGuestEmailInput) (domain.GuestCommunication, error) {
	if err := s.requirePerm(actor, auth.PermCommsWrite); err != nil {
		return domain.GuestCommunication{}, err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return domain.GuestCommunication{}, err
	}

	if s.beforeSendGuestLockHook != nil {
		s.beforeSendGuestLockHook(in.GuestID)
	}

	var out domain.GuestCommunication
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		prop, err := s.store.GetPropertyByID(ctx, tx, in.PropertyID)
		if err != nil {
			return err
		}
		canonGuestID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, in.PropertyID, in.GuestID)
		if err != nil {
			return err
		}
		if s.afterSendGuestLockHook != nil {
			s.afterSendGuestLockHook(canonGuestID)
		}
		if anonOrPending {
			return fmt.Errorf("%w: cannot email an anonymized or erasure-pending guest profile", domain.ErrConflict)
		}
		guest, err := s.store.GetGuest(ctx, tx, in.PropertyID, canonGuestID)
		if err != nil {
			return err
		}
		if guest.Status == domain.GuestStatusAnonymized || guest.AnonymizedAt != nil {
			return fmt.Errorf("%w: cannot email an anonymized guest profile", domain.ErrConflict)
		}

		recEmail := strings.TrimSpace(in.Recipient)
		if recEmail == "" {
			recEmail = guest.Email
		}
		recipient, err := domain.ValidateEmailRecipient(recEmail)
		if err != nil {
			return domain.NewValidationError(map[string]string{"recipient": err.Error()})
		}

		var resPtr *domain.Reservation
		var unitNames []string
		if in.ReservationID != nil {
			if err := s.store.ValidateGuestReservationLinkTx(ctx, tx, in.PropertyID, &guest.ID, in.ReservationID); err != nil {
				return err
			}
			res, rErr := s.store.GetReservation(ctx, tx, in.PropertyID, *in.ReservationID)
			if rErr != nil {
				return rErr
			}
			resPtr = &res
			for _, u := range res.Units {
				unitNames = append(unitNames, u.UnitName)
			}
		}
		var enqPtr *domain.GuestEnquiry
		if in.EnquiryID != nil {
			enq, eErr := s.store.GetGuestEnquiry(ctx, tx, in.PropertyID, *in.EnquiryID)
			if eErr != nil {
				return eErr
			}
			if enq.GuestID == nil && enq.ReservationID == nil {
				return domain.NewValidationError(map[string]string{
					"enquiry_id": "enquiry is not associated with this guest profile",
				})
			}
			if enq.GuestID != nil {
				enqGuestCanon, cErr := s.store.ResolveCanonicalGuestID(ctx, tx, in.PropertyID, *enq.GuestID)
				if cErr != nil {
					return cErr
				}
				if enqGuestCanon != guest.ID {
					return domain.NewValidationError(map[string]string{
						"enquiry_id": "enquiry belongs to a different guest profile",
					})
				}
			}
			if enq.ReservationID != nil {
				if err := s.store.ValidateGuestReservationLinkTx(ctx, tx, in.PropertyID, &guest.ID, enq.ReservationID); err != nil {
					return domain.NewValidationError(map[string]string{
						"enquiry_id": "enquiry reservation belongs to a different guest profile",
					})
				}
				if in.ReservationID != nil && *enq.ReservationID != *in.ReservationID {
					return domain.NewValidationError(map[string]string{
						"enquiry_id": "enquiry belongs to a different reservation",
					})
				}
				if resPtr == nil {
					res, rErr := s.store.GetReservation(ctx, tx, in.PropertyID, *enq.ReservationID)
					if rErr != nil {
						return rErr
					}
					resPtr = &res
					for _, u := range res.Units {
						unitNames = append(unitNames, u.UnitName)
					}
				}
			}
			enqPtr = &enq
		}

		subj := strings.TrimSpace(in.Subject)
		txt := strings.TrimSpace(in.BodyText)
		htm := strings.TrimSpace(in.BodyHTML)
		tplCode := "direct_message"
		var tplVerID *uuid.UUID

		if in.TemplateID != nil {
			tpl, tErr := s.store.GetCommunicationTemplate(ctx, tx, in.PropertyID, *in.TemplateID)
			if tErr != nil {
				return tErr
			}
			ver, vErr := s.store.GetLatestTemplateVersion(ctx, tx, in.PropertyID, tpl.ID)
			if vErr != nil {
				return vErr
			}
			tplCode = strings.ToLower(tpl.Code)
			tplVerID = &ver.ID
			vars := domain.BuildReservationTemplateVars(prop, guest, resPtr, unitNames, enqPtr)
			if subj == "" || txt == "" {
				rSubj, rTxt, rHtm, rErr := domain.RenderTemplate(ver.SubjectTemplate, ver.BodyTextTemplate, ver.BodyHTMLTemplate, vars)
				if rErr != nil {
					return rErr
				}
				if subj == "" {
					subj = rSubj
				}
				if txt == "" {
					txt = rTxt
				}
				if htm == "" {
					htm = rHtm
				}
			}
		}

		cleanSubj, err := domain.SanitizeEmailHeaderValue(subj)
		if err != nil || cleanSubj == "" {
			return domain.NewValidationError(map[string]string{"subject": "valid subject without line breaks is required"})
		}
		if txt == "" {
			return domain.NewValidationError(map[string]string{"body_text": "email body is required"})
		}
		if _, err := mailer.ValidateMessage(mailer.Message{
			From:     s.cfg.EmailFrom,
			To:       recipient,
			Subject:  cleanSubj,
			BodyText: txt,
			BodyHTML: htm,
		}); err != nil {
			return err
		}

		commID, err := postgres.NewUUID()
		if err != nil {
			return err
		}
		outboxID, err := postgres.NewUUID()
		if err != nil {
			return err
		}
		uid := actor.UserID
		out, err = s.store.CreateGuestCommunication(ctx, tx, domain.GuestCommunication{
			ID:                commID,
			PropertyID:        in.PropertyID,
			GuestID:           &guest.ID,
			ReservationID:     in.ReservationID,
			EnquiryID:         in.EnquiryID,
			InteractionType:   domain.InteractionEmailOutbound,
			Direction:         domain.CommDirOutbound,
			Subject:           cleanSubj,
			BodyText:          txt,
			BodyHTML:          htm,
			Summary:           "Outbound email queued by " + actor.FullName,
			RecipientEmail:    recipient,
			SenderEmail:       s.cfg.EmailFrom,
			TemplateID:        in.TemplateID,
			TemplateVersionID: tplVerID,
			DeliveryStatus:    domain.DeliveryQueued,
			StaffUserID:       &uid,
			OccurredAt:        s.nowUTC(),
		})
		if err != nil {
			return err
		}
		if _, err := s.store.QueueEmail(ctx, tx, domain.EmailOutboxItem{
			ID:              outboxID,
			PropertyID:      in.PropertyID,
			ReservationID:   nil, // allow multiple direct CRM emails per reservation safely
			GuestID:         &guest.ID,
			CommunicationID: &commID,
			RecipientEmail:  recipient,
			Template:        tplCode,
			Subject:         cleanSubj,
			BodyText:        txt,
			BodyHTML:        htm,
			NextAttemptAt:   s.nowUTC(),
		}); err != nil {
			return err
		}
		if err := s.store.LinkCommunicationOutbox(ctx, tx, commID, outboxID); err != nil {
			return err
		}
		out.EmailOutboxID = &outboxID
		return s.recordAudit(ctx, tx, actor, &in.PropertyID, "guest_communication.email_queued", "guest_communication", commID.String(), nil, map[string]any{
			"guest_id":        guest.ID,
			"reservation_id":  in.ReservationID,
			"enquiry_id":      in.EnquiryID,
			"template_id":     in.TemplateID,
			"outbox_id":       outboxID,
			"delivery_status": domain.DeliveryQueued,
		})
	})
	return out, err
}

func (s *Service) PreviewCommunicationTemplate(ctx context.Context, actor Actor, propertyID, templateID uuid.UUID, guestID, reservationID *uuid.UUID) (subject, bodyText, bodyHTML string, err error) {
	return s.PreviewCommunicationTemplateWithEnquiry(ctx, actor, propertyID, templateID, guestID, reservationID, nil)
}

func (s *Service) PreviewCommunicationTemplateWithEnquiry(ctx context.Context, actor Actor, propertyID, templateID uuid.UUID, guestID, reservationID, enquiryID *uuid.UUID) (subject, bodyText, bodyHTML string, err error) {
	if err := s.requirePerm(actor, auth.PermCommsRead); err != nil {
		return "", "", "", err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return "", "", "", err
	}
	prop, err := s.store.GetPropertyByID(ctx, nil, propertyID)
	if err != nil {
		return "", "", "", err
	}
	tpl, err := s.store.GetCommunicationTemplate(ctx, nil, propertyID, templateID)
	if err != nil {
		return "", "", "", err
	}
	guest := domain.Guest{FirstName: "Sian", LastName: "Davies", FullName: "Sian Davies", Email: "sian@example.wales"}
	var resolvedGuestID *uuid.UUID
	if guestID != nil {
		canonID, gErr := s.store.ResolveCanonicalGuestID(ctx, nil, propertyID, *guestID)
		if gErr != nil {
			return "", "", "", gErr
		}
		g, gErr := s.store.GetGuest(ctx, nil, propertyID, canonID)
		if gErr != nil {
			return "", "", "", gErr
		}
		if g.Status == domain.GuestStatusAnonymized {
			return "", "", "", fmt.Errorf("%w: cannot preview template for an anonymized guest", domain.ErrConflict)
		}
		guest = g
		resolvedGuestID = &canonID
	}

	var enqPtr *domain.GuestEnquiry
	if enquiryID != nil {
		enq, eErr := s.store.GetGuestEnquiry(ctx, nil, propertyID, *enquiryID)
		if eErr != nil {
			return "", "", "", eErr
		}
		if resolvedGuestID != nil && enq.GuestID == nil && enq.ReservationID == nil {
			return "", "", "", domain.NewValidationError(map[string]string{
				"enquiry_id": "enquiry is not associated with the selected guest profile",
			})
		}
		if enq.GuestID != nil {
			enqCanon, cErr := s.store.ResolveCanonicalGuestID(ctx, nil, propertyID, *enq.GuestID)
			if cErr != nil {
				return "", "", "", cErr
			}
			if resolvedGuestID != nil && enqCanon != *resolvedGuestID {
				return "", "", "", domain.NewValidationError(map[string]string{
					"enquiry_id": "enquiry belongs to a different guest profile",
				})
			}
			if resolvedGuestID == nil {
				eg, gErr := s.store.GetGuest(ctx, nil, propertyID, enqCanon)
				if gErr != nil {
					return "", "", "", gErr
				}
				if eg.Status == domain.GuestStatusAnonymized {
					return "", "", "", fmt.Errorf("%w: cannot preview template for an anonymized guest", domain.ErrConflict)
				}
				guest = eg
				resolvedGuestID = &enqCanon
			}
		}
		if enq.ReservationID != nil {
			if resolvedGuestID != nil {
				if err := s.store.ValidateGuestReservationLinkTx(ctx, nil, propertyID, resolvedGuestID, enq.ReservationID); err != nil {
					return "", "", "", domain.NewValidationError(map[string]string{
						"enquiry_id": "enquiry reservation belongs to a different guest profile",
					})
				}
			}
			if reservationID != nil && *enq.ReservationID != *reservationID {
				return "", "", "", domain.NewValidationError(map[string]string{
					"enquiry_id": "enquiry belongs to a different reservation",
				})
			}
			if reservationID == nil {
				reservationID = enq.ReservationID
			}
		}
		enqPtr = &enq
	}

	var resPtr *domain.Reservation
	unitNames := []string{"Preseli Shepherd's Hut 1"}
	if reservationID != nil {
		if resolvedGuestID != nil {
			if err := s.store.ValidateGuestReservationLinkTx(ctx, nil, propertyID, resolvedGuestID, reservationID); err != nil {
				return "", "", "", err
			}
		}
		r, rErr := s.store.GetReservation(ctx, nil, propertyID, *reservationID)
		if rErr != nil {
			return "", "", "", rErr
		}
		if resolvedGuestID == nil {
			rgCanon, cErr := s.store.ResolveCanonicalGuestID(ctx, nil, propertyID, r.GuestID)
			if cErr != nil {
				return "", "", "", cErr
			}
			rg, gErr := s.store.GetGuest(ctx, nil, propertyID, rgCanon)
			if gErr != nil {
				return "", "", "", gErr
			}
			if rg.Status == domain.GuestStatusAnonymized {
				return "", "", "", fmt.Errorf("%w: cannot preview template for an anonymized guest", domain.ErrConflict)
			}
			guest = rg
		}
		resPtr = &r
		unitNames = unitNames[:0]
		for _, u := range r.Units {
			unitNames = append(unitNames, u.UnitName)
		}
	} else {
		sample := domain.Reservation{
			Reference:       "TB-2026-SAMPLE",
			ArrivalDate:     time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
			DepartureDate:   time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
			Adults:          2,
			TotalPence:      37500,
			BalanceDuePence: 12500,
		}
		resPtr = &sample
	}
	vars := domain.BuildReservationTemplateVars(prop, guest, resPtr, unitNames, enqPtr)
	return domain.RenderTemplate(tpl.SubjectTemplate, tpl.BodyTextTemplate, tpl.BodyHTMLTemplate, vars)
}

type EmailWebhookEvent struct {
	Provider        string          `json:"provider"`
	ProviderEventID string          `json:"provider_event_id"`
	EventType       string          `json:"event_type"` // DELIVERED, BOUNCED, FAILED
	EmailOutboxID   *uuid.UUID      `json:"email_outbox_id,omitempty"`
	CommunicationID *uuid.UUID      `json:"communication_id,omitempty"`
	Detail          string          `json:"detail"`
	OccurredAt      time.Time       `json:"occurred_at"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

// VerifyEmailWebhookSignature checks an HMAC-SHA256 hex digest when TIRBACH_EMAIL_WEBHOOK_SECRET is configured.
func (s *Service) VerifyEmailWebhookSignature(rawBody []byte, headerSig string) error {
	secret := strings.TrimSpace(s.cfg.EmailWebhookSecret)
	if secret == "" {
		if s.cfg.IsProduction() {
			return fmt.Errorf("%w: email webhook secret not configured", domain.ErrUnauthorized)
		}
		return nil
	}
	sig := strings.TrimPrefix(strings.TrimSpace(headerSig), "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(rawBody)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(sig)), []byte(expected)) {
		return fmt.Errorf("%w: invalid email webhook signature", domain.ErrUnauthorized)
	}
	return nil
}

func (s *Service) ProcessEmailDeliveryWebhook(ctx context.Context, rawBody []byte, headerSig string) error {
	if err := s.VerifyEmailWebhookSignature(rawBody, headerSig); err != nil {
		return err
	}
	var ev EmailWebhookEvent
	if err := json.Unmarshal(rawBody, &ev); err != nil {
		return fmt.Errorf("%w: invalid webhook JSON", domain.ErrValidation)
	}
	evType := strings.ToUpper(strings.TrimSpace(ev.EventType))
	switch evType {
	case "DELIVERED", "BOUNCED", "FAILED":
	default:
		return fmt.Errorf("%w: unsupported delivery event_type %q", domain.ErrValidation, ev.EventType)
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		return s.store.RecordCommunicationDeliveryEvent(ctx, tx, ev.EmailOutboxID, ev.CommunicationID, ev.Provider, ev.ProviderEventID, evType, ev.Detail, rawBody, ev.OccurredAt)
	})
}

func (s *Service) ListFailedPropertyEmails(ctx context.Context, actor Actor, propertyID uuid.UUID) ([]domain.FailedEmailSummary, error) {
	if err := s.requirePerm(actor, auth.PermCommsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListFailedPropertyEmails(ctx, propertyID)
}

func (s *Service) RetryFailedOutboxEmail(ctx context.Context, actor Actor, propertyID, outboxID uuid.UUID) error {
	if err := s.requirePerm(actor, auth.PermCommsWrite); err != nil {
		return err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.RetryFailedOutboxEmail(ctx, tx, propertyID, outboxID); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "email_outbox.retried", "email_outbox", outboxID.String(), nil, nil)
	})
}

// ReconcileOutboxInput supplies evidence-backed operator reconciliation inputs for a
// DISPATCH_UNKNOWN outbox record whose delivery outcome could not be verified automatically.
type ReconcileOutboxInput struct {
	PropertyID   uuid.UUID
	OutboxID     uuid.UUID
	Resolution   string
	ReasonCode   string
	EvidenceType string
	EvidenceRef  string
	Note         string
}

func (s *Service) ReconcileUnknownOutboxDispatch(ctx context.Context, actor Actor, in ReconcileOutboxInput) (domain.EmailOutboxItem, error) {
	if err := s.requirePerm(actor, auth.PermCommsReconcile); err != nil {
		return domain.EmailOutboxItem{}, err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return domain.EmailOutboxItem{}, err
	}
	if s.isDispatchActiveLocally(in.OutboxID) {
		return domain.EmailOutboxItem{}, fmt.Errorf("%w: cannot reconcile outbox item %s while local worker dispatch is active", domain.ErrConflict, in.OutboxID)
	}
	var out domain.EmailOutboxItem
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		now := s.leaseClockParam()
		out, txErr = s.store.ReconcileUnknownOutboxDispatch(ctx, tx, postgres.ReconcileOutboxParams{
			PropertyID:   in.PropertyID,
			OutboxID:     in.OutboxID,
			ActorID:      actor.UserID,
			Resolution:   in.Resolution,
			ReasonCode:   in.ReasonCode,
			EvidenceType: in.EvidenceType,
			EvidenceRef:  in.EvidenceRef,
			Note:         in.Note,
			At:           now,
		})
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &in.PropertyID, "email_outbox.reconciled", "email_outbox", in.OutboxID.String(), nil, map[string]any{
			"actor_id":      actor.UserID.String(),
			"reconciled_at": out.ReconciledAt,
			"resolution":    out.ReconciledDecision,
			"reason_code":   out.ReconciledReasonCode,
			"evidence_type": out.ReconciledEvidence,
			"evidence_ref":  out.ReconciledEvidenceRef,
			"status":        out.Status,
		})
	})
	return out, err
}

// --- Templates & Automation Rule Management ---

func (s *Service) ListCommunicationTemplates(ctx context.Context, actor Actor, propertyID uuid.UUID) ([]domain.CommunicationTemplate, error) {
	if err := s.requirePerm(actor, auth.PermCommsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListCommunicationTemplates(ctx, nil, propertyID)
}

func (s *Service) CreateCommunicationTemplate(ctx context.Context, actor Actor, t domain.CommunicationTemplate) (domain.CommunicationTemplate, error) {
	if err := s.requirePerm(actor, auth.PermCommsTemplates); err != nil {
		return domain.CommunicationTemplate{}, err
	}
	if err := s.requirePropertyScope(actor, t.PropertyID); err != nil {
		return domain.CommunicationTemplate{}, err
	}
	if err := domain.ValidateTemplatePlaceholders(t.SubjectTemplate, t.BodyTextTemplate, t.BodyHTMLTemplate); err != nil {
		return domain.CommunicationTemplate{}, err
	}
	if _, err := domain.SanitizeEmailHeaderValue(t.SubjectTemplate); err != nil {
		return domain.CommunicationTemplate{}, err
	}
	uid := actor.UserID
	t.CreatedBy = &uid
	t.UpdatedBy = &uid
	var created domain.CommunicationTemplate
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		created, txErr = s.store.CreateCommunicationTemplate(ctx, tx, t)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "communication_template.created", "communication_template", created.ID.String(), nil, created)
	})
	return created, err
}

func (s *Service) UpdateCommunicationTemplate(ctx context.Context, actor Actor, t domain.CommunicationTemplate) (domain.CommunicationTemplate, error) {
	if err := s.requirePerm(actor, auth.PermCommsTemplates); err != nil {
		return domain.CommunicationTemplate{}, err
	}
	if err := s.requirePropertyScope(actor, t.PropertyID); err != nil {
		return domain.CommunicationTemplate{}, err
	}
	if err := domain.ValidateTemplatePlaceholders(t.SubjectTemplate, t.BodyTextTemplate, t.BodyHTMLTemplate); err != nil {
		return domain.CommunicationTemplate{}, err
	}
	if _, err := domain.SanitizeEmailHeaderValue(t.SubjectTemplate); err != nil {
		return domain.CommunicationTemplate{}, err
	}
	uid := actor.UserID
	t.UpdatedBy = &uid
	var updated domain.CommunicationTemplate
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetCommunicationTemplate(ctx, tx, t.PropertyID, t.ID)
		if err != nil {
			return err
		}
		updated, err = s.store.UpdateCommunicationTemplate(ctx, tx, t)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &updated.PropertyID, "communication_template.updated", "communication_template", updated.ID.String(), before, updated)
	})
	return updated, err
}

func (s *Service) ListCommunicationAutomationRules(ctx context.Context, actor Actor, propertyID uuid.UUID) ([]domain.CommunicationAutomationRule, error) {
	if err := s.requirePerm(actor, auth.PermCommsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListCommunicationAutomationRules(ctx, nil, propertyID)
}

func (s *Service) ListCommunicationJobs(ctx context.Context, actor Actor, propertyID uuid.UUID) ([]domain.CommunicationJob, error) {
	if err := s.requirePerm(actor, auth.PermCommsRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListCommunicationJobs(ctx, nil, propertyID, 50)
}

func (s *Service) CreateCommunicationAutomationRule(ctx context.Context, actor Actor, r domain.CommunicationAutomationRule) (domain.CommunicationAutomationRule, error) {
	if err := s.requirePerm(actor, auth.PermCommsTemplates); err != nil {
		return domain.CommunicationAutomationRule{}, err
	}
	if err := s.requirePropertyScope(actor, r.PropertyID); err != nil {
		return domain.CommunicationAutomationRule{}, err
	}
	uid := actor.UserID
	r.CreatedBy = &uid
	r.UpdatedBy = &uid
	var out domain.CommunicationAutomationRule
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		out, txErr = s.store.CreateCommunicationAutomationRule(ctx, tx, r)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "automation_rule.created", "communication_automation_rule", out.ID.String(), nil, out)
	})
	return out, err
}

func (s *Service) UpdateCommunicationAutomationRule(ctx context.Context, actor Actor, r domain.CommunicationAutomationRule) (domain.CommunicationAutomationRule, error) {
	if err := s.requirePerm(actor, auth.PermCommsTemplates); err != nil {
		return domain.CommunicationAutomationRule{}, err
	}
	if err := s.requirePropertyScope(actor, r.PropertyID); err != nil {
		return domain.CommunicationAutomationRule{}, err
	}
	uid := actor.UserID
	r.UpdatedBy = &uid
	var out domain.CommunicationAutomationRule
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		out, txErr = s.store.UpdateCommunicationAutomationRule(ctx, tx, r)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "automation_rule.updated", "communication_automation_rule", out.ID.String(), nil, out)
	})
	return out, err
}

// SweepDueCommunicationJobs executes scheduled automation triggers safely from the background ticker.
func (s *Service) SweepDueCommunicationJobs(ctx context.Context) (int, error) {
	return s.store.AcquireDueCommunicationJobs(ctx, s.nowUTC(), 25)
}

// --- Marketing Consent, GDPR Data Export, Retention & Anonymisation ---

func (s *Service) RecordGuestConsent(ctx context.Context, actor Actor, r domain.GuestConsentRecord) (domain.GuestConsentRecord, error) {
	if err := s.requirePerm(actor, auth.PermGuestsWrite); err != nil {
		return domain.GuestConsentRecord{}, err
	}
	if err := s.requirePropertyScope(actor, r.PropertyID); err != nil {
		return domain.GuestConsentRecord{}, err
	}
	if r.Status != domain.ConsentGranted && r.Status != domain.ConsentWithdrawn {
		return domain.GuestConsentRecord{}, domain.NewValidationError(map[string]string{"status": "consent status must be GRANTED or WITHDRAWN"})
	}
	uid := actor.UserID
	r.RecordedBy = &uid
	// Server timestamp is authoritative; client-supplied timestamps are ignored.
	r.OccurredAt = s.nowUTC()
	var out domain.GuestConsentRecord
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		canonID, _, anonOrPending, err := s.store.LockCanonicalGuestClusterTx(ctx, tx, r.PropertyID, r.GuestID)
		if err != nil {
			return err
		}
		r.GuestID = canonID
		if anonOrPending && r.Status == domain.ConsentGranted {
			return fmt.Errorf("%w: cannot grant marketing consent on an anonymized or erasure-pending guest profile", domain.ErrConflict)
		}
		if anonOrPending {
			r.Notes = "Consent withdrawn on erasure"
		}
		var txErr error
		out, txErr = s.store.RecordGuestConsent(ctx, tx, r)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &r.PropertyID, "guest_consent.recorded", "guest", r.GuestID.String(), nil, auditConsentSnapshot(out))
	})
	return out, err
}

func (s *Service) ExportGuestData(ctx context.Context, actor Actor, propertyID, guestID uuid.UUID) (domain.GuestDataExport, error) {
	if err := s.requirePerm(actor, auth.PermGuestsExport); err != nil {
		return domain.GuestDataExport{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.GuestDataExport{}, err
	}
	canonicalID, clusterIDs, err := s.store.GetGuestClusterIDs(ctx, nil, propertyID, guestID)
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	guest, err := s.store.GetGuestSummaryItem(ctx, nil, propertyID, canonicalID, s.nowUTC())
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	guest.CreatedBy = nil
	guest.UpdatedBy = nil

	reservations, err := s.store.ListGuestReservationsByCluster(ctx, nil, propertyID, clusterIDs)
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	resIDs := make([]uuid.UUID, 0, len(reservations))
	for i := range reservations {
		reservations[i].CreatedBy = nil
		reservations[i].UpdatedBy = nil
		resIDs = append(resIDs, reservations[i].ID)
	}

	prefs, err := s.store.ListGuestPreferences(ctx, nil, propertyID, canonicalID)
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	for i := range prefs {
		prefs[i].CreatedBy = nil
		prefs[i].UpdatedBy = nil
	}

	notes, err := s.store.ListGuestNotes(ctx, nil, propertyID, canonicalID)
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	for i := range notes {
		// Minimize staff attribution metadata while retaining CRM note records about the data subject.
		notes[i].AuthorName = ""
		notes[i].CreatedBy = nil
		notes[i].UpdatedBy = nil
	}

	enquiries, err := s.store.ListGuestEnquiriesByClusterOrReservations(ctx, nil, propertyID, clusterIDs, resIDs)
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	enqIDs := make([]uuid.UUID, 0, len(enquiries))
	for i := range enquiries {
		enqIDs = append(enqIDs, enquiries[i].ID)
		enquiries[i].AssignedUserID = nil
		enquiries[i].AssignedUserName = ""
		enquiries[i].ResolvedBy = nil
		enquiries[i].ClosedBy = nil
		enquiries[i].CreatedBy = nil
		enquiries[i].UpdatedBy = nil
	}

	enquiryMsgs, err := s.store.ListGuestEnquiryMessagesByEnquiryIDs(ctx, nil, propertyID, enqIDs)
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	for i := range enquiryMsgs {
		if enquiryMsgs[i].StaffUserID != nil {
			enquiryMsgs[i].SenderName = "Property Staff"
			enquiryMsgs[i].SenderEmail = ""
			enquiryMsgs[i].StaffUserID = nil
		}
	}

	comms, err := s.store.ListGuestCommunicationsByClusterOrLinks(ctx, nil, propertyID, clusterIDs, resIDs, enqIDs)
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	commIDs := make([]uuid.UUID, 0, len(comms))
	for i := range comms {
		commIDs = append(commIDs, comms[i].ID)
		comms[i].StaffUserName = ""
		comms[i].StaffUserID = nil
		comms[i].FollowUpAssignedTo = nil
	}

	deliveryEvents, err := s.store.ListGuestDeliveryEventsByCommunicationIDs(ctx, nil, propertyID, commIDs)
	if err != nil {
		return domain.GuestDataExport{}, err
	}

	consents, err := s.store.ListGuestConsentRecords(ctx, nil, propertyID, canonicalID)
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	for i := range consents {
		consents[i].RecordedBy = nil
	}

	merges, err := s.store.ListGuestMergeHistory(ctx, nil, propertyID, canonicalID)
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	for i := range merges {
		merges[i].MergedBy = nil
	}
	holds, err := s.store.ListGuestBookingHolds(ctx, nil, propertyID, canonicalID)
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	dismissals, err := s.store.ListGuestDuplicateDismissals(ctx, nil, propertyID, canonicalID)
	if err != nil {
		return domain.GuestDataExport{}, err
	}
	for i := range dismissals {
		dismissals[i].DismissedBy = nil
	}

	_ = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		return s.recordAudit(ctx, tx, actor, &propertyID, "guest.data_exported", "guest", canonicalID.String(), nil, nil)
	})

	return domain.GuestDataExport{
		ExportedAt:          s.nowUTC(),
		ExportScopeNotice:   "Operational guest CRM export containing guest profile, reservations, linked booking holds, preferences, CRM notes, enquiries, enquiry correspondence, communications, sanitized delivery events, consent ledger, complete merge lineage, and duplicate dismissal records. Before issuing as a formal UK GDPR Art. 15 Subject Access Request response, an operator must review internal notes/free-text fields and apply any necessary third-party or statutory exemptions.",
		PropertyID:          propertyID,
		Guest:               guest,
		Reservations:        reservations,
		BookingHolds:        holds,
		Preferences:         prefs,
		Notes:               notes,
		Enquiries:           enquiries,
		EnquiryMessages:     enquiryMsgs,
		Communications:      comms,
		DeliveryEvents:      deliveryEvents,
		ConsentHistory:      consents,
		MergeHistory:        merges,
		DuplicateDismissals: dismissals,
	}, nil
}

func (s *Service) AnonymizeGuest(ctx context.Context, actor Actor, propertyID, guestID uuid.UUID, reason string) (domain.Guest, error) {
	if err := s.requirePerm(actor, auth.PermGuestsAnonymize); err != nil {
		return domain.Guest{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.Guest{}, err
	}
	var out domain.Guest
	var pendingDispatches int
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		out, pendingDispatches, txErr = s.store.PrepareOrAnonymizeGuestAt(ctx, tx, propertyID, guestID, actor.UserID, s.nowUTC())
		if txErr != nil {
			return txErr
		}
		if pendingDispatches > 0 {
			return s.recordAudit(ctx, tx, actor, &propertyID, "guest.anonymisation_pending", "guest", out.ID.String(), nil, map[string]any{
				"pending_dispatches": pendingDispatches,
				"requested_guest_id": guestID.String(),
			})
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "guest.anonymized", "guest", out.ID.String(), nil, map[string]any{
			"has_reason": strings.TrimSpace(reason) != "", "anonymized_at": out.AnonymizedAt, "requested_guest_id": guestID.String(),
		})
	})
	if err != nil {
		return domain.Guest{}, err
	}
	if pendingDispatches > 0 {
		return domain.Guest{}, fmt.Errorf("%w: anonymisation pending: %d active or unverified email dispatch(es); wait for completion or reconcile before retrying anonymisation", domain.ErrConflict, pendingDispatches)
	}
	return out, nil
}

func (s *Service) GetPropertyRetentionPolicy(ctx context.Context, actor Actor, propertyID uuid.UUID) (domain.PropertyRetentionPolicy, error) {
	if err := s.requirePerm(actor, auth.PermCommsRead); err != nil {
		return domain.PropertyRetentionPolicy{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.PropertyRetentionPolicy{}, err
	}
	return s.store.GetPropertyRetentionPolicy(ctx, nil, propertyID)
}

func (s *Service) UpdatePropertyRetentionPolicy(ctx context.Context, actor Actor, p domain.PropertyRetentionPolicy) (domain.PropertyRetentionPolicy, error) {
	if err := s.requirePerm(actor, auth.PermCommsTemplates); err != nil {
		return domain.PropertyRetentionPolicy{}, err
	}
	if err := s.requirePropertyScope(actor, p.PropertyID); err != nil {
		return domain.PropertyRetentionPolicy{}, err
	}
	if p.AutoAnonymizeEnabled {
		return domain.PropertyRetentionPolicy{}, domain.NewValidationError(map[string]string{
			"auto_anonymize_enabled": "automated retention anonymization is not active; retention windows are advisory configuration only",
		})
	}
	if p.EnquiryRetentionDays < 30 || p.CommsRetentionDays < 30 || p.InactiveGuestRetentionDays < 90 {
		return domain.PropertyRetentionPolicy{}, domain.NewValidationError(map[string]string{"retention_days": "retention periods violate minimum allowed threshold"})
	}
	uid := actor.UserID
	p.UpdatedBy = &uid
	var out domain.PropertyRetentionPolicy
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		out, txErr = s.store.UpsertPropertyRetentionPolicy(ctx, tx, p)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, &p.PropertyID, "retention_policy.updated", "property_retention_policy", p.PropertyID.String(), nil, out)
	})
	return out, err
}
