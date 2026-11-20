package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/Prestwickuk/lovelace/internal/config"
	"github.com/Prestwickuk/lovelace/internal/seed"
	"github.com/Prestwickuk/lovelace/internal/service"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

func main() {
	email := flag.String("admin-email", os.Getenv("LOVELACE_ADMIN_EMAIL"), "Initial owner_admin email")
	name := flag.String("admin-name", os.Getenv("LOVELACE_ADMIN_NAME"), "Initial owner_admin full name")
	password := flag.String("admin-password", os.Getenv("LOVELACE_ADMIN_PASSWORD"), "Initial owner_admin password (min 12 chars)")
	adminOnly := flag.Bool("admin-only", strings.EqualFold(strings.TrimSpace(os.Getenv("LOVELACE_SEED_ADMIN_ONLY")), "true"), "Bootstrap initial owner_admin only (must_rotate_password=true) without seeding demo property data")
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

	if *adminOnly {
		svc := service.New(store, cfg)
		bootEmail := strings.TrimSpace(*email)
		bootPass := *password
		bootName := strings.TrimSpace(*name)
		if bootEmail == "" {
			bootEmail = cfg.BootstrapAdminEmail
		}
		if bootPass == "" {
			bootPass = cfg.BootstrapAdminPassword
		}
		if bootName == "" {
			bootName = cfg.BootstrapAdminName
		}
		created, u, err := svc.BootstrapInitialAdmin(ctx, bootEmail, bootName, bootPass)
		if err != nil {
			slog.Error("bootstrap initial admin failed", "error", err)
			os.Exit(1)
		}
		if created {
			fmt.Printf("Bootstrapped initial owner_admin (%s) with must_rotate_password=true\n", u.ID)
		} else {
			fmt.Println("Administrator account already exists; bootstrap skipped")
		}
		return
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
