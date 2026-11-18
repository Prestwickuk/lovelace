package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

// Valid Argon2id PHC hash of an unguessable dummy value used to equalize timing when email is unknown.
const dummyArgon2Hash = "$argon2id$v=19$m=65536,t=3,p=1$c29tZXJhbmRvbXNhbHQxNg$YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE"

type LoginResult struct {
	User                   domain.AdminUser
	SessionToken           string
	CSRFToken              string
	GeneratedRecoveryCodes []string
}

type RecoveryCodeSummary struct {
	UnusedCount     int        `json:"unused_count"`
	TotalCount      int        `json:"total_count"`
	LastGeneratedAt *time.Time `json:"last_generated_at,omitempty"`
}

func (s *Service) Login(ctx context.Context, email, password, ip, userAgent, requestID string) (LoginResult, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || len(email) > 254 || password == "" || len(password) > auth.MaxPasswordBytes {
		return LoginResult{}, domain.ErrUnauthorized
	}

	byIP, byEmail, err := s.store.CountRecentFailedLogins(ctx, email, ip, s.cfg.LoginWindow)
	if err != nil {
		return LoginResult{}, err
	}
	if byIP >= s.cfg.LoginMaxFailIP || byEmail >= s.cfg.LoginMaxFailAcc {
		return LoginResult{}, domain.ErrRateLimited
	}

	user, err := s.store.GetAdminUserByEmail(ctx, nil, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			_, _ = auth.VerifyPassword(password, dummyArgon2Hash)
			_ = s.store.RecordLoginAttempt(ctx, email, ip, false)
			return LoginResult{}, domain.ErrUnauthorized
		}
		return LoginResult{}, err
	}

	if !user.IsActive {
		_, _ = auth.VerifyPassword(password, user.PasswordHash)
		_ = s.store.RecordLoginAttempt(ctx, email, ip, false)
		return LoginResult{}, domain.ErrUnauthorized
	}

	ok, err := auth.VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		_ = s.store.RecordLoginAttempt(ctx, email, ip, false)
		return LoginResult{}, domain.ErrUnauthorized
	}

	tokenPlain, tokenHash, err := auth.GenerateToken()
	if err != nil {
		return LoginResult{}, err
	}
	csrfToken, err := auth.GenerateCSRFToken()
	if err != nil {
		return LoginResult{}, err
	}
	sessID, err := postgres.NewUUID()
	if err != nil {
		return LoginResult{}, err
	}

	now := s.nowUTC()
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.CreateSession(ctx, tx, domain.AdminSession{
			ID:        sessID,
			UserID:    user.ID,
			TokenHash: tokenHash,
			CSRFToken: csrfToken,
			IPAddress: ip,
			UserAgent: userAgent,
			ExpiresAt: now.Add(s.cfg.SessionTTL),
		}); err != nil {
			return err
		}
		if err := s.store.UpdateLastLogin(ctx, tx, user.ID); err != nil {
			return err
		}
		actor := Actor{
			UserID:    user.ID,
			Email:     user.Email,
			Role:      user.Role,
			RequestID: requestID,
			IPAddress: ip,
			UserAgent: userAgent,
		}
		return s.recordAudit(ctx, tx, actor, user.PropertyID, "auth.login_succeeded", "admin_user", user.ID.String(), nil, map[string]any{
			"session_id": sessID.String(),
		})
	})
	if err != nil {
		return LoginResult{}, err
	}
	_ = s.store.RecordLoginAttempt(ctx, email, ip, true)

	return LoginResult{
		User:         user,
		SessionToken: tokenPlain,
		CSRFToken:    csrfToken,
	}, nil
}

func (s *Service) AuthenticateSession(ctx context.Context, rawToken string) (Actor, domain.AdminUser, error) {
	if strings.TrimSpace(rawToken) == "" {
		return Actor{}, domain.AdminUser{}, domain.ErrUnauthorized
	}
	hash := auth.HashToken(rawToken)
	sess, user, err := s.store.GetActiveSessionByTokenHash(ctx, hash, s.cfg.SessionIdleTTL)
	if err != nil {
		return Actor{}, domain.AdminUser{}, domain.ErrUnauthorized
	}
	// Refresh sliding idle timestamp asynchronously or synchronously (best-effort).
	_ = s.store.TouchSession(ctx, sess.ID)

	actor := Actor{
		UserID:             user.ID,
		PropertyID:         user.PropertyID,
		Email:              user.Email,
		FullName:           user.FullName,
		Role:               user.Role,
		SessionID:          sess.ID,
		CSRFToken:          sess.CSRFToken,
		MustRotatePassword: user.MustRotatePassword,
	}
	return actor, user, nil
}

