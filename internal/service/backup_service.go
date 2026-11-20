package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/auth"
	"github.com/Prestwickuk/lovelace/internal/backup"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

// CanManageBackups reports whether actor is a global owner_admin permitted to manage system backups.
func (s *Service) CanManageBackups(ctx context.Context, actor Actor) bool {
	return s.CanCreateProperty(ctx, actor)
}

func (s *Service) verifyBackupOwnerActor(ctx context.Context, actor Actor, requirePassword bool, currentPassword string) (domain.AdminUser, error) {
	if err := s.requireGlobalOwnerAdmin(ctx, actor); err != nil {
		return domain.AdminUser{}, err
	}
	user, err := s.store.GetAdminUserByID(ctx, nil, actor.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.AdminUser{}, domain.ErrUnauthorized
		}
		return domain.AdminUser{}, err
	}
	if !user.IsActive || user.Role != domain.RoleOwnerAdmin || user.PropertyID != nil {
		return domain.AdminUser{}, domain.ErrForbidden
	}
	if user.MustRotatePassword {
		return domain.AdminUser{}, domain.ErrPasswordRotationRequired
	}
	if requirePassword {
		ok, verr := auth.VerifyPassword(currentPassword, user.PasswordHash)
		if verr != nil || !ok {
			return domain.AdminUser{}, domain.NewValidationError(map[string]string{
				"current_password": "current administrator password is incorrect",
			})
		}
	}
	return user, nil
}

func auditBackupTargetSnapshot(t domain.BackupStorageTarget) map[string]any {
	m := map[string]any{
		"id":                      t.ID.String(),
		"provider":                string(t.Provider),
		"encrypt_archive":         t.EncryptArchive,
		"schedule_enabled":        t.ScheduleEnabled,
		"schedule_interval_hours": t.ScheduleIntervalHours,
		"retention_count":         t.RetentionCount,
		"is_active":               t.IsActive,
		"last_status":             string(t.LastStatus),
		"last_verified_at":        t.LastVerifiedAt,
		"last_backup_at":          t.LastBackupAt,
		"version":                 t.Version,
		"created_at":              t.CreatedAt,
		"updated_at":              t.UpdatedAt,
	}
	return m
}

func auditBackupRunSnapshot(r domain.BackupRun) map[string]any {
	m := map[string]any{
		"id":               r.ID.String(),
		"target_id":        r.TargetID,
		"run_kind":         string(r.RunKind),
		"provider":         string(r.Provider),
		"status":           string(r.Status),
		"encrypted":        r.Encrypted,
		"size_bytes":       r.SizeBytes,
		"table_count":      r.TableCount,
		"row_count":        r.RowCount,
		"attachment_count": r.AttachmentCount,
		"started_at":       r.StartedAt,
		"completed_at":     r.CompletedAt,
	}
	if len(r.ArchiveSHA256) == 64 {
		m["archive_sha256"] = strings.ToLower(r.ArchiveSHA256)
	}
	return m
}

// BackupOverview bundles configured cloud targets, recent backup runs, and empty-DB restore eligibility.
type BackupOverview struct {
	Targets           []domain.BackupStorageTarget `json:"targets"`
	RecentRuns        []domain.BackupRun           `json:"recent_runs"`
	CanRestoreEmptyDB bool                         `json:"can_restore_empty_db"`
}

func (s *Service) GetBackupOverview(ctx context.Context, actor Actor) (BackupOverview, error) {
	if _, err := s.verifyBackupOwnerActor(ctx, actor, false, ""); err != nil {
		return BackupOverview{}, err
	}
	targets, err := s.store.ListBackupTargets(ctx)
	if err != nil {
		return BackupOverview{}, err
	}
	runs, err := s.store.ListBackupRuns(ctx, 25)
	if err != nil {
		return BackupOverview{}, err
	}
	empty, err := s.store.IsEnvironmentEmptyForRestore(ctx)
	if err != nil {
		return BackupOverview{}, err
	}
	return BackupOverview{
		Targets:           targets,
		RecentRuns:        runs,
		CanRestoreEmptyDB: empty,
	}, nil
}

