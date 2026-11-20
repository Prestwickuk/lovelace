package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Prestwickuk/lovelace/internal/domain"
)

// DBTX abstracts *pgxpool.Pool and pgx.Tx for shared query helpers.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store provides transactional PostgreSQL persistence for Lovelace.
type Store struct {
	pool *pgxpool.Pool
}

// New connects to PostgreSQL using a configured connection pool and verifies connectivity.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgresql: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Pool exposes the underlying pgxpool for readiness checks.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// Close closes all database connections.
func (s *Store) Close() {
	s.pool.Close()
}

// Ping checks database health.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// WithTx runs fn inside a database transaction, rolling back on error or panic.
func (s *Store) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// NewUUID generates a time-ordered UUIDv7 for index-friendly PostgreSQL primary keys.
func NewUUID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate uuidv7: %w", err)
	}
	return id, nil
}

// MapErr translates low-level PostgreSQL driver errors into domain errors.
func MapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%w: %s", domain.ErrConflict, pgErr.ConstraintName)
		case "23P01": // exclusion_violation (e.g. overlapping availability_blocks)
			return fmt.Errorf("%w: %s", domain.ErrOverlap, pgErr.ConstraintName)
		case "23503": // foreign_key_violation
			return fmt.Errorf("%w: invalid reference (%s)", domain.ErrValidation, pgErr.ConstraintName)
		case "23514": // check_violation
			return fmt.Errorf("%w: constraint (%s)", domain.ErrValidation, pgErr.ConstraintName)
		case "22023": // invalid_parameter_value
			return fmt.Errorf("%w: %s", domain.ErrValidation, pgErr.Message)
		}
	}
	return err
}