func (s *Service) Logout(ctx context.Context, actor Actor) error {
	if actor.SessionID == uuid.Nil {
		return nil
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE admin_sessions SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, actor.SessionID); err != nil {
			return postgres.MapErr(err)
		}
		return s.recordAudit(ctx, tx, actor, actor.PropertyID, "auth.logout", "admin_session", actor.SessionID.String(), nil, nil)
	})
}

// effectiveActorPropertyScope returns the authoritative property scope of an actor by checking
// their persisted admin_users record first (falling back to actor.PropertyID only for synthetic actors).
// Global owner_admin accounts (admin_users.property_id IS NULL) return nil.
func (s *Service) effectiveActorPropertyScope(ctx context.Context, db postgres.DBTX, actor Actor) *uuid.UUID {
	if actor.UserID != uuid.Nil {
		if u, err := s.store.GetAdminUserByID(ctx, db, actor.UserID); err == nil {
			return u.PropertyID
		}
	}
	return actor.PropertyID
}

const bootstrapAdminAdvisoryLockKey int64 = 748291099

// HasAdminUsers reports whether at least one administrator account exists in the database.
func (s *Service) HasAdminUsers(ctx context.Context) (bool, error) {
	count, err := s.store.CountAdminUsers(ctx, nil)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// BootstrapInitialAdmin provisions the initial global owner_admin account with MustRotatePassword=true
// when and only when admin_users has zero rows. Subsequent invocations are strictly idempotent no-ops
// and never overwrite existing accounts or rotated passwords.
func (s *Service) BootstrapInitialAdmin(ctx context.Context, email, fullName, password string) (bool, domain.AdminUser, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	fullName = strings.TrimSpace(fullName)
	if fullName == "" {
		fullName = "Owner Administrator"
	}

	u := domain.AdminUser{
		PropertyID:         nil,
		Email:              email,
		FullName:           fullName,
		Role:               domain.RoleOwnerAdmin,
		IsActive:           true,
		MustRotatePassword: true,
	}
	if err := u.ValidateNew(); err != nil {
		return false, domain.AdminUser{}, err
	}
	if err := auth.ValidatePasswordPolicy(password); err != nil {
		return false, domain.AdminUser{}, err
	}

	// Fast path check before deriving Argon2id hash on routine restarts.
	if existing, err := s.store.CountAdminUsers(ctx, nil); err != nil {
		return false, domain.AdminUser{}, err
	} else if existing > 0 {
		return false, domain.AdminUser{}, nil
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return false, domain.AdminUser{}, err
	}
	id, err := postgres.NewUUID()
	if err != nil {
		return false, domain.AdminUser{}, err
	}
	u.ID = id
	u.PasswordHash = hash

	var (
		created bool
		out     domain.AdminUser
	)
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, lockErr := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, bootstrapAdminAdvisoryLockKey); lockErr != nil {
			return postgres.MapErr(lockErr)
		}
		count, cntErr := s.store.CountAdminUsers(ctx, tx)
		if cntErr != nil {
			return cntErr
		}
		if count > 0 {
			return nil
		}
		var txErr error
		out, txErr = s.store.CreateAdminUser(ctx, tx, u)
		if txErr != nil {
			return txErr
		}
		created = true
		return s.recordAudit(ctx, tx, Actor{
			Email: "system",
			Role:  domain.RoleOwnerAdmin,
		}, nil, "admin_user.created", "admin_user", out.ID.String(), nil, auditAdminUserSnapshot(out))
	})
	if err != nil {
		return false, domain.AdminUser{}, err
	}
	return created, out, nil
}