// ExportLocalBackupArchive generates a consistent .zip (or .zip.enc) archive after verifying the owner's current password.
func (s *Service) ExportLocalBackupArchive(
	ctx context.Context,
	actor Actor,
	currentPassword string,
	archivePassphrase string,
) (backup.BuiltArchive, domain.BackupRun, error) {
	if _, err := s.verifyBackupOwnerActor(ctx, actor, true, currentPassword); err != nil {
		return backup.BuiltArchive{}, domain.BackupRun{}, err
	}

	now := s.nowUTC()
	migrations, dumps, err := s.store.ExportBackupSnapshot(ctx)
	if err != nil {
		return backup.BuiltArchive{}, domain.BackupRun{}, err
	}
	built, err := backup.BuildArchive(now, migrations, dumps, s.attachmentDir, archivePassphrase)
	if err != nil {
		return backup.BuiltArchive{}, domain.BackupRun{}, err
	}

	runID, err := postgres.NewUUID()
	if err != nil {
		return backup.BuiltArchive{}, domain.BackupRun{}, err
	}
	doneAt := s.nowUTC()
	uid := actor.UserID

	var savedRun domain.BackupRun
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		savedRun, txErr = s.store.CreateBackupRun(ctx, tx, domain.BackupRun{
			ID:              runID,
			RunKind:         domain.BackupRunLocalDownload,
			Provider:        domain.BackupProviderLocalZip,
			Status:          domain.BackupRunStatusSucceeded,
			Encrypted:       built.Encrypted,
			ObjectKey:       built.Suggested,
			ArchiveSHA256:   built.SHA256,
			SizeBytes:       built.SizeBytes,
			TableCount:      len(built.Manifest.TableOrder),
			RowCount:        built.Manifest.TotalRows,
			AttachmentCount: built.Manifest.AttachmentCount,
			TriggeredBy:     &uid,
			StartedAt:       now,
			CompletedAt:     &doneAt,
		})
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, nil, "backup.local_exported", "backup_run", savedRun.ID.String(), nil, auditBackupRunSnapshot(savedRun))
	})
	if err != nil {
		return backup.BuiltArchive{}, domain.BackupRun{}, err
	}
	return built, savedRun, nil
}

// SaveBackupTargetInput creates or updates an encrypted cloud storage destination.
type SaveBackupTargetInput struct {
	ID                    *uuid.UUID
	Version               int
	CurrentPassword       string
	Name                  string
	Provider              domain.BackupProvider
	BucketOrContainer     string
	Region                string
	EndpointURL           string
	KeyPrefix             string
	EncryptArchive        bool
	ScheduleEnabled       bool
	ScheduleIntervalHours int
	RetentionCount        int
	IsActive              bool
	Credentials           map[string]string
}

