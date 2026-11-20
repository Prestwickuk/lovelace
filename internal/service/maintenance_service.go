package service

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/auth"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/storage"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

// CreateMaintenanceRequestInput captures reactive maintenance creation and optional
// explicit inventory restriction creation (which requires blocks:write).
type CreateMaintenanceRequestInput struct {
	PropertyID           uuid.UUID
	Title                string
	Description          string
	CategoryID           uuid.UUID
	Priority             domain.MaintenancePriority
	WorkType             domain.MaintenanceWorkType
	LocationID           *uuid.UUID
	UnitID               *uuid.UUID
	AssetID              *uuid.UUID
	AssignedUserID       *uuid.UUID
	AssignedContractorID *uuid.UUID
	TargetDate           *time.Time
	ScheduledFor         *time.Time
	GuestImpact          bool
	SafetyImpact         bool
	AvailabilityImpact   domain.MaintenanceAvailabilityImpact
	InspectionRequired   bool
	InternalNotes        string

	// Optional explicit inventory restriction during intake (never automatic without confirmation).
	CreateRestriction    bool
	RestrictionStartDate *time.Time
	RestrictionEndDate   *time.Time
	RestrictionNotes     string
}

func (s *Service) ensureMaintenanceSeeded(ctx context.Context, propertyID uuid.UUID) error {
	return s.store.EnsureDefaultMaintenanceData(ctx, nil, propertyID)
}

func (s *Service) propertyLocalDate(ctx context.Context, propertyID uuid.UUID) (time.Time, domain.Property, error) {
	prop, err := s.store.GetPropertyByID(ctx, nil, propertyID)
	if err != nil {
		return time.Time{}, domain.Property{}, err
	}
	loc, err := time.LoadLocation(strings.TrimSpace(prop.Timezone))
	if err != nil {
		loc = time.UTC
	}
	nowLocal := s.nowUTC().In(loc)
	today := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, time.UTC)
	return today, prop, nil
}

// ---------------- Staff, Categories, Locations, Assets, Contractors ----------------

func (s *Service) ListMaintenanceAssignableStaff(ctx context.Context, actor Actor, propertyID uuid.UUID) ([]domain.OpsStaffMember, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListMaintenanceAssignableStaff(ctx, propertyID)
}

func (s *Service) ListMaintenanceCategories(ctx context.Context, actor Actor, propertyID uuid.UUID, includeInactive bool) ([]domain.MaintenanceCategory, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	if err := s.ensureMaintenanceSeeded(ctx, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListMaintenanceCategories(ctx, nil, propertyID, includeInactive)
}

func (s *Service) CreateMaintenanceCategory(ctx context.Context, actor Actor, c domain.MaintenanceCategory) (domain.MaintenanceCategory, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceManage); err != nil {
		return domain.MaintenanceCategory{}, err
	}
	if err := s.requirePropertyScope(actor, c.PropertyID); err != nil {
		return domain.MaintenanceCategory{}, err
	}
	if err := c.Validate(); err != nil {
		return domain.MaintenanceCategory{}, err
	}
	if c.SortOrder == 0 {
		c.SortOrder = 100
	}
	var out domain.MaintenanceCategory
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.store.CreateMaintenanceCategory(ctx, tx, c)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "maintenance_category.created", "maintenance_category", out.ID.String(), nil, out)
	})
	return out, err
}

func (s *Service) UpdateMaintenanceCategory(ctx context.Context, actor Actor, c domain.MaintenanceCategory) (domain.MaintenanceCategory, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceManage); err != nil {
		return domain.MaintenanceCategory{}, err
	}
	if err := s.requirePropertyScope(actor, c.PropertyID); err != nil {
		return domain.MaintenanceCategory{}, err
	}
	if err := c.Validate(); err != nil {
		return domain.MaintenanceCategory{}, err
	}
	var out domain.MaintenanceCategory
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetMaintenanceCategory(ctx, tx, c.PropertyID, c.ID)
		if err != nil {
			return err
		}
		out, err = s.store.UpdateMaintenanceCategory(ctx, tx, c)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "maintenance_category.updated", "maintenance_category", out.ID.String(), before, out)
	})
	return out, err
}

func (s *Service) ListMaintenanceLocations(ctx context.Context, actor Actor, propertyID uuid.UUID, includeInactive bool) ([]domain.MaintenanceLocation, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	if err := s.ensureMaintenanceSeeded(ctx, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListMaintenanceLocations(ctx, nil, propertyID, includeInactive)
}

func (s *Service) CreateMaintenanceLocation(ctx context.Context, actor Actor, l domain.MaintenanceLocation) (domain.MaintenanceLocation, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceManage); err != nil {
		return domain.MaintenanceLocation{}, err
	}
	if err := s.requirePropertyScope(actor, l.PropertyID); err != nil {
		return domain.MaintenanceLocation{}, err
	}
	if err := l.Validate(); err != nil {
		return domain.MaintenanceLocation{}, err
	}
	if l.SortOrder == 0 {
		l.SortOrder = 100
	}
	var out domain.MaintenanceLocation
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if l.UnitID != nil {
			if _, err := s.store.GetUnit(ctx, tx, l.PropertyID, *l.UnitID); err != nil {
				return domain.NewValidationError(map[string]string{"unit_id": "invalid unit for property"})
			}
		}
		var err error
		out, err = s.store.CreateMaintenanceLocation(ctx, tx, l)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "maintenance_location.created", "maintenance_location", out.ID.String(), nil, out)
	})
	return out, err
}

