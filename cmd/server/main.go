package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tirbach/tir-bach/internal/app"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/mailer"
	"github.com/tirbach/tir-bach/internal/seed"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "Probe local server /readyz and exit 0/1")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	priv, err := store.InspectRolePrivileges(ctx)
	if err != nil {
		slog.Error("failed to verify database role privileges", "error", err)
		os.Exit(1)
	}
	slog.Info("database runtime role verified",
		"role", priv.RoleName,
		"superuser", priv.IsSuperuser,
		"createdb", priv.CanCreateDB,
		"createrole", priv.CanCreateRole,
	)
	if strings.EqualFold(cfg.AppEnv, "production") && (priv.IsSuperuser || priv.CanCreateDB || priv.CanCreateRole) {
		slog.Error("refusing to run in production with privileged database role (SUPERUSER/CREATEDB/CREATEROLE prohibited)", "role", priv.RoleName)
		os.Exit(1)
	}

	if err := store.Migrate(ctx); err != nil {
		slog.Error("failed to verify or apply database migrations", "error", err)
		os.Exit(1)
	}

	if cfg.AutoSeed {
		if strings.EqualFold(cfg.AppEnv, "production") {
			slog.Error("TIRBACH_AUTO_SEED cannot be enabled in production")
			os.Exit(1)
		}
		prop, err := seed.EnsureBaseline(ctx, store, seed.Options{
			AdminEmail:    os.Getenv("TIRBACH_ADMIN_EMAIL"),
			AdminName:     os.Getenv("TIRBACH_ADMIN_NAME"),
			AdminPassword: os.Getenv("TIRBACH_ADMIN_PASSWORD"),
		})
		if err != nil {
			slog.Error("failed to auto-seed baseline", "error", err)
			os.Exit(1)
		}
		slog.Info("baseline seed verified", "property", prop.Slug, "property_id", prop.ID)
	}

	svc := service.New(store, cfg)
	if err := svc.ValidateProductionStartup(ctx); err != nil {
		slog.Error("production readiness startup check failed", "error", err)
		os.Exit(1)
	}
	if rep := svc.Readiness(ctx); rep.Attachments.Status != "ok" {
		slog.Error("attachment storage readiness check failed; uploads will fail until TIRBACH_ATTACHMENT_DIR is writable", "detail", rep.Attachments.Detail)
	}
	if !svc.HasEmailProvider() {
		slog.Warn("no genuine email delivery provider configured; confirmation emails will remain PENDING in outbox")
	}
	router, err := app.NewRouter(cfg, store, svc)
	if err != nil {
		slog.Error("failed to initialize router", "error", err)
		os.Exit(1)
	}

	// Background workers: sweep expired temporary holds and dispatch pending confirmation emails.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				workerCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				if expired, err := svc.SweepExpiredHolds(workerCtx); err != nil {
					slog.Warn("hold expiry sweep error", "error", err)
				} else if expired > 0 {
					slog.Info("expired stale booking holds", "count", expired)
				}
				if sent, err := svc.DispatchPendingEmails(workerCtx); err != nil {
					if errors.Is(err, mailer.ErrProviderUnconfigured) {
						slog.Debug("skipping email outbox dispatch: no genuine provider configured")
					} else {
						slog.Warn("email outbox dispatch error", "error", err)
					}
				} else if sent > 0 {
					slog.Info("dispatched confirmation emails", "count", sent)
				}
				if gen, err := svc.SweepDueMaintenanceSchedules(workerCtx); err != nil {
					slog.Warn("preventative maintenance schedule sweep error", "error", err)
				} else if gen > 0 {
					slog.Info("generated preventative maintenance requests", "count", gen)
				}
				if queued, err := svc.SweepDueCommunicationJobs(workerCtx); err != nil {
					slog.Warn("automated guest communications sweep error", "error", err)
				} else if queued > 0 {
					slog.Info("queued automated guest communications", "count", queued)
				}
				cancel()
			}
		}
	}()

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("LOVELACE Booking System server listening", "addr", cfg.ListenAddr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown error", "error", err)
	}
}

func runHealthcheck() int {
	addr := strings.TrimSpace(os.Getenv("TIRBACH_LISTEN_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	} else if strings.HasPrefix(addr, "0.0.0.0:") {
		addr = "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/readyz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