func (s *Service) SaveBackupTarget(ctx context.Context, actor Actor, in SaveBackupTargetInput) (domain.BackupStorageTarget, error) {
	if _, err := s.verifyBackupOwnerActor(ctx, actor, true, in.CurrentPassword); err != nil {
		return domain.BackupStorageTarget{}, err
	}
	if s.secretBox == nil {
		return domain.BackupStorageTarget{}, errors.New("credential encryption box is not configured")
	}

	cloudCfg := s.effectiveBackupCloudConfig()
	now := s.nowUTC()
	uid := actor.UserID

	scheduleHours := in.ScheduleIntervalHours
	if scheduleHours <= 0 {
		scheduleHours = 24
	}
	retention := in.RetentionCount
	if retention <= 0 {
		retention = 14
	}

	var nextRunAt *time.Time
	if in.ScheduleEnabled && in.IsActive {
		nr := now.Add(time.Duration(scheduleHours) * time.Hour)
		nextRunAt = &nr
	}

	// Create new cloud target
	if in.ID == nil || *in.ID == uuid.Nil {
		mergedCreds, hint, err := backup.ValidateAndExtractCredentialHint(in.Provider, in.EncryptArchive, in.Credentials, nil)
		if err != nil {
			return domain.BackupStorageTarget{}, err
		}
		sealed, err := s.secretBox.SealMap(mergedCreds)
		if err != nil {
			return domain.BackupStorageTarget{}, err
		}
		tid, err := postgres.NewUUID()
		if err != nil {
			return domain.BackupStorageTarget{}, err
		}
		target := domain.BackupStorageTarget{
			ID:                    tid,
			Name:                  in.Name,
			Provider:              in.Provider,
			BucketOrContainer:     in.BucketOrContainer,
			Region:                in.Region,
			EndpointURL:           in.EndpointURL,
			KeyPrefix:             in.KeyPrefix,
			EncryptedCredentials:  sealed,
			CredentialHint:        hint,
			EncryptArchive:        in.EncryptArchive,
			ScheduleEnabled:       in.ScheduleEnabled,
			ScheduleIntervalHours: scheduleHours,
			RetentionCount:        retention,
			IsActive:              in.IsActive,
			LastStatus:            domain.BackupTargetStatusNeverRun,
			NextRunAt:             nextRunAt,
			CreatedBy:             &uid,
			UpdatedBy:             &uid,
		}
		if err := target.Validate(cloudCfg.AllowLoopbackHTTPForTests); err != nil {
			return domain.BackupStorageTarget{}, err
		}

		var created domain.BackupStorageTarget
		err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
			var txErr error
			created, txErr = s.store.CreateBackupTarget(ctx, tx, target)
			if txErr != nil {
				return txErr
			}
			return s.recordAudit(ctx, tx, actor, nil, "backup_target.created", "backup_target", created.ID.String(), nil, auditBackupTargetSnapshot(created))
		})
		return created, err
	}

	// Update existing cloud target
	var updated domain.BackupStorageTarget
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetBackupTarget(ctx, tx, *in.ID)
		if err != nil {
			return err
		}
		existingCreds, err := s.secretBox.OpenMap(before.EncryptedCredentials)
		if err != nil {
			existingCreds = nil
		}
		mergedCreds, hint, err := backup.ValidateAndExtractCredentialHint(in.Provider, in.EncryptArchive, in.Credentials, existingCreds)
		if err != nil {
			return err
		}
		sealed, err := s.secretBox.SealMap(mergedCreds)
		if err != nil {
			return err
		}
		next := before
		next.Name = in.Name
		next.Provider = in.Provider
		next.BucketOrContainer = in.BucketOrContainer
		next.Region = in.Region
		next.EndpointURL = in.EndpointURL
		next.KeyPrefix = in.KeyPrefix
		next.EncryptedCredentials = sealed
		next.CredentialHint = hint
		next.EncryptArchive = in.EncryptArchive
		next.ScheduleEnabled = in.ScheduleEnabled
		next.ScheduleIntervalHours = scheduleHours
		next.RetentionCount = retention
		next.IsActive = in.IsActive
		next.NextRunAt = nextRunAt
		next.UpdatedBy = &uid
		if in.Version > 0 {
			next.Version = in.Version
		}
		if err := next.Validate(cloudCfg.AllowLoopbackHTTPForTests); err != nil {
			return err
		}
		updated, err = s.store.UpdateBackupTarget(ctx, tx, next)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, nil, "backup_target.updated", "backup_target", updated.ID.String(), auditBackupTargetSnapshot(before), auditBackupTargetSnapshot(updated))
	})
	return updated, err
}