func (s *Service) UpdateMaintenanceLocation(ctx context.Context, actor Actor, l domain.MaintenanceLocation) (domain.MaintenanceLocation, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceManage); err != nil {
		return domain.MaintenanceLocation{}, err
	}
	if err := s.requirePropertyScope(actor, l.PropertyID); err != nil {
		return domain.MaintenanceLocation{}, err
	}
	if err := l.Validate(); err != nil {
		return domain.MaintenanceLocation{}, err
	}
	var out domain.MaintenanceLocation
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetMaintenanceLocation(ctx, tx, l.PropertyID, l.ID)
		if err != nil {
			return err
		}
		if l.UnitID != nil {
			if _, err := s.store.GetUnit(ctx, tx, l.PropertyID, *l.UnitID); err != nil {
				return domain.NewValidationError(map[string]string{"unit_id": "invalid unit for property"})
			}
		}
		out, err = s.store.UpdateMaintenanceLocation(ctx, tx, l)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "maintenance_location.updated", "maintenance_location", out.ID.String(), before, out)
	})
	return out, err
}

func (s *Service) ListMaintenanceAssets(ctx context.Context, actor Actor, propertyID uuid.UUID, includeRetired bool) ([]domain.MaintenanceAsset, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListMaintenanceAssets(ctx, nil, propertyID, includeRetired)
}

func (s *Service) GetMaintenanceAssetWithHistory(ctx context.Context, actor Actor, propertyID, assetID uuid.UUID) (domain.MaintenanceAsset, []domain.MaintenanceRequest, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceRead); err != nil {
		return domain.MaintenanceAsset{}, nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.MaintenanceAsset{}, nil, err
	}
	today, _, err := s.propertyLocalDate(ctx, propertyID)
	if err != nil {
		return domain.MaintenanceAsset{}, nil, err
	}
	asset, err := s.store.GetMaintenanceAsset(ctx, nil, propertyID, assetID)
	if err != nil {
		return domain.MaintenanceAsset{}, nil, err
	}
	history, err := s.store.ListMaintenanceRequests(ctx, nil, propertyID, domain.MaintenanceRequestFilter{
		AssetID: &assetID,
		Limit:   100,
	}, today)
	if err != nil {
		return domain.MaintenanceAsset{}, nil, err
	}
	return asset, history, nil
}

func (s *Service) CreateMaintenanceAsset(ctx context.Context, actor Actor, a domain.MaintenanceAsset) (domain.MaintenanceAsset, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceManage); err != nil {
		return domain.MaintenanceAsset{}, err
	}
	if err := s.requirePropertyScope(actor, a.PropertyID); err != nil {
		return domain.MaintenanceAsset{}, err
	}
	if err := a.Validate(); err != nil {
		return domain.MaintenanceAsset{}, err
	}
	var out domain.MaintenanceAsset
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if a.LocationID != nil {
			if _, err := s.store.GetMaintenanceLocation(ctx, tx, a.PropertyID, *a.LocationID); err != nil {
				return domain.NewValidationError(map[string]string{"location_id": "invalid location for property"})
			}
		}
		if a.UnitID != nil {
			if _, err := s.store.GetUnit(ctx, tx, a.PropertyID, *a.UnitID); err != nil {
				return domain.NewValidationError(map[string]string{"unit_id": "invalid unit for property"})
			}
		}
		var err error
		out, err = s.store.CreateMaintenanceAsset(ctx, tx, a, actor.UserID)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "maintenance_asset.created", "maintenance_asset", out.ID.String(), nil, out)
	})
	return out, err
}

func (s *Service) UpdateMaintenanceAsset(ctx context.Context, actor Actor, a domain.MaintenanceAsset) (domain.MaintenanceAsset, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceManage); err != nil {
		return domain.MaintenanceAsset{}, err
	}
	if err := s.requirePropertyScope(actor, a.PropertyID); err != nil {
		return domain.MaintenanceAsset{}, err
	}
	if err := a.Validate(); err != nil {
		return domain.MaintenanceAsset{}, err
	}
	var out domain.MaintenanceAsset
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetMaintenanceAsset(ctx, tx, a.PropertyID, a.ID)
		if err != nil {
			return err
		}
		out, err = s.store.UpdateMaintenanceAsset(ctx, tx, a, actor.UserID)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "maintenance_asset.updated", "maintenance_asset", out.ID.String(), before, out)
	})
	return out, err
}