func (s *Service) ListUsers(ctx context.Context, actor Actor) ([]domain.AdminUser, error) {
	if err := s.requirePerm(actor, auth.PermUsersManage); err != nil {
		return nil, err
	}
	scopePropID := s.effectiveActorPropertyScope(ctx, nil, actor)
	return s.store.ListAdminUsers(ctx, scopePropID)
}

type CreateUserInput struct {
	PropertyID         *uuid.UUID
	Email              string
	FullName           string
	Role               domain.Role
	Password           string
	MustRotatePassword bool
}

func (s *Service) CreateUser(ctx context.Context, actor Actor, in CreateUserInput) (domain.AdminUser, error) {
	if err := s.requirePerm(actor, auth.PermUsersManage); err != nil {
		return domain.AdminUser{}, err
	}
	// Property-scoped owner_admin accounts cannot create global users or users in another property.
	if scopePropID := s.effectiveActorPropertyScope(ctx, nil, actor); scopePropID != nil {
		if in.PropertyID == nil || *in.PropertyID != *scopePropID {
			return domain.AdminUser{}, domain.ErrForbidden
		}
	}
	u := domain.AdminUser{
		PropertyID:         in.PropertyID,
		Email:              in.Email,
		FullName:           in.FullName,
		Role:               in.Role,
		IsActive:           true,
		MustRotatePassword: in.MustRotatePassword,
	}
	if err := u.ValidateNew(); err != nil {
		return domain.AdminUser{}, err
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return domain.AdminUser{}, err
	}
	id, err := postgres.NewUUID()
	if err != nil {
		return domain.AdminUser{}, err
	}
	u.ID = id
	u.PasswordHash = hash

	var created domain.AdminUser
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		created, txErr = s.store.CreateAdminUser(ctx, tx, u)
		if txErr != nil {
			return txErr
		}
		return s.recordAudit(ctx, tx, actor, created.PropertyID, "admin_user.created", "admin_user", created.ID.String(), nil, auditAdminUserSnapshot(created))
	})
	return created, err
}

type UpdateUserInput struct {
	ID                 uuid.UUID
	FullName           string
	Role               domain.Role
	IsActive           bool
	MustRotatePassword *bool
	Version            int
}

func (s *Service) UpdateUser(ctx context.Context, actor Actor, in UpdateUserInput) (domain.AdminUser, error) {
	if err := s.requirePerm(actor, auth.PermUsersManage); err != nil {
		return domain.AdminUser{}, err
	}
	if strings.TrimSpace(in.FullName) == "" || !in.Role.Valid() {
		return domain.AdminUser{}, domain.NewValidationError(map[string]string{
			"user": "valid full_name and role are required",
		})
	}
	if actor.UserID == in.ID && !in.IsActive {
		return domain.AdminUser{}, domain.NewValidationError(map[string]string{
			"is_active": "cannot deactivate your own active admin account",
		})
	}

	var updated domain.AdminUser
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.store.GetAdminUserByID(ctx, tx, in.ID)
		if err != nil {
			return err
		}
		// Property-scoped owner_admin accounts cannot modify global or cross-property users.
		if scopePropID := s.effectiveActorPropertyScope(ctx, tx, actor); scopePropID != nil {
			if before.PropertyID == nil || *before.PropertyID != *scopePropID {
				return domain.ErrForbidden
			}
		}
		if actor.UserID == in.ID && in.Role != before.Role {
			return domain.NewValidationError(map[string]string{
				"role": "cannot change your own administrative role",
			})
		}
		next := before
		next.FullName = strings.TrimSpace(in.FullName)
		next.Role = in.Role
		next.IsActive = in.IsActive
		if in.MustRotatePassword != nil {
			next.MustRotatePassword = *in.MustRotatePassword
		}
		next.Version = in.Version

		updated, err = s.store.UpdateAdminUser(ctx, tx, next)
		if err != nil {
			return err
		}
		// Revoke active sessions immediately if user is deactivated or role changed.
		if !updated.IsActive || updated.Role != before.Role {
			if err := s.store.RevokeAllUserSessions(ctx, tx, updated.ID); err != nil {
				return err
			}
		}
		return s.recordAudit(ctx, tx, actor, updated.PropertyID, "admin_user.updated", "admin_user", updated.ID.String(), auditAdminUserSnapshot(before), auditAdminUserSnapshot(updated))
	})
	return updated, err
}