// VerifyBackupTarget tests connectivity and bucket/container list permissions for a saved cloud target.
func (s *Service) VerifyBackupTarget(ctx context.Context, actor Actor, targetID uuid.UUID) (domain.BackupStorageTarget, error) {
	if _, err := s.verifyBackupOwnerActor(ctx, actor, false, ""); err != nil {
		return domain.BackupStorageTarget{}, err
	}
	target, err := s.store.GetBackupTarget(ctx, nil, targetID)
	if err != nil {
		return domain.BackupStorageTarget{}, err
	}
	creds, err := s.secretBox.OpenMap(target.EncryptedCredentials)
	if err != nil {
		return domain.BackupStorageTarget{}, fmt.Errorf("decrypt target credentials: %w", err)
	}

	client := backup.NewCloudStorageClient(s.effectiveBackupCloudConfig())
	verifyErr := client.VerifyTarget(ctx, target, creds)
	now := s.nowUTC()

	var updated domain.BackupStorageTarget
	txErr := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetBackupTarget(ctx, tx, targetID)
		if err != nil {
			return err
		}
		if verifyErr != nil {
			errMsg := truncateErrSummary(verifyErr.Error())
			updated, err = s.store.RecordBackupTargetOutcome(ctx, tx, targetID, domain.BackupTargetStatusFailed, nil, nil, nil, errMsg)
			if err != nil {
				return err
			}
			return s.recordAudit(ctx, tx, actor, nil, "backup_target.verify_failed", "backup_target", updated.ID.String(), auditBackupTargetSnapshot(before), auditBackupTargetSnapshot(updated))
		}
		st := domain.BackupTargetStatusVerified
		if before.LastStatus == domain.BackupTargetStatusSucceeded {
			st = domain.BackupTargetStatusSucceeded
		}
		updated, err = s.store.RecordBackupTargetOutcome(ctx, tx, targetID, st, &now, nil, nil, "")
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, nil, "backup_target.verified", "backup_target", updated.ID.String(), auditBackupTargetSnapshot(before), auditBackupTargetSnapshot(updated))
	})
	if txErr != nil {
		return domain.BackupStorageTarget{}, txErr
	}
	if verifyErr != nil {
		return updated, verifyErr
	}
	return updated, nil
}

func (s *Service) DeleteBackupTarget(ctx context.Context, actor Actor, targetID uuid.UUID, currentPassword string) error {
	if _, err := s.verifyBackupOwnerActor(ctx, actor, true, currentPassword); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetBackupTarget(ctx, tx, targetID)
		if err != nil {
			return err
		}
		if err := s.store.DeleteBackupTarget(ctx, tx, targetID); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, nil, "backup_target.deleted", "backup_target", targetID.String(), auditBackupTargetSnapshot(before), nil)
	})
}

// TriggerCloudBackup runs an immediate snapshot + encrypted upload + retention prune to a configured cloud target.
func (s *Service) TriggerCloudBackup(ctx context.Context, actor Actor, targetID uuid.UUID, currentPassword string) (domain.BackupRun, error) {
	if _, err := s.verifyBackupOwnerActor(ctx, actor, true, currentPassword); err != nil {
		return domain.BackupRun{}, err
	}
	target, err := s.store.GetBackupTarget(ctx, nil, targetID)
	if err != nil {
		return domain.BackupRun{}, err
	}
	return s.executeCloudBackup(ctx, &actor, target, domain.BackupRunCloudManual)
}

// SweepScheduledBackups runs any due scheduled cloud backups in the background worker.
func (s *Service) SweepScheduledBackups(ctx context.Context) (int, error) {
	now := s.nowUTC()
	due, err := s.store.ListDueScheduledBackupTargets(ctx, now, 3)
	if err != nil {
		return 0, err
	}
	ran := 0
	for _, target := range due {
		if _, err := s.executeCloudBackup(ctx, nil, target, domain.BackupRunCloudScheduled); err == nil {
			ran++
		}
	}
	return ran, nil
}