func (s *Service) ListMaintenanceContractors(ctx context.Context, actor Actor, propertyID uuid.UUID, includeInactive bool) ([]domain.MaintenanceContractor, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListMaintenanceContractors(ctx, nil, propertyID, includeInactive)
}

func (s *Service) CreateMaintenanceContractor(ctx context.Context, actor Actor, c domain.MaintenanceContractor) (domain.MaintenanceContractor, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceManage); err != nil {
		return domain.MaintenanceContractor{}, err
	}
	if err := s.requirePropertyScope(actor, c.PropertyID); err != nil {
		return domain.MaintenanceContractor{}, err
	}
	if err := c.Validate(); err != nil {
		return domain.MaintenanceContractor{}, err
	}
	var out domain.MaintenanceContractor
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.store.CreateMaintenanceContractor(ctx, tx, c)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "maintenance_contractor.created", "maintenance_contractor", out.ID.String(), nil, out)
	})
	return out, err
}

func (s *Service) UpdateMaintenanceContractor(ctx context.Context, actor Actor, c domain.MaintenanceContractor) (domain.MaintenanceContractor, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceManage); err != nil {
		return domain.MaintenanceContractor{}, err
	}
	if err := s.requirePropertyScope(actor, c.PropertyID); err != nil {
		return domain.MaintenanceContractor{}, err
	}
	if err := c.Validate(); err != nil {
		return domain.MaintenanceContractor{}, err
	}
	var out domain.MaintenanceContractor
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetMaintenanceContractor(ctx, tx, c.PropertyID, c.ID)
		if err != nil {
			return err
		}
		out, err = s.store.UpdateMaintenanceContractor(ctx, tx, c)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "maintenance_contractor.updated", "maintenance_contractor", out.ID.String(), before, out)
	})
	return out, err
}

// ---------------- Requests & Lifecycle ----------------

func (s *Service) ListMaintenanceRequests(ctx context.Context, actor Actor, propertyID uuid.UUID, filter domain.MaintenanceRequestFilter) ([]domain.MaintenanceRequest, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	if err := s.ensureMaintenanceSeeded(ctx, propertyID); err != nil {
		return nil, err
	}
	today, _, err := s.propertyLocalDate(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	return s.store.ListMaintenanceRequests(ctx, nil, propertyID, filter, today)
}

func (s *Service) GetMaintenanceRequestDetail(ctx context.Context, actor Actor, propertyID, requestID uuid.UUID) (domain.MaintenanceRequestDetail, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceRead); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	today, _, err := s.propertyLocalDate(ctx, propertyID)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	return s.store.GetMaintenanceRequestDetail(ctx, nil, propertyID, requestID, today)
}

func (s *Service) CreateMaintenanceRequest(ctx context.Context, actor Actor, in CreateMaintenanceRequestInput) (domain.MaintenanceRequestDetail, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceReport); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	if err := s.requirePropertyScope(actor, in.PropertyID); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	if in.CreateRestriction {
		if err := s.requirePerm(actor, auth.PermMaintenanceWrite); err != nil {
			return domain.MaintenanceRequestDetail{}, err
		}
		if err := s.requirePerm(actor, auth.PermBlocksWrite); err != nil {
			return domain.MaintenanceRequestDetail{}, err
		}
		if in.UnitID == nil {
			return domain.MaintenanceRequestDetail{}, domain.NewValidationError(map[string]string{
				"unit_id": "unit_id is required to place an accommodation inventory restriction",
			})
		}
		if in.RestrictionStartDate == nil || in.RestrictionEndDate == nil || !in.RestrictionEndDate.After(*in.RestrictionStartDate) {
			return domain.MaintenanceRequestDetail{}, domain.NewValidationError(map[string]string{
				"restriction_dates": "restriction end date must be strictly after start date",
			})
		}
	}
	// If reporter lacks PermMaintenanceWrite (e.g., housekeeping_staff reporting an issue),
	// assignment fields cannot be forced unless permitted.
	if (in.AssignedUserID != nil || in.AssignedContractorID != nil) && !auth.HasPermission(actor.Role, auth.PermMaintenanceWrite) {
		return domain.MaintenanceRequestDetail{}, domain.ErrForbidden
	}

	if err := s.ensureMaintenanceSeeded(ctx, in.PropertyID); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}

	workType := in.WorkType
	if workType == "" {
		workType = domain.MaintWorkReactive
	}
	req := domain.MaintenanceRequest{
		PropertyID:           in.PropertyID,
		Title:                in.Title,
		Description:          in.Description,
		CategoryID:           in.CategoryID,
		Priority:             in.Priority,
		Status:               domain.MaintStatusReported,
		WorkType:             workType,
		LocationID:           in.LocationID,
		UnitID:               in.UnitID,
		AssetID:              in.AssetID,
		ReportedBy:           &actor.UserID,
		AssignedUserID:       in.AssignedUserID,
		AssignedContractorID: in.AssignedContractorID,
		TargetDate:           in.TargetDate,
		ScheduledFor:         in.ScheduledFor,
		GuestImpact:          in.GuestImpact,
		SafetyImpact:         in.SafetyImpact,
		AvailabilityImpact:   in.AvailabilityImpact,
		InspectionRequired:   in.InspectionRequired,
		InternalNotes:        in.InternalNotes,
	}
	if req.AssignedUserID != nil || req.AssignedContractorID != nil {
		req.Status = domain.MaintStatusScheduled
	}
	if err := req.Validate(); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}

	var createdID uuid.UUID
	now := s.nowUTC()
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		created, err := s.store.CreateMaintenanceRequest(ctx, tx, req, actor.UserID, now)
		if err != nil {
			return err
		}
		createdID = created.ID

		if in.CreateRestriction && in.UnitID != nil {
			if _, err := s.createMaintenanceRestrictionTx(
				ctx, tx, actor, created.PropertyID, created.ID, *in.UnitID,
				*in.RestrictionStartDate, *in.RestrictionEndDate,
				firstNonEmpty(in.RestrictionNotes, fmt.Sprintf("%s — %s", created.Reference, created.Title)),
			); err != nil {
				return err
			}
		}

		return s.recordAudit(ctx, tx, actor, &created.PropertyID, "maintenance_request.created", "maintenance_request", created.ID.String(), nil, created)
	})
	if err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	return s.GetMaintenanceRequestDetail(ctx, actor, in.PropertyID, createdID)
}

