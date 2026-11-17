package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/mailer"
	"github.com/tirbach/tir-bach/internal/payments"
	"github.com/tirbach/tir-bach/internal/secrets"
	"github.com/tirbach/tir-bach/internal/storage"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

// Actor identifies the authenticated administrator performing an action.
type Actor struct {
	UserID             uuid.UUID
	PropertyID         *uuid.UUID
	Email              string
	FullName           string
	Role               domain.Role
	MustRotatePassword bool
	SessionID          uuid.UUID
	CSRFToken          string
	RequestID          string
	IPAddress          string
	UserAgent          string
}

// Canonical production outbox worker dispatch timing defaults.
// HeartbeatInterval (20s) is one-third of LeaseTTL (60s), and TransportTimeout (30s)
// is strictly bounded below LeaseTTL so stalled SMTP calls cannot silently outlive a lease.
const (
	DefaultDispatchLeaseTTL          = 60 * time.Second
	DefaultDispatchHeartbeatInterval = 20 * time.Second
	DefaultDispatchTransportTimeout  = 30 * time.Second
	MinDispatchLeaseTTL              = 20 * time.Millisecond
)

// DispatchConfig controls outbox worker lease durations, heartbeat intervals,
// and bounded external SMTP transport deadlines. Zero values normalize to defaults.
type DispatchConfig struct {
	WorkerID          string
	LeaseTTL          time.Duration
	HeartbeatInterval time.Duration
	TransportTimeout  time.Duration
}

// DefaultDispatchConfig returns the canonical production dispatch timing configuration.
func DefaultDispatchConfig() DispatchConfig {
	return DispatchConfig{
		WorkerID:          "outbox-worker",
		LeaseTTL:          DefaultDispatchLeaseTTL,
		HeartbeatInterval: DefaultDispatchHeartbeatInterval,
		TransportTimeout:  DefaultDispatchTransportTimeout,
	}
}

// ValidateDispatchConfig normalizes omitted fields and validates safe timing invariants:
// LeaseTTL >= 20ms, 0 < HeartbeatInterval <= LeaseTTL/2, and 0 < TransportTimeout < LeaseTTL.
func ValidateDispatchConfig(cfg DispatchConfig) (DispatchConfig, error) {
	if cfg.LeaseTTL == 0 {
		cfg.LeaseTTL = DefaultDispatchLeaseTTL
	}
	errs := make(map[string]string)
	if cfg.LeaseTTL < MinDispatchLeaseTTL {
		errs["lease_ttl"] = fmt.Sprintf("must be at least %s", MinDispatchLeaseTTL)
	}
	if len(errs) == 0 {
		if cfg.HeartbeatInterval == 0 {
			if cfg.LeaseTTL == DefaultDispatchLeaseTTL {
				cfg.HeartbeatInterval = DefaultDispatchHeartbeatInterval
			} else {
				cfg.HeartbeatInterval = cfg.LeaseTTL / 3
				if cfg.HeartbeatInterval <= 0 {
					cfg.HeartbeatInterval = time.Millisecond
				}
			}
		}
		if cfg.HeartbeatInterval <= 0 || cfg.HeartbeatInterval > cfg.LeaseTTL/2 {
			errs["heartbeat_interval"] = "must be > 0 and at most half of lease_ttl"
		}
		if cfg.TransportTimeout == 0 {
			if cfg.LeaseTTL > DefaultDispatchTransportTimeout {
				cfg.TransportTimeout = DefaultDispatchTransportTimeout
			} else {
				cfg.TransportTimeout = cfg.LeaseTTL / 2
				if cfg.TransportTimeout <= 0 {
					cfg.TransportTimeout = time.Millisecond
				}
			}
		}
		if cfg.TransportTimeout <= 0 || cfg.TransportTimeout >= cfg.LeaseTTL {
			errs["transport_timeout"] = "must be > 0 and strictly less than lease_ttl"
		}
	}
	if len(errs) > 0 {
		return DispatchConfig{}, domain.NewValidationError(errs)
	}
	return cfg, nil
}

type activeDispatchHandle struct {
	workerID string
	token    uuid.UUID
	cancel   context.CancelFunc
}