func (s *Service) executeCloudBackup(
	ctx context.Context,
	actor *Actor,
	target domain.BackupStorageTarget,
	kind domain.BackupRunKind,
) (domain.BackupRun, error) {
	startedAt := s.nowUTC()
	creds, err := s.secretBox.OpenMap(target.EncryptedCredentials)
	if err != nil {
		return domain.BackupRun{}, fmt.Errorf("decrypt target credentials: %w", err)
	}

	passphrase := ""
	if target.EncryptArchive {
		passphrase = creds["archive_passphrase"]
	}

	migrations, dumps, snapErr := s.store.ExportBackupSnapshot(ctx)
	var (
		built     backup.BuiltArchive
		objectKey string
		runErr    error
	)
	if snapErr != nil {
		runErr = snapErr
	} else {
		built, runErr = backup.BuildArchive(startedAt, migrations, dumps, s.attachmentDir, passphrase)
		if runErr == nil {
			client := backup.NewCloudStorageClient(s.effectiveBackupCloudConfig())
			objectKey, _, runErr = client.UploadAndPrune(ctx, target, creds, built.Suggested, built.Bytes)
		}
	}

	doneAt := s.nowUTC()
	var nextRun *time.Time
	if target.ScheduleEnabled && target.IsActive {
		nr := doneAt.Add(time.Duration(target.ScheduleIntervalHours) * time.Hour)
		nextRun = &nr
	}

	runID, err := postgres.NewUUID()
	if err != nil {
		return domain.BackupRun{}, err
	}
	var trigBy *uuid.UUID
	auditActor := Actor{
		Email: "system@lovelace.internal",
		Role:  domain.RoleOwnerAdmin,
	}
	if actor != nil {
		uid := actor.UserID
		trigBy = &uid
		auditActor = *actor
	}

	tid := target.ID
	var savedRun domain.BackupRun
	txErr := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		runRec := domain.BackupRun{
			ID:              runID,
			TargetID:        &tid,
			RunKind:         kind,
			Provider:        target.Provider,
			Status:          domain.BackupRunStatusSucceeded,
			Encrypted:       built.Encrypted,
			ObjectKey:       objectKey,
			ArchiveSHA256:   built.SHA256,
			SizeBytes:       built.SizeBytes,
			TableCount:      len(built.Manifest.TableOrder),
			RowCount:        built.Manifest.TotalRows,
			AttachmentCount: built.Manifest.AttachmentCount,
			TriggeredBy:     trigBy,
			StartedAt:       startedAt,
			CompletedAt:     &doneAt,
		}
		auditAction := "backup.cloud_uploaded"
		if runErr != nil {
			runRec.Status = domain.BackupRunStatusFailed
			runRec.ErrorSummary = truncateErrSummary(runErr.Error())
			auditAction = "backup.cloud_upload_failed"
			if _, uerr := s.store.RecordBackupTargetOutcome(ctx, tx, target.ID, domain.BackupTargetStatusFailed, nil, nil, nextRun, runRec.ErrorSummary); uerr != nil {
				return uerr
			}
		} else {
			if _, uerr := s.store.RecordBackupTargetOutcome(ctx, tx, target.ID, domain.BackupTargetStatusSucceeded, &doneAt, &doneAt, nextRun, ""); uerr != nil {
				return uerr
			}
		}

		var cerr error
		savedRun, cerr = s.store.CreateBackupRun(ctx, tx, runRec)
		if cerr != nil {
			return cerr
		}
		return s.recordAudit(ctx, tx, auditActor, nil, auditAction, "backup_run", savedRun.ID.String(), nil, auditBackupRunSnapshot(savedRun))
	})
	if txErr != nil {
		return domain.BackupRun{}, txErr
	}
	if runErr != nil {
		return savedRun, runErr
	}
	return savedRun, nil
}

// VerifyUploadedBackupArchive verifies manifest structure and per-file SHA-256 digests without modifying the database.
func (s *Service) VerifyUploadedBackupArchive(
	ctx context.Context,
	actor Actor,
	raw []byte,
	archivePassphrase string,
) (domain.BackupVerificationReport, error) {
	if _, err := s.verifyBackupOwnerActor(ctx, actor, false, ""); err != nil {
		return domain.BackupVerificationReport{}, err
	}
	startedAt := s.nowUTC()
	unpacked, verifyErr := backup.VerifyAndUnpackArchive(raw, archivePassphrase)
	doneAt := s.nowUTC()

	runID, err := postgres.NewUUID()
	if err == nil {
		uid := actor.UserID
		_ = s.store.WithTx(ctx, func(tx pgx.Tx) error {
			rec := domain.BackupRun{
				ID:          runID,
				RunKind:     domain.BackupRunVerifyArchive,
				Provider:    domain.BackupProviderLocalZip,
				Status:      domain.BackupRunStatusSucceeded,
				Encrypted:   backup.IsEncryptedArchive(raw),
				SizeBytes:   int64(len(raw)),
				TriggeredBy: &uid,
				StartedAt:   startedAt,
				CompletedAt: &doneAt,
			}
			if verifyErr != nil {
				rec.Status = domain.BackupRunStatusFailed
				rec.ErrorSummary = truncateErrSummary(verifyErr.Error())
			} else {
				rec.ArchiveSHA256 = unpacked.Report.ArchiveSHA256
				rec.TableCount = unpacked.Report.TableCount
				rec.RowCount = unpacked.Report.TotalRows
				rec.AttachmentCount = unpacked.Report.AttachmentCount
			}
			saved, cerr := s.store.CreateBackupRun(ctx, tx, rec)
			if cerr != nil {
				return cerr
			}
			return s.recordAudit(ctx, tx, actor, nil, "backup.archive_verified", "backup_run", saved.ID.String(), nil, auditBackupRunSnapshot(saved))
		})
	}
	if verifyErr != nil {
		return domain.BackupVerificationReport{}, fmt.Errorf("%w: %v", domain.ErrValidation, verifyErr)
	}
	return unpacked.Report, nil
}