func (s *Service) UpdateMaintenanceRequest(ctx context.Context, actor Actor, req domain.MaintenanceRequest) (domain.MaintenanceRequestDetail, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceWrite); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	if err := s.requirePropertyScope(actor, req.PropertyID); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	if err := req.Validate(); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	today, _, err := s.propertyLocalDate(ctx, req.PropertyID)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}

	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetMaintenanceRequest(ctx, tx, req.PropertyID, req.ID, today)
		if err != nil {
			return err
		}
		after, err := s.store.UpdateMaintenanceRequest(ctx, tx, req, actor.UserID, today)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &req.PropertyID, "maintenance_request.updated", "maintenance_request", req.ID.String(), before, after)
	})
	if err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	return s.GetMaintenanceRequestDetail(ctx, actor, req.PropertyID, req.ID)
}

func (s *Service) AssignMaintenanceRequest(
	ctx context.Context,
	actor Actor,
	propertyID, requestID uuid.UUID,
	userID, contractorID *uuid.UUID,
	dueDate *time.Time,
	notes string,
	version int,
) (domain.MaintenanceRequestDetail, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceWrite); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	today, _, err := s.propertyLocalDate(ctx, propertyID)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetMaintenanceRequest(ctx, tx, propertyID, requestID, today)
		if err != nil {
			return err
		}
		after, err := s.store.AssignMaintenanceRequest(ctx, tx, propertyID, requestID, userID, contractorID, actor.UserID, dueDate, notes, version, today)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "maintenance_request.assigned", "maintenance_request", requestID.String(), before, after)
	})
	if err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	return s.GetMaintenanceRequestDetail(ctx, actor, propertyID, requestID)
}

func (s *Service) TransitionMaintenanceStatus(
	ctx context.Context,
	actor Actor,
	propertyID, requestID uuid.UUID,
	toStatus domain.MaintenanceStatus,
	reason string,
	scheduledFor *time.Time,
	version int,
) (domain.MaintenanceRequestDetail, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceWrite); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	if !toStatus.Valid() {
		return domain.MaintenanceRequestDetail{}, domain.NewValidationError(map[string]string{"status": "invalid status"})
	}
	today, _, err := s.propertyLocalDate(ctx, propertyID)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetMaintenanceRequest(ctx, tx, propertyID, requestID, today)
		if err != nil {
			return err
		}
		// Final closing or reopening a closed request requires inspection authority.
		if toStatus == domain.MaintStatusClosed || before.Status == domain.MaintStatusClosed {
			if err := s.requirePerm(actor, auth.PermMaintenanceInspect); err != nil {
				return err
			}
		}
		if toStatus == domain.MaintStatusClosed && before.InspectionRequired && before.LatestInspectionResult != string(domain.MaintInspectionPassed) {
			return domain.NewValidationError(map[string]string{
				"inspection": "inspection must be recorded as PASSED before closing this maintenance request",
			})
		}

		// Important domain invariant: TransitionMaintenanceStatus NEVER auto-releases linked inventory restrictions.
		after, err := s.store.TransitionMaintenanceStatus(ctx, tx, propertyID, requestID, toStatus, actor.UserID, reason, scheduledFor, version, today)
		if err != nil {
			return err
		}
		if !before.Status.IsOpen() && toStatus.IsOpen() {
			noteBody := "Request reopened"
			if strings.TrimSpace(reason) != "" {
				noteBody += ": " + strings.TrimSpace(reason)
			}
			if _, err := s.store.AddMaintenanceWorkEntry(ctx, tx, domain.MaintenanceWorkEntry{
				PropertyID: propertyID,
				RequestID:  requestID,
				EntryType:  domain.WorkEntryReopened,
				Body:       noteBody,
				AuthorID:   &actor.UserID,
			}); err != nil {
				return err
			}
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "maintenance_request.status_changed", "maintenance_request", requestID.String(), before, after)
	})
	if err != nil {
		return domain.MaintenanceRequestDetail{}, err
	}
	return s.GetMaintenanceRequestDetail(ctx, actor, propertyID, requestID)
}