// Service implements Phase 1–3 business operations with RBAC and atomic audit logging.
type Service struct {
	store                   *postgres.Store
	cfg                     config.Config
	secretBox               *secrets.Box
	payProvider             payments.Provider
	emailSender             mailer.Sender
	blobStore               storage.BlobStore
	blobInitErr             error
	attachmentDir           string
	nowOverride             func() time.Time
	beforeSendGuestLockHook func(guestID uuid.UUID)
	afterSendGuestLockHook  func(guestID uuid.UUID)

	dispatchMu       sync.Mutex
	dispatchCfg      DispatchConfig
	activeDispatches map[uuid.UUID]activeDispatchHandle

	channelMu           sync.Mutex
	channelAdapterCfg   channels.AdapterConfig
	channelConnLocks    map[uuid.UUID]*sync.Mutex
	channelSyncWorkerID string
}

// SetChannelAdapterConfig injects channel adapter HTTP/clock/test endpoint overrides.
// Only tests should supply ProviderBaseURLs / AllowHTTPForTests; production always uses official endpoints.
func (s *Service) SetChannelAdapterConfig(cfg channels.AdapterConfig) {
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	s.channelAdapterCfg = cfg
}

func (s *Service) effectiveChannelAdapterConfig() channels.AdapterConfig {
	s.channelMu.Lock()
	cfg := s.channelAdapterCfg
	s.channelMu.Unlock()
	if cfg.Clock == nil && s.nowOverride != nil {
		cfg.Clock = s.nowOverride
	}
	return cfg
}

func (s *Service) channelConnectionLock(connID uuid.UUID) *sync.Mutex {
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	if s.channelConnLocks == nil {
		s.channelConnLocks = make(map[uuid.UUID]*sync.Mutex)
	}
	mu, ok := s.channelConnLocks[connID]
	if !ok {
		mu = &sync.Mutex{}
		s.channelConnLocks[connID] = mu
	}
	return mu
}

// SetChannelSyncWorkerID overrides the durable worker identity for channel sync job leasing tests.
func (s *Service) SetChannelSyncWorkerID(workerID string) {
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	s.channelSyncWorkerID = strings.TrimSpace(workerID)
}

func (s *Service) effectiveChannelWorkerID() string {
	s.channelMu.Lock()
	w := s.channelSyncWorkerID
	s.channelMu.Unlock()
	if w != "" {
		return w
	}
	return "channel-sync-worker"
}

// SetDispatchConfig validates and stores worker lease, heartbeat, and transport timeout settings.
// Invalid timing relationships (such as HeartbeatInterval > LeaseTTL/2 or TransportTimeout >= LeaseTTL)
// are rejected so an external transport call cannot silently outlive lease safety bounds.
func (s *Service) SetDispatchConfig(cfg DispatchConfig) error {
	normalized, err := ValidateDispatchConfig(cfg)
	if err != nil {
		return err
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	s.dispatchCfg = normalized
	return nil
}

func (s *Service) effectiveDispatchConfig() DispatchConfig {
	s.dispatchMu.Lock()
	cfg := s.dispatchCfg
	s.dispatchMu.Unlock()
	normalized, err := ValidateDispatchConfig(cfg)
	if err != nil {
		return DefaultDispatchConfig()
	}
	return normalized
}

func (s *Service) registerActiveDispatch(id uuid.UUID, h activeDispatchHandle) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if s.activeDispatches == nil {
		s.activeDispatches = make(map[uuid.UUID]activeDispatchHandle)
	}
	s.activeDispatches[id] = h
}

func (s *Service) unregisterActiveDispatch(id uuid.UUID, token uuid.UUID) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if cur, ok := s.activeDispatches[id]; ok && cur.token == token {
		delete(s.activeDispatches, id)
	}
}

func (s *Service) cancelActiveDispatchIfPresent(id uuid.UUID) {
	s.dispatchMu.Lock()
	h, ok := s.activeDispatches[id]
	s.dispatchMu.Unlock()
	if ok && h.cancel != nil {
		h.cancel()
	}
}

func (s *Service) isDispatchActiveLocally(id uuid.UUID) bool {
	s.dispatchMu.Lock()
	_, ok := s.activeDispatches[id]
	s.dispatchMu.Unlock()
	return ok
}

// SetBeforeSendGuestLockHook registers a test-only synchronization hook fired inside SendGuestEmail
// immediately prior to locking the canonical guest cluster row. Passing nil clears the hook.
func (s *Service) SetBeforeSendGuestLockHook(fn func(guestID uuid.UUID)) {
	s.beforeSendGuestLockHook = fn
}

// SetAfterSendGuestLockHook registers a test-only synchronization hook fired inside SendGuestEmail
// while holding the PostgreSQL FOR UPDATE lock on the canonical guest cluster prior to queueing.
func (s *Service) SetAfterSendGuestLockHook(fn func(guestID uuid.UUID)) {
	s.afterSendGuestLockHook = fn
}

