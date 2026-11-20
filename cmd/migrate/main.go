package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	dsn := strings.TrimSpace(os.Getenv("MIGRATION_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if dsn == "" {
		slog.Error("MIGRATION_DATABASE_URL (or DATABASE_URL fallback) must be set")
		os.Exit(1)
	}

	ctx := context.Background()
	store, err := postgres.New(ctx, dsn)
	if err != nil {
		slog.Error("failed to connect to PostgreSQL for migration", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	priv, err := store.InspectRolePrivileges(ctx)
	if err != nil {
		slog.Error("failed to inspect migration database role", "error", err)
		os.Exit(1)
	}
	if priv.IsSuperuser {
		slog.Warn("migration role has SUPERUSER privilege; use non-superuser lovelace_migrator in production", "role", priv.RoleName)
	}

	if err := store.Migrate(ctx); err != nil {
		slog.Error("database migration failed", "error", err, "role", priv.RoleName)
		os.Exit(1)
	}

	slog.Info("LOVELACE database migrations up to date", "role", priv.RoleName, "superuser", priv.IsSuperuser)
}
