package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/seed"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func main() {
	email := flag.String("admin-email", os.Getenv("TIRBACH_ADMIN_EMAIL"), "Initial owner_admin email")
	name := flag.String("admin-name", os.Getenv("TIRBACH_ADMIN_NAME"), "Initial owner_admin full name")
	password := flag.String("admin-password", os.Getenv("TIRBACH_ADMIN_PASSWORD"), "Initial owner_admin password (min 12 chars)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	store, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		slog.Error("run migrations", "error", err)
		os.Exit(1)
	}

	if *email == "" || *password == "" {
		slog.Warn("admin-email or admin-password omitted; seeding property and inventory without creating an admin user")
	}

	prop, err := seed.EnsureBaseline(ctx, store, seed.Options{
		AdminEmail:    *email,
		AdminName:     *name,
		AdminPassword: *password,
	})
	if err != nil {
		slog.Error("seed failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("Seeded property %s (%s)\n", prop.Name, prop.ID)
}