func New(store *postgres.Store, cfg config.Config) *Service {
	var box *secrets.Box
	if len(cfg.EncryptionKey) == 32 {
		if b, err := secrets.NewBox(cfg.EncryptionKey); err == nil {
			box = b
		}
	}
	var payProv payments.Provider
	if cfg.StripeSecretKey != "" {
		payProv = payments.NewStripeClient(cfg.StripeSecretKey, cfg.StripeAPIBaseURL)
	} else if cfg.StripeSandbox && !cfg.IsProduction() {
		origins := cfg.AllowedPublicOrigins()
		primaryOrigin := ""
		if len(origins) > 0 {
			primaryOrigin = origins[0]
		}
		payProv = payments.NewSandboxProvider(primaryOrigin)
		if cfg.StripeWebhookSecret == "" {
			cfg.StripeWebhookSecret = "whsec_sandbox_dev_only"
		}
	}
	var emailSnd mailer.Sender
	if strings.TrimSpace(cfg.SMTPAddr) != "" {
		emailSnd = &mailer.SMTPSender{
			Addr:     cfg.SMTPAddr,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
		}
	}
	attDir := strings.TrimSpace(cfg.AttachmentStorageDir)
	if attDir == "" {
		attDir = filepath.Join(os.TempDir(), "lovelace-attachments")
	}
	blobStore, blobInitErr := storage.NewLocalFSStore(attDir)
	if blobInitErr != nil {
		blobStore = nil
	}
	return &Service{
		store:         store,
		cfg:           cfg,
		secretBox:     box,
		payProvider:   payProv,
		emailSender:   emailSnd,
		blobStore:     blobStore,
		blobInitErr:   blobInitErr,
		attachmentDir: attDir,
	}
}

func (s *Service) HasEmailProvider() bool {
	return mailer.IsGenuineProvider(s.emailSender)
}

func (s *Service) EmailProviderKind() string {
	return mailer.ProviderKind(s.emailSender)
}