func (s *Service) AddMaintenanceWorkEntry(
	ctx context.Context,
	actor Actor,
	propertyID, requestID uuid.UUID,
	entryType domain.MaintenanceWorkEntryType,
	body, partsUsed string,
) (domain.MaintenanceWorkEntry, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceWrite); err != nil {
		return domain.MaintenanceWorkEntry{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.MaintenanceWorkEntry{}, err
	}
	if entryType == "" {
		entryType = domain.WorkEntryGeneralNote
	}
	if !entryType.Valid() {
		return domain.MaintenanceWorkEntry{}, domain.NewValidationError(map[string]string{"entry_type": "invalid work entry type"})
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return domain.MaintenanceWorkEntry{}, domain.NewValidationError(map[string]string{"body": "work note cannot be empty"})
	}

	var out domain.MaintenanceWorkEntry
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		today, _, err := s.propertyLocalDate(ctx, propertyID)
		if err != nil {
			return err
		}
		if _, err := s.store.GetMaintenanceRequest(ctx, tx, propertyID, requestID, today); err != nil {
			return err
		}
		out, err = s.store.AddMaintenanceWorkEntry(ctx, tx, domain.MaintenanceWorkEntry{
			PropertyID: propertyID,
			RequestID:  requestID,
			EntryType:  entryType,
			Body:       body,
			PartsUsed:  strings.TrimSpace(partsUsed),
			AuthorID:   &actor.UserID,
		})
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "maintenance_work_entry.created", "maintenance_request", requestID.String(), nil, out)
	})
	return out, err
}

func (s *Service) RecordMaintenanceInspection(
	ctx context.Context,
	actor Actor,
	propertyID, requestID uuid.UUID,
	result domain.MaintenanceInspectionResult,
	notes string,
	closeIfPassed bool,
	version int,
) (domain.MaintenanceRequestDetail, domain.MaintenanceInspection, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceInspect); err != nil {
		return domain.MaintenanceRequestDetail{}, domain.MaintenanceInspection{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.MaintenanceRequestDetail{}, domain.MaintenanceInspection{}, err
	}
	if !result.Valid() {
		return domain.MaintenanceRequestDetail{}, domain.MaintenanceInspection{}, domain.NewValidationError(map[string]string{"result": "must be PASSED or FAILED"})
	}
	if result == domain.MaintInspectionFailed && strings.TrimSpace(notes) == "" {
		return domain.MaintenanceRequestDetail{}, domain.MaintenanceInspection{}, domain.NewValidationError(map[string]string{"notes": "please explain why the inspection failed"})
	}
	today, _, err := s.propertyLocalDate(ctx, propertyID)
	if err != nil {
		return domain.MaintenanceRequestDetail{}, domain.MaintenanceInspection{}, err
	}

	var insp domain.MaintenanceInspection
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetMaintenanceRequest(ctx, tx, propertyID, requestID, today)
		if err != nil {
			return err
		}
		var after domain.MaintenanceRequest
		// Note: inspection NEVER auto-releases any inventory blockout/restriction.
		after, insp, err = s.store.RecordMaintenanceInspection(ctx, tx, propertyID, requestID, actor.UserID, result, notes, closeIfPassed, version, today)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "maintenance_request.inspected", "maintenance_request", requestID.String(), before, map[string]any{
			"request":    after,
			"inspection": insp,
		})
	})
	if err != nil {
		return domain.MaintenanceRequestDetail{}, domain.MaintenanceInspection{}, err
	}
	detail, err := s.GetMaintenanceRequestDetail(ctx, actor, propertyID, requestID)
	return detail, insp, err
}

// ---------------- Attachments ----------------

