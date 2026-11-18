package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/domain"
)

func (s *Store) CountAdminUsers(ctx context.Context, db DBTX) (int, error) {
	if db == nil {
		db = s.pool
	}
	var count int
	err := db.QueryRow(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&count)
	return count, MapErr(err)
}

func (s *Store) ListAdminUsers(ctx context.Context, propertyID *uuid.UUID) ([]domain.AdminUser, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if propertyID != nil {
		rows, err = s.pool.Query(ctx, `
			SELECT id, property_id, email, full_name, role, password_hash,
			       is_active, must_rotate_password, last_login_at, version, created_at, updated_at
			FROM admin_users
			WHERE property_id = $1
			ORDER BY created_at ASC
		`, *propertyID)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, property_id, email, full_name, role, password_hash,
			       is_active, must_rotate_password, last_login_at, version, created_at, updated_at
			FROM admin_users
			ORDER BY created_at ASC
		`)
	}
	if err != nil {
		return nil, MapErr(err)
	}
	defer rows.Close()

	var out []domain.AdminUser
	for rows.Next() {
		u, err := scanAdminUser(rows)
		if err != nil {
			return nil, MapErr(err)
		}
		out = append(out, u)
	}
	return out, MapErr(rows.Err())
}

func (s *Store) GetAdminUserByID(ctx context.Context, db DBTX, id uuid.UUID) (domain.AdminUser, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT id, property_id, email, full_name, role, password_hash,
		       is_active, must_rotate_password, last_login_at, version, created_at, updated_at
		FROM admin_users
		WHERE id = $1
	`, id)
	u, err := scanAdminUser(row)
	return u, MapErr(err)
}

func (s *Store) GetAdminUserByEmail(ctx context.Context, db DBTX, email string) (domain.AdminUser, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		SELECT id, property_id, email, full_name, role, password_hash,
		       is_active, must_rotate_password, last_login_at, version, created_at, updated_at
		FROM admin_users
		WHERE LOWER(email) = LOWER($1)
	`, email)
	u, err := scanAdminUser(row)
	return u, MapErr(err)
}

func (s *Store) CreateAdminUser(ctx context.Context, db DBTX, u domain.AdminUser) (domain.AdminUser, error) {
	if db == nil {
		db = s.pool
	}
	row := db.QueryRow(ctx, `
		INSERT INTO admin_users (
			id, property_id, email, full_name, role, password_hash, is_active, must_rotate_password
		) VALUES ($1, $2, LOWER($3), $4, $5, $6, $7, $8)
		RETURNING id, property_id, email, full_name, role, password_hash,
		          is_active, must_rotate_password, last_login_at, version, created_at, updated_at
	`, u.ID, u.PropertyID, u.Email, u.FullName, string(u.Role), u.PasswordHash, u.IsActive, u.MustRotatePassword)
	out, err := scanAdminUser(row)
	return out, MapErr(err)
}

func (s *Store) UpdateAdminUser(ctx context.Context, tx pgx.Tx, u domain.AdminUser) (domain.AdminUser, error) {
	row := tx.QueryRow(ctx, `
		UPDATE admin_users
		SET full_name = $2,
		    role = $3,
		    is_active = $4,
		    must_rotate_password = $5,
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $1 AND version = $6
		RETURNING id, property_id, email, full_name, role, password_hash,
		          is_active, must_rotate_password, last_login_at, version, created_at, updated_at
	`, u.ID, u.FullName, string(u.Role), u.IsActive, u.MustRotatePassword, u.Version)
	out, err := scanAdminUser(row)
	if err == pgx.ErrNoRows {
		if _, gerr := s.GetAdminUserByID(ctx, tx, u.ID); gerr == nil {
			return domain.AdminUser{}, domain.ErrConflict
		}
		return domain.AdminUser{}, domain.ErrNotFound
	}
	return out, MapErr(err)
}

func (s *Store) UpdateAdminUserPassword(ctx context.Context, tx pgx.Tx, userID uuid.UUID, passwordHash string, clearRotate bool) error {
	tag, err := tx.Exec(ctx, `
		UPDATE admin_users
		SET password_hash = $2,
		    must_rotate_password = CASE WHEN $3 THEN FALSE ELSE must_rotate_password END,
		    version = version + 1,
		    updated_at = NOW()
		WHERE id = $1
	`, userID, passwordHash, clearRotate)
	if err != nil {
		return MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) UpdateLastLogin(ctx context.Context, db DBTX, userID uuid.UUID) error {
	if db == nil {
		db = s.pool
	}
	_, err := db.Exec(ctx, `UPDATE admin_users SET last_login_at = NOW() WHERE id = $1`, userID)
	return MapErr(err)
}

func scanAdminUser(r scannable) (domain.AdminUser, error) {
	var u domain.AdminUser
	var role string
	err := r.Scan(
		&u.ID, &u.PropertyID, &u.Email, &u.FullName, &role, &u.PasswordHash,
		&u.IsActive, &u.MustRotatePassword, &u.LastLoginAt, &u.Version, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return domain.AdminUser{}, err
	}
	u.Role = domain.Role(role)
	return u, nil
}

// --- Sessions ---

func (s *Store) CreateSession(ctx context.Context, db DBTX, sess domain.AdminSession) error {
	if db == nil {
		db = s.pool
	}
	_, err := db.Exec(ctx, `
		INSERT INTO admin_sessions (id, user_id, token_hash, csrf_token, ip_address, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, sess.ID, sess.UserID, sess.TokenHash, sess.CSRFToken, sess.IPAddress, sess.UserAgent, sess.ExpiresAt)
	return MapErr(err)
}