// RestoreBackupIntoEmptyEnvironment verifies current password, verifies archive SHA-256 integrity, enforces that
// the target environment has 0 properties/reservations/guests, and restores all tables + attachments atomically.
func (s *Service) RestoreBackupIntoEmptyEnvironment(
	ctx context.Context,
	actor Actor,
	currentPassword string,
	raw []byte,
	archivePassphrase string,
) (domain.BackupVerificationReport, error) {
	if _, err := s.verifyBackupOwnerActor(ctx, actor, true, currentPassword); err != nil {
		return domain.BackupVerificationReport{}, err
	}

	startedAt := s.nowUTC()
	unpacked, err := backup.VerifyAndUnpackArchive(raw, archivePassphrase)
	if err != nil {
		return domain.BackupVerificationReport{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}

	var randBuf [24]byte
	if _, err := rand.Read(randBuf[:]); err != nil {
		return domain.BackupVerificationReport{}, err
	}
	lockedHash, err := auth.HashPassword("LockedImport!" + hex.EncodeToString(randBuf[:]))
	if err != nil {
		return domain.BackupVerificationReport{}, err
	}

	runID, err := postgres.NewUUID()
	if err != nil {
		return domain.BackupVerificationReport{}, err
	}
	uid := actor.UserID
	var restoredRows int64

	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		restoredRows, txErr = s.store.RestoreBackupSnapshotIntoEmptyDB(ctx, tx, actor.UserID, lockedHash, unpacked.Tables)
		if txErr != nil {
			return txErr
		}
		doneAt := s.nowUTC()
		savedRun, txErr := s.store.CreateBackupRun(ctx, tx, domain.BackupRun{
			ID:              runID,
			RunKind:         domain.BackupRunRestoreEmptyDB,
			Provider:        domain.BackupProviderLocalZip,
			Status:          domain.BackupRunStatusSucceeded,
			Encrypted:       unpacked.Report.Encrypted,
			ArchiveSHA256:   unpacked.Report.ArchiveSHA256,
			SizeBytes:       unpacked.Report.ArchiveSizeBytes,
			TableCount:      unpacked.Report.TableCount,
			RowCount:        restoredRows,
			AttachmentCount: unpacked.Report.AttachmentCount,
			TriggeredBy:     &uid,
			StartedAt:       startedAt,
			CompletedAt:     &doneAt,
		})
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, nil, "backup.restored_empty_db", "backup_run", savedRun.ID.String(), nil, auditBackupRunSnapshot(savedRun))
	})
	if err != nil {
		return domain.BackupVerificationReport{}, err
	}

	// Restore verified attachment blobs into s.attachmentDir with strict path containment.
	if len(unpacked.Attachments) > 0 && strings.TrimSpace(s.attachmentDir) != "" {
		absRoot, err := filepath.Abs(filepath.Clean(s.attachmentDir))
		if err == nil {
			_ = os.MkdirAll(absRoot, 0o750)
			for _, att := range unpacked.Attachments {
				dest := filepath.Clean(filepath.Join(absRoot, filepath.FromSlash(att.RelPath)))
				if !strings.HasPrefix(dest, absRoot+string(filepath.Separator)) {
					continue
				}
				if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
					continue
				}
				_ = os.WriteFile(dest, att.Data, 0o640)
			}
		}
	}

	return unpacked.Report, nil
}

func truncateErrSummary(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 220 {
		return s[:220]
	}
	return s
}