type ReadinessComponent struct {
	Status  string `json:"status"`
	Mode    string `json:"mode,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Pending int    `json:"pending,omitempty"`
	Failed  int    `json:"failed,omitempty"`
}

type ReadinessReport struct {
	Ready       bool               `json:"ready"`
	Database    ReadinessComponent `json:"database"`
	Payments    ReadinessComponent `json:"payments"`
	Email       ReadinessComponent `json:"email"`
	Public      ReadinessComponent `json:"public_api"`
	Attachments ReadinessComponent `json:"attachments"`
	Meta        map[string]any     `json:"meta,omitempty"`
}

// ValidateProductionStartup enforces fail-closed production requirements across
// Stripe keys, sandbox prevention, trusted public origins, and BFF API secret.
// When all properties have stripe_enabled=false, missing Stripe keys are
// permitted (property payments intentionally disabled).
func (s *Service) ValidateProductionStartup(ctx context.Context) error {
	if !s.cfg.IsProduction() {
		return nil
	}
	if s.cfg.StripeSandbox {
		return errors.New("TIRBACH_STRIPE_SANDBOX cannot be enabled in production")
	}
	if err := config.ValidatePublicOrigins(s.cfg.AllowedPublicOrigins(), s.cfg.IsProduction()); err != nil {
		return err
	}
	if len(s.cfg.AllowedPublicOrigins()) == 0 {
		return errors.New("TIRBACH_PUBLIC_ORIGIN must configure at least one HTTPS public origin in production")
	}
	if strings.TrimSpace(s.cfg.PublicAPISecret) == "" {
		return errors.New("TIRBACH_PUBLIC_API_SECRET is required in production to isolate public backend endpoints to trusted BFF callers")
	}
	if err := s.cfg.ValidateStripeKeys(); err != nil {
		return err
	}
	activeStripeProps, err := s.store.CountActiveStripeEnabledProperties(ctx)
	if err != nil {
		return fmt.Errorf("check stripe-enabled properties: %w", err)
	}
	if activeStripeProps > 0 && !s.cfg.IsStripeConfigured() {
		return errors.New("live Stripe payments are enabled on public property settings, but valid production live Stripe credentials (sk_live_/rk_live_ and whsec_) are not configured")
	}
	if (activeStripeProps > 0 || s.cfg.RequireEmail) && !s.HasEmailProvider() {
		return errors.New("confirmation email delivery (TIRBACH_SMTP_ADDR) is required in production whenever direct public payments are enabled or TIRBACH_REQUIRE_EMAIL=true")
	}
	if s.cfg.GuestPortalEnabled {
		activePortalProps, pErr := s.store.CountActivePortalEnabledProperties(ctx)
		if pErr != nil {
			return fmt.Errorf("check portal-enabled properties: %w", pErr)
		}
		if activePortalProps > 0 && !s.HasEmailProvider() {
			return errors.New("email delivery (TIRBACH_SMTP_ADDR) is required in production whenever the public guest portal is enabled on any property")
		}
	}
	if att := s.attachmentReadiness(); att.Status != "ok" {
		return fmt.Errorf("attachment storage (%s) is unavailable or not writable: %s", s.attachmentDir, att.Detail)
	}
	return nil
}

func (s *Service) attachmentReadiness() ReadinessComponent {
	if s.blobInitErr != nil {
		return ReadinessComponent{
			Status: "unhealthy",
			Detail: fmt.Sprintf("init %s: %v", s.attachmentDir, s.blobInitErr),
		}
	}
	if s.blobStore == nil {
		return ReadinessComponent{
			Status: "unhealthy",
			Detail: fmt.Sprintf("attachment store %s is not initialized", s.attachmentDir),
		}
	}
	if err := s.blobStore.HealthCheck(); err != nil {
		return ReadinessComponent{
			Status: "unhealthy",
			Detail: fmt.Sprintf("healthcheck %s: %v", s.attachmentDir, err),
		}
	}
	return ReadinessComponent{Status: "ok"}
}

func (s *Service) Readiness(ctx context.Context) ReadinessReport {
	attComp := s.attachmentReadiness()
	report := ReadinessReport{
		Ready:       attComp.Status == "ok",
		Attachments: attComp,
	}
	if err := s.store.Ping(ctx); err != nil {
		report.Ready = false
		report.Database = ReadinessComponent{Status: "unhealthy", Detail: "database unavailable"}
		report.Payments = ReadinessComponent{Status: "unknown"}
		report.Email = ReadinessComponent{Status: "unknown"}
		report.Public = ReadinessComponent{Status: "unknown"}
		return report
	}
	report.Database = ReadinessComponent{Status: "ok"}

	activeStripeProps, stripePropsErr := s.store.CountActiveStripeEnabledProperties(ctx)

	if s.cfg.IsProduction() {
		if s.cfg.StripeSandbox {
			report.Ready = false
			report.Payments = ReadinessComponent{Status: "misconfigured", Mode: "sandbox_forbidden", Detail: "sandbox mode forbidden in production"}
		} else if err := s.cfg.ValidateStripeKeys(); err != nil {
			report.Ready = false
			report.Payments = ReadinessComponent{Status: "misconfigured", Mode: "live", Detail: err.Error()}
		} else if stripePropsErr != nil {
			report.Ready = false
			report.Payments = ReadinessComponent{Status: "unknown", Mode: "live", Detail: "unable to determine property payment policy state"}
		} else if activeStripeProps == 0 && !s.cfg.IsStripeConfigured() {
			report.Payments = ReadinessComponent{Status: "disabled", Mode: "property_disabled", Detail: "stripe disabled at property policy level"}
		} else if s.cfg.IsStripeConfigured() {
			report.Payments = ReadinessComponent{Status: "ok", Mode: "live"}
		} else {
			report.Ready = false
			report.Payments = ReadinessComponent{Status: "misconfigured", Mode: "live", Detail: "missing Stripe live secret or webhook secret"}
		}

		if err := config.ValidatePublicOrigins(s.cfg.AllowedPublicOrigins(), s.cfg.IsProduction()); err != nil || len(s.cfg.AllowedPublicOrigins()) == 0 || strings.TrimSpace(s.cfg.PublicAPISecret) == "" {
			report.Ready = false
			detail := "missing TIRBACH_PUBLIC_API_SECRET or valid HTTPS TIRBACH_PUBLIC_ORIGIN"
			if err != nil {
				detail = err.Error()
			}
			report.Public = ReadinessComponent{Status: "misconfigured", Detail: detail}
		} else {
			report.Public = ReadinessComponent{Status: "ok"}
		}
	} else {
		if stripePropsErr != nil {
			report.Payments = ReadinessComponent{Status: "unknown", Detail: "unable to determine property payment policy state"}
		} else {
			mode := "disabled"
			status := "disabled"
			if s.cfg.IsStripeConfigured() {
				mode = "live"
				status = "ok"
			} else if s.cfg.StripeSandbox {
				mode = "sandbox"
				status = "ok"
			}
			report.Payments = ReadinessComponent{Status: status, Mode: mode}
		}
		report.Public = ReadinessComponent{Status: "ok"}
	}

	outboxSummary, outboxErr := s.store.GetOutboxSummary(ctx)
	if outboxErr != nil {
		if s.cfg.IsProduction() {
			report.Ready = false
		}
		report.Email = ReadinessComponent{
			Status: "unhealthy",
			Mode:   s.EmailProviderKind(),
			Detail: "unable to query email outbox summary",
		}
		return report
	}
	if !s.HasEmailProvider() {
		status := "unconfigured"
		detail := "no genuine SMTP provider configured; confirmation emails remain PENDING in outbox"
		if s.cfg.IsProduction() {
			if stripePropsErr != nil {
				status = "unknown"
				report.Ready = false
				detail = "unable to verify email requirement while property payment policies are unavailable"
			} else if activeStripeProps > 0 || s.cfg.RequireEmail {
				status = "misconfigured"
				report.Ready = false
				detail = "confirmation email provider is required in production when live public bookings are enabled or TIRBACH_REQUIRE_EMAIL=true"
			}
		}
		report.Email = ReadinessComponent{
			Status:  status,
			Mode:    s.EmailProviderKind(),
			Detail:  detail,
			Pending: outboxSummary.PendingCount,
			Failed:  outboxSummary.FailedCount,
		}
	} else {
		report.Email = ReadinessComponent{
			Status:  "configured",
			Mode:    s.EmailProviderKind(),
			Pending: outboxSummary.PendingCount,
			Failed:  outboxSummary.FailedCount,
		}
	}
	return report
}

func (s *Service) SetPaymentProvider(p payments.Provider) {
	s.payProvider = p
}

func (s *Service) SetEmailSender(sender mailer.Sender) {
	if sender != nil {
		s.emailSender = sender
	}
}

func (s *Service) SetClock(fn func() time.Time) {
	s.nowOverride = fn
}

func (s *Service) SetBlobStore(bs storage.BlobStore) {
	if bs != nil {
		s.blobStore = bs
		s.blobInitErr = nil
	}
}

func (s *Service) requirePerm(actor Actor, perm auth.Permission) error {
	if actor.UserID == uuid.Nil {
		return domain.ErrUnauthorized
	}
	if actor.MustRotatePassword {
		return domain.ErrPasswordRotationRequired
	}
	if !auth.HasPermission(actor.Role, perm) {
		return domain.ErrForbidden
	}
	return nil
}

// requirePropertyScope ensures property-scoped accounts cannot cross property boundaries
// once Phase 5 multi-site users are added. Global owner_admin (PropertyID == nil) can access all.
func (s *Service) requirePropertyScope(actor Actor, propertyID uuid.UUID) error {
	if actor.PropertyID != nil && *actor.PropertyID != propertyID {
		return domain.ErrForbidden
	}
	return nil
}

func (s *Service) recordAudit(
	ctx context.Context,
	tx pgx.Tx,
	actor Actor,
	propertyID *uuid.UUID,
	action, resourceType, resourceID string,
	before, after any,
) error {
	var beforeJSON, afterJSON json.RawMessage
	if before != nil {
		b, err := sanitizeAuditPayload(resourceType, before)
		if err != nil {
			return err
		}
		beforeJSON = b
	}
	if after != nil {
		a, err := sanitizeAuditPayload(resourceType, after)
		if err != nil {
			return err
		}
		afterJSON = a
	}

	var actorID *uuid.UUID
	if actor.UserID != uuid.Nil {
		id := actor.UserID
		actorID = &id
	}
	return s.store.AppendAuditLog(ctx, tx, domain.AuditLog{
		PropertyID:   propertyID,
		ActorUserID:  actorID,
		ActorEmail:   actor.Email,
		ActorRole:    string(actor.Role),
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		RequestID:    actor.RequestID,
		IPAddress:    actor.IPAddress,
		UserAgent:    actor.UserAgent,
		BeforeState:  beforeJSON,
		AfterState:   afterJSON,
	})
}

func (s *Service) nowUTC() time.Time {
	if s.nowOverride != nil {
		return s.nowOverride().UTC()
	}
	return time.Now().UTC()
}

// leaseClockParam returns time.Time{} in production (signaling PostgreSQL to evaluate
// clock_timestamp() as the single distributed clock authority) or the injected test clock
// when deterministic clock control is active in tests.
func (s *Service) leaseClockParam() time.Time {
	if s.nowOverride != nil {
		return s.nowOverride().UTC()
	}
	return time.Time{}
}
