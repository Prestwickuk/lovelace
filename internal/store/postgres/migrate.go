package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/tirbach/tir-bach/migrations"
)

const migrationLockID int64 = 748291043 // deterministic advisory lock ID for Tir Bach migrations

// RolePrivilegeSummary reports the PostgreSQL role privileges of the active connection.
type RolePrivilegeSummary struct {
	RoleName      string
	IsSuperuser   bool
	CanCreateDB   bool
	CanCreateRole bool
	CanBypassRLS  bool
}

// InspectRolePrivileges returns privilege flags for current_user on the pool.
func (s *Store) InspectRolePrivileges(ctx context.Context) (RolePrivilegeSummary, error) {
	var sum RolePrivilegeSummary
	err := s.pool.QueryRow(ctx, `
		SELECT current_user, rolsuper, rolcreatedb, rolcreaterole, rolbypassrls
		FROM pg_roles
		WHERE rolname = current_user
	`).Scan(&sum.RoleName, &sum.IsSuperuser, &sum.CanCreateDB, &sum.CanCreateRole, &sum.CanBypassRLS)
	if err != nil {
		return RolePrivilegeSummary{}, fmt.Errorf("inspect current database role privileges: %w", err)
	}
	return sum, nil
}

// Migrate applies all embedded SQL migrations that have not yet been recorded.
// When an unprivileged runtime role starts after tirbach-migrate has already applied
// all migrations, Migrate verifies checksums using read-only SELECTs without executing DDL.
func (s *Store) Migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)

	type loadedMig struct {
		name     string
		raw      []byte
		checksum string
	}
	migs := make([]loadedMig, 0, len(files))
	for _, name := range files {
		raw, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		sum := sha256.Sum256(raw)
		migs = append(migs, loadedMig{
			name:     name,
			raw:      raw,
			checksum: hex.EncodeToString(sum[:]),
		})
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for migrations: %w", err)
	}
	defer conn.Release()

	// Fast read-only verification path for DML-only runtime roles (e.g., lovelace_app).
	var hasMigTable bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&hasMigTable); err != nil {
		return fmt.Errorf("check schema_migrations presence: %w", err)
	}
	if hasMigTable {
		allMatch := true
		for _, m := range migs {
			var existingChecksum string
			if err := conn.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE filename = $1", m.name).Scan(&existingChecksum); err != nil {
				allMatch = false
				break
			}
			if existingChecksum != m.checksum {
				return fmt.Errorf("migration %s checksum mismatch (applied=%s, current=%s)", m.name, existingChecksum, m.checksum)
			}
		}
		if allMatch {
			return nil
		}
	}

	// Migrations are pending: require DDL ability on schema public before taking lock and creating/altering tables.
	var canCreate bool
	if err := conn.QueryRow(ctx, `SELECT has_schema_privilege(current_user, 'public', 'CREATE')`).Scan(&canCreate); err != nil {
		return fmt.Errorf("check schema CREATE privilege for migrations: %w", err)
	}
	if !canCreate {
		return fmt.Errorf("database has pending migrations and current role lacks DDL privileges on schema public; run tirbach-migrate using the migration role")
	}

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockID)
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename    TEXT PRIMARY KEY,
			checksum    TEXT NOT NULL,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}

	for _, m := range migs {
		var existingChecksum string
		err = conn.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE filename = $1", m.name).Scan(&existingChecksum)
		if err == nil {
			if existingChecksum != m.checksum {
				return fmt.Errorf("migration %s checksum mismatch (applied=%s, current=%s)", m.name, existingChecksum, m.checksum)
			}
			continue
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", m.name, err)
		}
		if _, err := tx.Exec(ctx, string(m.raw)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("execute migration %s: %w", m.name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (filename, checksum) VALUES ($1, $2)", m.name, m.checksum); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", m.name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", m.name, err)
		}
	}
	return nil
}