// UploadMaintenanceAttachment validates MIME/extension/size, writes safely to private storage,
// and records metadata transactionally. Requires maintenance:report when allowOnReport is true
// (used on initial issue creation) and maintenance:write for follow-up attachments.
func (s *Service) UploadMaintenanceAttachment(
	ctx context.Context,
	actor Actor,
	propertyID, requestID uuid.UUID,
	workEntryID *uuid.UUID,
	originalFilename string,
	data []byte,
	caption string,
	allowOnReport bool,
) (domain.MaintenanceAttachment, error) {
	reqPerm := auth.PermMaintenanceWrite
	if allowOnReport {
		reqPerm = auth.PermMaintenanceReport
	}
	if err := s.requirePerm(actor, reqPerm); err != nil {
		return domain.MaintenanceAttachment{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.MaintenanceAttachment{}, err
	}
	if s.blobInitErr != nil {
		return domain.MaintenanceAttachment{}, fmt.Errorf("attachment storage unavailable: %w", s.blobInitErr)
	}
	if s.blobStore == nil {
		return domain.MaintenanceAttachment{}, fmt.Errorf("attachment storage not configured")
	}

	today, _, err := s.propertyLocalDate(ctx, propertyID)
	if err != nil {
		return domain.MaintenanceAttachment{}, err
	}
	if _, err := s.store.GetMaintenanceRequest(ctx, nil, propertyID, requestID, today); err != nil {
		return domain.MaintenanceAttachment{}, err
	}
	if workEntryID != nil {
		if err := s.store.ValidateWorkEntryBelongsToRequest(ctx, nil, propertyID, requestID, *workEntryID); err != nil {
			return domain.MaintenanceAttachment{}, err
		}
	}

	maxBytes := s.cfg.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	saved, err := s.blobStore.Put(propertyID, originalFilename, data, maxBytes)
	if err != nil {
		return domain.MaintenanceAttachment{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}

	var out domain.MaintenanceAttachment
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.store.CreateMaintenanceAttachment(ctx, tx, domain.MaintenanceAttachment{
			PropertyID:       propertyID,
			RequestID:        requestID,
			WorkEntryID:      workEntryID,
			StorageKey:       saved.StorageKey,
			OriginalFilename: storage.SanitizeDownloadFilename(originalFilename),
			ContentType:      saved.ContentType,
			SizeBytes:        saved.SizeBytes,
			SHA256:           saved.SHA256,
			Caption:          strings.TrimSpace(caption),
			UploadedBy:       &actor.UserID,
		})
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "maintenance_attachment.uploaded", "maintenance_attachment", out.ID.String(), nil, out)
	})
	if err != nil {
		_ = s.blobStore.Delete(saved.StorageKey)
		return domain.MaintenanceAttachment{}, err
	}
	return out, nil
}

func (s *Service) OpenMaintenanceAttachment(
	ctx context.Context,
	actor Actor,
	propertyID, attachmentID uuid.UUID,
) (domain.MaintenanceAttachment, io.ReadCloser, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceRead); err != nil {
		return domain.MaintenanceAttachment{}, nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.MaintenanceAttachment{}, nil, err
	}
	if s.blobStore == nil {
		return domain.MaintenanceAttachment{}, nil, fmt.Errorf("attachment storage not configured")
	}
	att, err := s.store.GetMaintenanceAttachment(ctx, nil, propertyID, attachmentID)
	if err != nil {
		return domain.MaintenanceAttachment{}, nil, err
	}
	rc, err := s.blobStore.Open(att.StorageKey)
	if err != nil {
		if err == storage.ErrAttachmentNotFound {
			return domain.MaintenanceAttachment{}, nil, domain.ErrNotFound
		}
		return domain.MaintenanceAttachment{}, nil, err
	}
	return att, rc, nil
}

// ---------------- Inventory Restriction Links ----------------

func (s *Service) CreateMaintenanceRestriction(
	ctx context.Context,
	actor Actor,
	propertyID, requestID uuid.UUID,
	startDate, endDate time.Time,
	notes string,
) (domain.MaintenanceRestrictionLink, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceWrite); err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	if err := s.requirePerm(actor, auth.PermBlocksWrite); err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	if startDate.IsZero() || endDate.IsZero() || !endDate.After(startDate) {
		return domain.MaintenanceRestrictionLink{}, domain.NewValidationError(map[string]string{
			"date_range": "end date must be strictly after start date",
		})
	}
	today, _, err := s.propertyLocalDate(ctx, propertyID)
	if err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}

	var link domain.MaintenanceRestrictionLink
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		req, err := s.store.GetMaintenanceRequest(ctx, tx, propertyID, requestID, today)
		if err != nil {
			return err
		}
		if req.UnitID == nil {
			return domain.NewValidationError(map[string]string{
				"unit_id": "maintenance request must be linked to an accommodation unit before adding an inventory restriction",
			})
		}
		reason := firstNonEmpty(notes, fmt.Sprintf("Maintenance %s: %s", req.Reference, req.Title))
		link, err = s.createMaintenanceRestrictionTx(ctx, tx, actor, propertyID, requestID, *req.UnitID, startDate, endDate, reason)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &propertyID, "maintenance_restriction.created", "maintenance_request", requestID.String(), nil, link)
	})
	return link, err
}

