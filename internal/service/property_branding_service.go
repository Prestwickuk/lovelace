package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/branding"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/storage"
)

func (s *Service) verifyPropertyBrandingActor(ctx context.Context, actor Actor, propertyID uuid.UUID, perm auth.Permission) (domain.AdminUser, error) {
	if err := s.requirePerm(actor, perm); err != nil {
		return domain.AdminUser{}, err
	}
	user, err := s.store.GetAdminUserByID(ctx, nil, actor.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.AdminUser{}, domain.ErrUnauthorized
		}
		return domain.AdminUser{}, err
	}
	if !user.IsActive {
		return domain.AdminUser{}, domain.ErrUnauthorized
	}
	if user.MustRotatePassword {
		return domain.AdminUser{}, domain.ErrPasswordRotationRequired
	}
	if !auth.HasPermission(user.Role, perm) {
		return domain.AdminUser{}, domain.ErrForbidden
	}
	if err := checkUserPropertyAccess(user, propertyID); err != nil {
		return domain.AdminUser{}, err
	}
	return user, nil
}

func buildPropertyBrandingAuditPayload(rec domain.PropertyBrandingRecord, action string) map[string]any {
	now := rec.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	created := rec.CreatedAt
	if created.IsZero() {
		created = now
	}
	hasLogo := strings.TrimSpace(rec.LogoStorageKey) != "" && rec.LogoVersion > 0
	payload := map[string]any{
		"property_id":               rec.PropertyID.String(),
		"action":                    action,
		"schema_version":            rec.SchemaVersion,
		"version":                   rec.Version,
		"has_custom_palette":        rec.PrimaryColor != "" || rec.SecondaryColor != "",
		"has_terminology_overrides": rec.Terminology.HasCustomTerms(),
		"has_logo":                  hasLogo,
		"logo_size_bytes":           rec.LogoSizeBytes,
		"logo_version":              rec.LogoVersion,
		"created_at":                created.UTC().Format(time.RFC3339),
		"updated_at":                now.UTC().Format(time.RFC3339),
	}
	if hasLogo && storage.IsAllowedPropertyLogoMIME(rec.LogoContentType) {
		payload["logo_content_type"] = rec.LogoContentType
		if len(rec.LogoSHA256) == 64 {
			payload["logo_sha256"] = rec.LogoSHA256
		}
	}
	return payload
}

// GetPropertyBranding loads normalized PWE-4 property branding and terminology for an authorized admin actor.
func (s *Service) GetPropertyBranding(ctx context.Context, actor Actor, propertyID uuid.UUID) (domain.PropertyBrandingView, error) {
	if _, err := s.verifyPropertyBrandingActor(ctx, actor, propertyID, auth.PermPropertyRead); err != nil {
		return domain.PropertyBrandingView{}, err
	}
	rec, err := s.store.GetPropertyBrandingByPropertyID(ctx, nil, propertyID)
	if err != nil {
		return domain.PropertyBrandingView{}, err
	}
	return rec.ToView(), nil
}