// SweepStaleAuthState prunes aged login_attempts and long-expired/revoked admin_sessions.
func (s *Service) SweepStaleAuthState(ctx context.Context) (int64, error) {
	now := s.nowUTC()
	attemptRetention := 7 * 24 * time.Hour
	if s.cfg.LoginWindow*2 > attemptRetention {
		attemptRetention = s.cfg.LoginWindow * 2
	}
	sessionRetention := 30 * 24 * time.Hour
	return s.store.PruneStaleAuthState(ctx, now.Add(-attemptRetention), now.Add(-sessionRetention))
}

func (s *Service) ChangeOwnPassword(ctx context.Context, actor Actor, currentPassword, newPassword string) (LoginResult, error) {
	if actor.UserID == uuid.Nil {
		return LoginResult{}, domain.ErrUnauthorized
	}
	user, err := s.store.GetAdminUserByID(ctx, nil, actor.UserID)
	if err != nil {
		return LoginResult{}, err
	}
	ok, err := auth.VerifyPassword(currentPassword, user.PasswordHash)
	if err != nil || !ok {
		return LoginResult{}, domain.NewValidationError(map[string]string{
			"current_password": "is incorrect",
		})
	}
	if newPassword == currentPassword {
		return LoginResult{}, domain.NewValidationError(map[string]string{
			"new_password": "must differ from current password",
		})
	}
	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return LoginResult{}, err
	}
	tokenPlain, tokenHash, err := auth.GenerateToken()
	if err != nil {
		return LoginResult{}, err
	}
	csrfToken, err := auth.GenerateCSRFToken()
	if err != nil {
		return LoginResult{}, err
	}
	sessID, err := postgres.NewUUID()
	if err != nil {
		return LoginResult{}, err
	}
	now := s.nowUTC()
	var (
		updatedUser domain.AdminUser
		freshCodes  []string
	)
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.UpdateAdminUserPassword(ctx, tx, actor.UserID, newHash, true); err != nil {
			return err
		}
		if err := s.store.RevokeAllUserSessions(ctx, tx, actor.UserID); err != nil {
			return err
		}
		if err := s.store.CreateSession(ctx, tx, domain.AdminSession{
			ID:        sessID,
			UserID:    user.ID,
			TokenHash: tokenHash,
			CSRFToken: csrfToken,
			IPAddress: actor.IPAddress,
			UserAgent: actor.UserAgent,
			ExpiresAt: now.Add(s.cfg.SessionTTL),
		}); err != nil {
			return err
		}
		unusedCnt, _, _, sumErr := s.store.GetAdminRecoveryCodeSummary(ctx, tx, actor.UserID)
		if sumErr != nil {
			return sumErr
		}
		if unusedCnt == 0 {
			codes, genErr := auth.GenerateRecoveryCodes(auth.DefaultRecoveryCodeCount)
			if genErr != nil {
				return genErr
			}
			hashes := make([]string, 0, len(codes))
			for _, c := range codes {
				norm, _ := auth.NormalizeRecoveryCode(c)
				hashes = append(hashes, auth.HashRecoveryCode(s.cfg.EncryptionKey, actor.UserID.String(), norm))
			}
			if repErr := s.store.ReplaceAdminRecoveryCodes(ctx, tx, actor.UserID, hashes); repErr != nil {
				return repErr
			}
			freshCodes = codes
		}
		updatedUser, err = s.store.GetAdminUserByID(ctx, tx, actor.UserID)
		if err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, actor.PropertyID, "admin_user.password_changed", "admin_user", actor.UserID.String(), nil, map[string]any{
			"sessions_revoked": true,
			"new_session_id":   sessID.String(),
		})
	})
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		User:                   updatedUser,
		SessionToken:           tokenPlain,
		CSRFToken:              csrfToken,
		GeneratedRecoveryCodes: freshCodes,
	}, nil
}

func (s *Service) GetRecoveryCodeSummary(ctx context.Context, actor Actor) (RecoveryCodeSummary, error) {
	if actor.UserID == uuid.Nil {
		return RecoveryCodeSummary{}, domain.ErrUnauthorized
	}
	unused, total, lastAt, err := s.store.GetAdminRecoveryCodeSummary(ctx, nil, actor.UserID)
	if err != nil {
		return RecoveryCodeSummary{}, err
	}
	return RecoveryCodeSummary{
		UnusedCount:     unused,
		TotalCount:      total,
		LastGeneratedAt: lastAt,
	}, nil
}

