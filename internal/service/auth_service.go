package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

// Valid Argon2id PHC hash of an unguessable dummy value used to equalize timing when email is unknown.
const dummyArgon2Hash = "$argon2id$v=19$m=65536,t=3,p=1$c29tZXJhbmRvbXNhbHQxNg$YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE"

type LoginResult struct {
	User         domain.AdminUser
	SessionToken string
	CSRFToken    string
}

func (s *Service) Login(ctx context.Context, email, password, ip, userAgent, requestID string) (LoginResult, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || password == "" {
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

func (s *Service) ListUsers(ctx context.Context, actor Actor) ([]domain.AdminUser, error) {
	if err := s.requirePerm(actor, auth.PermUsersManage); err != nil {
		return nil, err
	}
	return s.store.ListAdminUsers(ctx)
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
	MustRotatePassword bool
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
		next := before
		next.FullName = strings.TrimSpace(in.FullName)
		next.Role = in.Role
		next.IsActive = in.IsActive
		next.MustRotatePassword = in.MustRotatePassword
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
	var updatedUser domain.AdminUser
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
		User:         updatedUser,
		SessionToken: tokenPlain,
		CSRFToken:    csrfToken,
	}, nil
}