// ValidatePropertyBrandingPreview validates temporary custom palette overrides without persisting anything.
func (s *Service) ValidatePropertyBrandingPreview(ctx context.Context, actor Actor, propertyID uuid.UUID, primaryRaw, secondaryRaw string) (branding.ResolvedPropertyPalette, error) {
	if _, err := s.verifyPropertyBrandingActor(ctx, actor, propertyID, auth.PermPropertyWrite); err != nil {
		return branding.ResolvedPropertyPalette{}, err
	}
	pal, err := branding.DeriveAndValidatePropertyPalette(primaryRaw, secondaryRaw)
	if err != nil {
		return branding.ResolvedPropertyPalette{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	return pal, nil
}

// UpdatePropertyBranding validates hex colours, contrast pairings, public branding text, and admin-only terminology
// before saving with optimistic locking and sanitized audit logging.
func (s *Service) UpdatePropertyBranding(
	ctx context.Context,
	actor Actor,
	propertyID uuid.UUID,
	expectedVersion int,
	in branding.PropertyBrandingInput,
) (domain.PropertyBrandingView, error) {
	if _, err := s.verifyPropertyBrandingActor(ctx, actor, propertyID, auth.PermPropertyWrite); err != nil {
		return domain.PropertyBrandingView{}, err
	}

	displayName, err := branding.SanitizeAndValidatePublicText("display name", in.DisplayName, 80)
	if err != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	tagline, err := branding.SanitizeAndValidatePublicText("tagline", in.Tagline, 140)
	if err != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	regionLabel, err := branding.SanitizeAndValidatePublicText("region label", in.RegionLabel, 80)
	if err != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	logoAlt, err := branding.SanitizeAndValidatePublicText("logo alt text", in.LogoAlt, 120)
	if err != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	headerLabel, err := branding.SanitizeAndValidatePublicText("admin workspace badge", in.HeaderLabel, 48)
	if err != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}

	primaryHex, err := branding.NormalizeOptionalHex("primary colour", in.PrimaryColor)
	if err != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	secondaryHex, err := branding.NormalizeOptionalHex("secondary colour", in.SecondaryColor)
	if err != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	if _, err := branding.DeriveAndValidatePropertyPalette(primaryHex, secondaryHex); err != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}

	cleanTerms, err := branding.SanitizeAndValidateTerminology(in.Terminology)
	if err != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}

	cleanInput := branding.PropertyBrandingInput{
		DisplayName:    displayName,
		Tagline:        tagline,
		RegionLabel:    regionLabel,
		PrimaryColor:   primaryHex,
		SecondaryColor: secondaryHex,
		LogoAlt:        logoAlt,
		HeaderLabel:    headerLabel,
		Terminology:    cleanTerms,
	}

	pubJSON, err := domain.EncodePublicBrandingJSON(cleanInput)
	if err != nil {
		return domain.PropertyBrandingView{}, err
	}
	adminJSON, err := domain.EncodeAdminBrandingJSON(headerLabel, cleanTerms)
	if err != nil {
		return domain.PropertyBrandingView{}, err
	}
	legacyProj, err := json.Marshal(branding.PropertyBranding{
		DisplayTagline: tagline,
		PrimaryColor:   primaryHex,
		AccentColor:    secondaryHex,
	})
	if err != nil {
		return domain.PropertyBrandingView{}, err
	}

	var updated domain.PropertyBrandingRecord
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		prev, err := s.store.GetPropertyBrandingByPropertyID(ctx, tx, propertyID)
		if err != nil {
			return err
		}
		updated, err = s.store.UpsertPropertyBrandingSettings(
			ctx, tx, propertyID, expectedVersion, pubJSON, adminJSON, legacyProj, actor.UserID,
		)
		if err != nil {
			return err
		}
		return s.recordAudit(
			ctx, tx, actor, &propertyID,
			"property.branding_updated", "property_branding", propertyID.String(),
			buildPropertyBrandingAuditPayload(prev, "updated"),
			buildPropertyBrandingAuditPayload(updated, "updated"),
		)
	})
	if err != nil {
		return domain.PropertyBrandingView{}, err
	}
	return updated.ToView(), nil
}

// UploadPropertyLogo validates and sanitizes a PNG/JPEG/WebP property logo, stores it in BlobStore
// outside any web root, updates PostgreSQL with optimistic locking, and cleans up superseded blobs safely.
func (s *Service) UploadPropertyLogo(
	ctx context.Context,
	actor Actor,
	propertyID uuid.UUID,
	expectedVersion int,
	originalFilename string,
	data []byte,
) (domain.PropertyBrandingView, error) {
	if _, err := s.verifyPropertyBrandingActor(ctx, actor, propertyID, auth.PermPropertyWrite); err != nil {
		return domain.PropertyBrandingView{}, err
	}
	if s.blobInitErr != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("attachment storage unavailable: %w", s.blobInitErr)
	}
	if s.blobStore == nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("attachment storage not configured")
	}

	if _, err := s.store.GetPropertyBrandingByPropertyID(ctx, nil, propertyID); err != nil {
		return domain.PropertyBrandingView{}, err
	}

	sanitized, err := storage.ValidateAndSanitizePropertyLogo(originalFilename, data, storage.MaxPropertyLogoBytes)
	if err != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}

	saved, err := s.blobStore.Put(propertyID, "property-logo"+sanitized.Extension, sanitized.Data, storage.MaxPropertyLogoBytes)
	if err != nil {
		return domain.PropertyBrandingView{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}

	var (
		oldKey  string
		updated domain.PropertyBrandingRecord
	)
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		prev, err := s.store.GetPropertyBrandingByPropertyID(ctx, tx, propertyID)
		if err != nil {
			return err
		}
		oldKey, updated, err = s.store.SetPropertyLogoMetadata(
			ctx, tx, propertyID, expectedVersion,
			saved.StorageKey, sanitized.ContentType, saved.SizeBytes, sanitized.SHA256,
			sanitized.Width, sanitized.Height, actor.UserID,
		)
		if err != nil {
			return err
		}
		return s.recordAudit(
			ctx, tx, actor, &propertyID,
			"property.logo_uploaded", "property_branding", propertyID.String(),
			buildPropertyBrandingAuditPayload(prev, "logo_uploaded"),
			buildPropertyBrandingAuditPayload(updated, "logo_uploaded"),
		)
	})
	if err != nil {
		_ = s.blobStore.Delete(saved.StorageKey)
		return domain.PropertyBrandingView{}, err
	}

	if oldKey != "" && oldKey != saved.StorageKey {
		_ = s.blobStore.Delete(oldKey)
	}
	return updated.ToView(), nil
}