// RegenerateOwnRecoveryCodes replaces the authenticated user's recovery codes after verifying currentPassword.
func (s *Service) RegenerateOwnRecoveryCodes(ctx context.Context, actor Actor, currentPassword string) ([]string, error) {
	if actor.UserID == uuid.Nil {
		return nil, domain.ErrUnauthorized
	}
	user, err := s.store.GetAdminUserByID(ctx, nil, actor.UserID)
	if err != nil {
		return nil, err
	}
	if user.MustRotatePassword || actor.MustRotatePassword {
		return nil, domain.ErrPasswordRotationRequired
	}
	ok, err := auth.VerifyPassword(currentPassword, user.PasswordHash)
	if err != nil || !ok {
		return nil, domain.NewValidationError(map[string]string{
			"current_password": "is incorrect",
		})
	}
	codes, err := auth.GenerateRecoveryCodes(auth.DefaultRecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(codes))
	for _, c := range codes {
		norm, _ := auth.NormalizeRecoveryCode(c)
		hashes = append(hashes, auth.HashRecoveryCode(s.cfg.EncryptionKey, user.ID.String(), norm))
	}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if repErr := s.store.ReplaceAdminRecoveryCodes(ctx, tx, user.ID, hashes); repErr != nil {
			return repErr
		}
		return s.recordAudit(ctx, tx, actor, user.PropertyID, "admin_user.recovery_codes_generated", "admin_user", user.ID.String(), nil, map[string]any{
			"user_id": user.ID.String(),
		})
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// RecoverPasswordWithCode resets an active admin user's password using a single-use recovery code without email.
func (s *Service) RecoverPasswordWithCode(ctx context.Context, email, rawRecoveryCode, newPassword, ip, userAgent, requestID string) (LoginResult, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || len(email) > 254 {
		return LoginResult{}, domain.ErrUnauthorized
	}
	if err := auth.ValidatePasswordPolicy(newPassword); err != nil {
		return LoginResult{}, err
	}

	byIP, byEmail, err := s.store.CountRecentFailedLogins(ctx, email, ip, s.cfg.LoginWindow)
	if err != nil {
		return LoginResult{}, err
	}
	if byIP >= s.cfg.LoginMaxFailIP || byEmail >= s.cfg.LoginMaxFailAcc {
		return LoginResult{}, domain.ErrRateLimited
	}

	normCode, validSyntax := auth.NormalizeRecoveryCode(rawRecoveryCode)
	if !validSyntax {
		_, _ = auth.VerifyPassword(newPassword, dummyArgon2Hash)
		_ = auth.HashRecoveryCode(s.cfg.EncryptionKey, uuid.Nil.String(), "DDDDDDDDDDDDDDDD")
		_ = s.store.RecordLoginAttempt(ctx, email, ip, false)
		return LoginResult{}, domain.ErrUnauthorized
	}

	user, err := s.store.GetAdminUserByEmail(ctx, nil, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			_, _ = auth.VerifyPassword(newPassword, dummyArgon2Hash)
			_ = auth.HashRecoveryCode(s.cfg.EncryptionKey, uuid.Nil.String(), normCode)
			_ = s.store.RecordLoginAttempt(ctx, email, ip, false)
			return LoginResult{}, domain.ErrUnauthorized
		}
		return LoginResult{}, err
	}
	if !user.IsActive {
		_, _ = auth.VerifyPassword(newPassword, user.PasswordHash)
		_ = auth.HashRecoveryCode(s.cfg.EncryptionKey, user.ID.String(), normCode)
		_ = s.store.RecordLoginAttempt(ctx, email, ip, false)
		return LoginResult{}, domain.ErrUnauthorized
	}

	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return LoginResult{}, err
	}
	candidateDigest := auth.HashRecoveryCode(s.cfg.EncryptionKey, user.ID.String(), normCode)

	tokenPlain, tokenHash, err := auth.GenerateToken()
	if err != nil {
		return LoginResult{}, err
	}
	csrfToken, err := auth.GenerateCSRFToken()
	if err != nil {
		return LoginResult{}, err
	}
	sessID, err := postgres.NewUUID()
	if err != nil {
		return LoginResult{}, err
	}

	now := s.nowUTC()
	var updatedUser domain.AdminUser
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		unusedRows, listErr := s.store.ListUnusedAdminRecoveryCodesForUpdate(ctx, tx, user.ID)
		if listErr != nil {
			return listErr
		}
		var matchedID uuid.UUID
		var matched bool
		for _, rec := range unusedRows {
			if auth.MatchRecoveryCodeHash(candidateDigest, rec.CodeHash) {
				matchedID = rec.ID
				matched = true
			}
		}
		if !matched {
			return domain.ErrUnauthorized
		}
		if useErr := s.store.MarkAdminRecoveryCodeUsed(ctx, tx, matchedID); useErr != nil {
			return useErr
		}
		if pwErr := s.store.UpdateAdminUserPassword(ctx, tx, user.ID, newHash, true); pwErr != nil {
			return pwErr
		}
		if revErr := s.store.RevokeAllUserSessions(ctx, tx, user.ID); revErr != nil {
			return revErr
		}
		if sessErr := s.store.CreateSession(ctx, tx, domain.AdminSession{
			ID:        sessID,
			UserID:    user.ID,
			TokenHash: tokenHash,
			CSRFToken: csrfToken,
			IPAddress: ip,
			UserAgent: userAgent,
			ExpiresAt: now.Add(s.cfg.SessionTTL),
		}); sessErr != nil {
			return sessErr
		}
		if llErr := s.store.UpdateLastLogin(ctx, tx, user.ID); llErr != nil {
			return llErr
		}
		var gErr error
		updatedUser, gErr = s.store.GetAdminUserByID(ctx, tx, user.ID)
		if gErr != nil {
			return gErr
		}
		actor := Actor{
			UserID:    user.ID,
			Email:     user.Email,
			Role:      user.Role,
			RequestID: requestID,
			IPAddress: ip,
			UserAgent: userAgent,
		}
		return s.recordAudit(ctx, tx, actor, user.PropertyID, "admin_user.password_recovered", "admin_user", user.ID.String(), nil, map[string]any{
			"user_id":          user.ID.String(),
			"sessions_revoked": true,
			"new_session_id":   sessID.String(),
		})
	})
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			_ = s.store.RecordLoginAttempt(ctx, email, ip, false)
		}
		return LoginResult{}, err
	}
	_ = s.store.RecordLoginAttempt(ctx, email, ip, true)

	return LoginResult{
		User:         updatedUser,
		SessionToken: tokenPlain,
		CSRFToken:    csrfToken,
	}, nil
}