func (s *Service) createMaintenanceRestrictionTx(
	ctx context.Context,
	tx pgx.Tx,
	actor Actor,
	propertyID, requestID, unitID uuid.UUID,
	startDate, endDate time.Time,
	reason string,
) (domain.MaintenanceRestrictionLink, error) {
	conflicts, err := s.store.FindClaimConflictsForChannel(ctx, tx, propertyID, []uuid.UUID{unitID}, startDate, endDate, domain.ClaimMaintenance, "ALL", nil, nil)
	if err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	if len(conflicts) > 0 {
		first := conflicts[0]
		detail := fmt.Sprintf("unit %s already has active %s (%s to %s)", first.UnitCode, first.ClaimType, domain.FormatDate(first.StartDate), domain.FormatDate(first.EndDate))
		if first.Reference != "" {
			detail = fmt.Sprintf("unit %s has active booking %s (%s) from %s to %s; resolve or move booking manually before blocking unit",
				first.UnitCode, first.Reference, first.GuestName, domain.FormatDate(first.StartDate), domain.FormatDate(first.EndDate))
		}
		return domain.MaintenanceRestrictionLink{}, fmt.Errorf("%w: %s", domain.ErrOverlap, detail)
	}

	groupID, err := postgres.NewUUID()
	if err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	grp := domain.BlockoutGroup{
		ID:           groupID,
		PropertyID:   propertyID,
		ScopeType:    domain.BlockScopeUnit,
		Category:     domain.ClaimMaintenance,
		ChannelScope: "ALL",
		StartDate:    startDate,
		EndDate:      endDate,
		Reason:       strings.TrimSpace(reason),
		CreatedBy:    &actor.UserID,
	}
	if _, _, err := s.store.CreateBulkBlockout(ctx, tx, grp, []uuid.UUID{unitID}); err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	link, err := s.store.CreateMaintenanceRestrictionLink(ctx, tx, propertyID, requestID, unitID, groupID, actor.UserID, reason)
	if err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	_, _ = s.store.AddMaintenanceWorkEntry(ctx, tx, domain.MaintenanceWorkEntry{
		PropertyID: propertyID,
		RequestID:  requestID,
		EntryType:  domain.WorkEntryGeneralNote,
		Body:       fmt.Sprintf("Placed maintenance inventory restriction on %s (%s -> %s)", link.UnitCode, domain.FormatDate(startDate), domain.FormatDate(endDate)),
		AuthorID:   &actor.UserID,
	})
	return link, nil
}

func (s *Service) UpdateMaintenanceRestrictionDates(
	ctx context.Context,
	actor Actor,
	propertyID, requestID, linkID uuid.UUID,
	startDate, endDate time.Time,
	groupVersion int,
) (domain.MaintenanceRestrictionLink, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceWrite); err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	if err := s.requirePerm(actor, auth.PermBlocksWrite); err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	if startDate.IsZero() || endDate.IsZero() || !endDate.After(startDate) {
		return domain.MaintenanceRestrictionLink{}, domain.NewValidationError(map[string]string{
			"date_range": "end date must be strictly after start date",
		})
	}

	var updated domain.MaintenanceRestrictionLink
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		link, err := s.store.GetMaintenanceRestrictionLink(ctx, tx, propertyID, linkID)
		if err != nil {
			return err
		}
		if link.RequestID != requestID {
			return domain.ErrNotFound
		}
		if link.ReleasedAt != nil {
			return domain.NewValidationError(map[string]string{"restriction": "cannot resize an already released restriction"})
		}
		if _, err := s.store.UpdateBlockoutGroupDates(ctx, tx, propertyID, link.BlockoutGroupID, startDate, endDate, groupVersion); err != nil {
			return err
		}
		updated, err = s.store.GetMaintenanceRestrictionLink(ctx, tx, propertyID, linkID)
		if err != nil {
			return err
		}
		_, _ = s.store.AddMaintenanceWorkEntry(ctx, tx, domain.MaintenanceWorkEntry{
			PropertyID: propertyID,
			RequestID:  requestID,
			EntryType:  domain.WorkEntryGeneralNote,
			Body:       fmt.Sprintf("Updated maintenance inventory restriction dates on %s to %s -> %s", updated.UnitCode, domain.FormatDate(startDate), domain.FormatDate(endDate)),
			AuthorID:   &actor.UserID,
		})
		return s.recordAudit(ctx, tx, actor, &propertyID, "maintenance_restriction.updated", "maintenance_request", requestID.String(), link, updated)
	})
	return updated, err
}

func (s *Service) ReleaseMaintenanceRestriction(
	ctx context.Context,
	actor Actor,
	propertyID, requestID, linkID uuid.UUID,
) (domain.MaintenanceRestrictionLink, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceWrite); err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	if err := s.requirePerm(actor, auth.PermBlocksWrite); err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.MaintenanceRestrictionLink{}, err
	}

	var updated domain.MaintenanceRestrictionLink
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		link, err := s.store.GetMaintenanceRestrictionLink(ctx, tx, propertyID, linkID)
		if err != nil {
			return err
		}
		if link.RequestID != requestID {
			return domain.ErrNotFound
		}
		if _, err := s.store.ReleaseBlockoutClaims(ctx, tx, propertyID, link.BlockoutGroupID, actor.UserID, []uuid.UUID{link.UnitID}); err != nil {
			return err
		}
		updated, err = s.store.MarkMaintenanceRestrictionReleased(ctx, tx, propertyID, linkID, actor.UserID)
		if err != nil {
			return err
		}
		_, _ = s.store.AddMaintenanceWorkEntry(ctx, tx, domain.MaintenanceWorkEntry{
			PropertyID: propertyID,
			RequestID:  requestID,
			EntryType:  domain.WorkEntryGeneralNote,
			Body:       fmt.Sprintf("Explicitly released inventory restriction on %s (%s -> %s)", link.UnitCode, domain.FormatDate(link.StartDate), domain.FormatDate(link.EndDate)),
			AuthorID:   &actor.UserID,
		})
		return s.recordAudit(ctx, tx, actor, &propertyID, "maintenance_restriction.released", "maintenance_request", requestID.String(), link, updated)
	})
	return updated, err
}