// DeletePropertyLogo clears the property's uploaded logo and removes the underlying blob after DB commit.
func (s *Service) DeletePropertyLogo(
	ctx context.Context,
	actor Actor,
	propertyID uuid.UUID,
	expectedVersion int,
) (domain.PropertyBrandingView, error) {
	if _, err := s.verifyPropertyBrandingActor(ctx, actor, propertyID, auth.PermPropertyWrite); err != nil {
		return domain.PropertyBrandingView{}, err
	}

	var (
		oldKey  string
		updated domain.PropertyBrandingRecord
	)
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		prev, err := s.store.GetPropertyBrandingByPropertyID(ctx, tx, propertyID)
		if err != nil {
			return err
		}
		oldKey, updated, err = s.store.ClearPropertyLogoMetadata(ctx, tx, propertyID, expectedVersion, actor.UserID)
		if err != nil {
			return err
		}
		return s.recordAudit(
			ctx, tx, actor, &propertyID,
			"property.logo_deleted", "property_branding", propertyID.String(),
			buildPropertyBrandingAuditPayload(prev, "logo_deleted"),
			buildPropertyBrandingAuditPayload(updated, "logo_deleted"),
		)
	})
	if err != nil {
		return domain.PropertyBrandingView{}, err
	}
	if oldKey != "" && s.blobStore != nil {
		_ = s.blobStore.Delete(oldKey)
	}
	return updated.ToView(), nil
}

// OpenAdminPropertyLogo opens an active uploaded property logo for an authorized admin actor.
func (s *Service) OpenAdminPropertyLogo(ctx context.Context, actor Actor, propertyID uuid.UUID) (domain.PropertyBrandingView, io.ReadCloser, error) {
	if _, err := s.verifyPropertyBrandingActor(ctx, actor, propertyID, auth.PermPropertyRead); err != nil {
		return domain.PropertyBrandingView{}, nil, err
	}
	if s.blobStore == nil {
		return domain.PropertyBrandingView{}, nil, domain.ErrNotFound
	}
	rec, err := s.store.GetPropertyBrandingByPropertyID(ctx, nil, propertyID)
	if err != nil {
		return domain.PropertyBrandingView{}, nil, err
	}
	if strings.TrimSpace(rec.LogoStorageKey) == "" || !storage.IsAllowedPropertyLogoMIME(rec.LogoContentType) {
		return domain.PropertyBrandingView{}, nil, domain.ErrNotFound
	}
	rc, err := s.blobStore.Open(rec.LogoStorageKey)
	if err != nil {
		if errors.Is(err, storage.ErrAttachmentNotFound) {
			return domain.PropertyBrandingView{}, nil, domain.ErrNotFound
		}
		return domain.PropertyBrandingView{}, nil, err
	}
	return rec.ToView(), rc, nil
}

// OpenPublicPropertyLogoBySlug streams the active public property logo cleanly via BlobStore.
// Returns domain.ErrNotFound if no public property or uploaded logo exists.
func (s *Service) OpenPublicPropertyLogoBySlug(ctx context.Context, slug string) (branding.PublicPropertyBranding, string, io.ReadCloser, error) {
	if s.blobStore == nil {
		return branding.PublicPropertyBranding{}, "", nil, domain.ErrNotFound
	}
	rec, err := s.store.GetPropertyBrandingBySlug(ctx, nil, slug)
	if err != nil {
		return branding.PublicPropertyBranding{}, "", nil, err
	}
	if strings.TrimSpace(rec.LogoStorageKey) == "" || !storage.IsAllowedPropertyLogoMIME(rec.LogoContentType) {
		return branding.PublicPropertyBranding{}, "", nil, domain.ErrNotFound
	}
	rc, err := s.blobStore.Open(rec.LogoStorageKey)
	if err != nil {
		if errors.Is(err, storage.ErrAttachmentNotFound) {
			return branding.PublicPropertyBranding{}, "", nil, domain.ErrNotFound
		}
		return branding.PublicPropertyBranding{}, "", nil, err
	}
	return rec.ToView().PublicProjection, rec.LogoContentType, rc, nil
}