// GetActiveSessionByTokenHash fetches a non-revoked, unexpired session + active user.
func (s *Store) GetActiveSessionByTokenHash(ctx context.Context, tokenHash []byte, idleTTL time.Duration) (domain.AdminSession, domain.AdminUser, error) {
	var sess domain.AdminSession
	var u domain.AdminUser
	var role string
	idleCutoff := time.Now().UTC().Add(-idleTTL)

	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.token_hash, s.csrf_token, s.ip_address, s.user_agent,
		       s.expires_at, s.last_seen_at, s.revoked_at, s.created_at,
		       u.id, u.property_id, u.email, u.full_name, u.role, u.password_hash,
		       u.is_active, u.must_rotate_password, u.last_login_at, u.version, u.created_at, u.updated_at
		FROM admin_sessions s
		JOIN admin_users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > NOW()
		  AND s.last_seen_at > $2
		  AND u.is_active = TRUE
	`, tokenHash, idleCutoff).Scan(
		&sess.ID, &sess.UserID, &sess.TokenHash, &sess.CSRFToken, &sess.IPAddress, &sess.UserAgent,
		&sess.ExpiresAt, &sess.LastSeenAt, &sess.RevokedAt, &sess.CreatedAt,
		&u.ID, &u.PropertyID, &u.Email, &u.FullName, &role, &u.PasswordHash,
		&u.IsActive, &u.MustRotatePassword, &u.LastLoginAt, &u.Version, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return domain.AdminSession{}, domain.AdminUser{}, MapErr(err)
	}
	u.Role = domain.Role(role)
	return sess, u, nil
}

func (s *Store) TouchSession(ctx context.Context, sessionID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE admin_sessions SET last_seen_at = NOW() WHERE id = $1 AND revoked_at IS NULL
	`, sessionID)
	return MapErr(err)
}

func (s *Store) RevokeSession(ctx context.Context, sessionID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE admin_sessions SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL
	`, sessionID)
	return MapErr(err)
}

func (s *Store) RevokeAllUserSessions(ctx context.Context, db DBTX, userID uuid.UUID) error {
	if db == nil {
		db = s.pool
	}
	_, err := db.Exec(ctx, `
		UPDATE admin_sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL
	`, userID)
	return MapErr(err)
}

// --- Login Attempts (Rate Limiting) ---

func (s *Store) RecordLoginAttempt(ctx context.Context, email, ip string, succeeded bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO login_attempts (email, ip_address, succeeded) VALUES (LOWER($1), $2, $3)
	`, email, ip, succeeded)
	return MapErr(err)
}

func (s *Store) CountRecentFailedLogins(ctx context.Context, email, ip string, window time.Duration) (byIP int, byEmail int, err error) {
	since := time.Now().UTC().Add(-window)
	err = s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE ip_address = $1),
			COUNT(*) FILTER (WHERE LOWER(email) = LOWER($2))
		FROM login_attempts
		WHERE succeeded = FALSE AND attempted_at >= $3
	`, ip, email, since).Scan(&byIP, &byEmail)
	return byIP, byEmail, MapErr(err)
}

// PruneStaleAuthState deletes aged login_attempts and long-expired/revoked admin_sessions
// so authentication tables remain bounded over long uptime.
func (s *Store) PruneStaleAuthState(ctx context.Context, attemptsBefore, sessionsBefore time.Time) (int64, error) {
	tag1, err := s.pool.Exec(ctx, `DELETE FROM login_attempts WHERE attempted_at < $1`, attemptsBefore)
	if err != nil {
		return 0, MapErr(err)
	}
	tag2, err := s.pool.Exec(ctx, `
		DELETE FROM admin_sessions
		WHERE (expires_at < $1) OR (revoked_at IS NOT NULL AND revoked_at < $1)
	`, sessionsBefore)
	if err != nil {
		return tag1.RowsAffected(), MapErr(err)
	}
	return tag1.RowsAffected() + tag2.RowsAffected(), nil
}