// ---------------- Preventative Schedules ----------------

func (s *Service) ListMaintenanceSchedules(ctx context.Context, actor Actor, propertyID uuid.UUID, includeInactive bool) ([]domain.MaintenanceSchedule, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceRead); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	return s.store.ListMaintenanceSchedules(ctx, nil, propertyID, includeInactive)
}

func (s *Service) CreateMaintenanceSchedule(ctx context.Context, actor Actor, sc domain.MaintenanceSchedule) (domain.MaintenanceSchedule, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceManage); err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	if err := s.requirePropertyScope(actor, sc.PropertyID); err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	if err := sc.Validate(); err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	var out domain.MaintenanceSchedule
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.store.CreateMaintenanceSchedule(ctx, tx, sc, actor.UserID)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "maintenance_schedule.created", "maintenance_schedule", out.ID.String(), nil, out)
	})
	return out, err
}

func (s *Service) UpdateMaintenanceSchedule(ctx context.Context, actor Actor, sc domain.MaintenanceSchedule) (domain.MaintenanceSchedule, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceManage); err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	if err := s.requirePropertyScope(actor, sc.PropertyID); err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	if err := sc.Validate(); err != nil {
		return domain.MaintenanceSchedule{}, err
	}
	var out domain.MaintenanceSchedule
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetMaintenanceSchedule(ctx, tx, sc.PropertyID, sc.ID)
		if err != nil {
			return err
		}
		out, err = s.store.UpdateMaintenanceSchedule(ctx, tx, sc, actor.UserID)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, &out.PropertyID, "maintenance_schedule.updated", "maintenance_schedule", out.ID.String(), before, out)
	})
	return out, err
}

func (s *Service) RunDuePreventativeSchedules(ctx context.Context, actor Actor, propertyID uuid.UUID, asOfDate *time.Time) ([]domain.MaintenanceRequest, error) {
	if err := s.requirePerm(actor, auth.PermMaintenanceManage); err != nil {
		return nil, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return nil, err
	}
	today, _, err := s.propertyLocalDate(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	runDate := today
	if asOfDate != nil && !asOfDate.IsZero() {
		runDate = time.Date(asOfDate.Year(), asOfDate.Month(), asOfDate.Day(), 0, 0, 0, 0, time.UTC)
	}
	var generated []domain.MaintenanceRequest
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		generated, err = s.store.AcquireDueSchedulesAndGenerateRequests(ctx, tx, propertyID, runDate, &actor.UserID)
		if err != nil {
			return err
		}
		for _, req := range generated {
			if err := s.recordAudit(ctx, tx, actor, &propertyID, "maintenance_request.generated_from_schedule", "maintenance_request", req.ID.String(), nil, req); err != nil {
				return err
			}
		}
		return nil
	})
	return generated, err
}

// SweepDueMaintenanceSchedules runs across all properties in the background worker without duplicating jobs.
func (s *Service) SweepDueMaintenanceSchedules(ctx context.Context) (int, error) {
	props, err := s.store.ListProperties(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, prop := range props {
		loc, err := time.LoadLocation(strings.TrimSpace(prop.Timezone))
		if err != nil {
			loc = time.UTC
		}
		nowLocal := s.nowUTC().In(loc)
		today := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, time.UTC)
		err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
			created, err := s.store.AcquireDueSchedulesAndGenerateRequests(ctx, tx, prop.ID, today, nil)
			if err != nil {
				return err
			}
			total += len(created)
			return nil
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (s *Service) GetOpsMaintenanceSummary(ctx context.Context, actor Actor, propertyID uuid.UUID, opDate *time.Time) (domain.OpsMaintenanceSummary, error) {
	if err := s.requirePerm(actor, auth.PermOpsRead); err != nil {
		return domain.OpsMaintenanceSummary{}, err
	}
	if err := s.requirePropertyScope(actor, propertyID); err != nil {
		return domain.OpsMaintenanceSummary{}, err
	}
	today, _, err := s.propertyLocalDate(ctx, propertyID)
	if err != nil {
		return domain.OpsMaintenanceSummary{}, err
	}
	selected := today
	if opDate != nil && !opDate.IsZero() {
		selected = time.Date(opDate.Year(), opDate.Month(), opDate.Day(), 0, 0, 0, 0, time.UTC)
	}
	return s.store.GetOpsMaintenanceSummary(ctx, nil, propertyID, selected, today)
}