// AdminResetUserPassword allows an authorized owner_admin to set a temporary password
// (with must_rotate_password=true and immediate session revocation) for another user in scope.
func (s *Service) AdminResetUserPassword(ctx context.Context, actor Actor, targetUserID uuid.UUID, temporaryPassword string) error {
	if err := s.requirePerm(actor, auth.PermUsersManage); err != nil {
		return err
	}
	if actor.UserID == targetUserID {
		return domain.NewValidationError(map[string]string{
			"user": "use My Account to update your own password",
		})
	}
	newHash, err := auth.HashPassword(temporaryPassword)
	if err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		target, err := s.store.GetAdminUserByID(ctx, tx, targetUserID)
		if err != nil {
			return err
		}
		if scopePropID := s.effectiveActorPropertyScope(ctx, tx, actor); scopePropID != nil {
			if target.PropertyID == nil || *target.PropertyID != *scopePropID {
				return domain.ErrForbidden
			}
		}
		if err := s.store.SetAdminUserPasswordAndRotation(ctx, tx, target.ID, newHash, true); err != nil {
			return err
		}
		if err := s.store.RevokeAllUserSessions(ctx, tx, target.ID); err != nil {
			return err
		}
		return s.recordAudit(ctx, tx, actor, target.PropertyID, "admin_user.password_reset_by_admin", "admin_user", target.ID.String(), nil, map[string]any{
			"user_id":              target.ID.String(),
			"sessions_revoked":     true,
			"must_rotate_password": true,
		})
	})
}
