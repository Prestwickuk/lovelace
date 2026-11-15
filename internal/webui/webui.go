package webui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/branding"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/httpx"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/storage"
)

//go:embed templates/*.html static
var embeddedFS embed.FS

// Handler serves the server-rendered LOVELACE Admin Console.
type Handler struct {
	svc       *service.Service
	cfg       config.Config
	brand     branding.PlatformBrand
	theme     branding.ThemeSelection
	loginTmpl *template.Template
	errorTmpl *template.Template
	pages     map[string]*template.Template
}

// New creates a Handler with the default platform PWE-1 theme contract.
func New(svc *service.Service, cfg config.Config) (*Handler, error) {
	return NewWithTheme(svc, cfg, branding.DefaultTheme())
}

// NewWithTheme creates a Handler using a strictly allowlisted PWE-1 theme selection.
func NewWithTheme(svc *service.Service, cfg config.Config, theme branding.ThemeSelection) (*Handler, error) {
	funcs := template.FuncMap{
		"formatMoney": domain.FormatPence,
		"formatDate":  domain.FormatDate,
		"formatDatePtr": func(d *time.Time) string {
			if d == nil || d.IsZero() {
				return "—"
			}
			return domain.FormatDate(*d)
		},
		"dateInputValue": func(d *time.Time) string {
			if d == nil || d.IsZero() {
				return ""
			}
			return domain.FormatDate(*d)
		},
		"maintPriorityClass": func(p domain.MaintenancePriority) string {
			switch p {
			case domain.MaintPriorityUrgent:
				return "badge-err"
			case domain.MaintPriorityHigh:
				return "badge-warn"
			case domain.MaintPriorityNormal:
				return "badge-info"
			default:
				return "badge-muted"
			}
		},
		"maintStatusClass": func(s domain.MaintenanceStatus) string {
			switch s {
			case domain.MaintStatusCompleted, domain.MaintStatusClosed:
				return "badge-ok"
			case domain.MaintStatusInProgress, domain.MaintStatusScheduled, domain.MaintStatusTriaged:
				return "badge-info"
			case domain.MaintStatusReported, domain.MaintStatusAwaitingParts, domain.MaintStatusOnHold:
				return "badge-warn"
			default:
				return "badge-muted"
			}
		},
		"maintImpactClass": func(i domain.MaintenanceAvailabilityImpact) string {
			switch i {
			case domain.MaintImpactUnavailable:
				return "badge-err"
			case domain.MaintImpactPartial:
				return "badge-warn"
			default:
				return "badge-muted"
			}
		},
		"maintIsImage": func(contentType string) bool {
			return storage.IsInlinePreviewMIME(contentType)
		},
		"formatTime": func(v any) string {
			switch t := v.(type) {
			case time.Time:
				return t.UTC().Format("2006-01-02 15:04:05")
			case *time.Time:
				if t == nil {
					return "—"
				}
				return t.UTC().Format("2006-01-02 15:04:05")
			default:
				return "—"
			}
		},
		"formatLocalTime": func(t time.Time) string {
			loc, err := time.LoadLocation("Europe/London")
			if err != nil {
				loc = time.UTC
			}
			return t.In(loc).Format("2006-01-02 15:04 MST")
		},
		"hasUUID": func(ids []uuid.UUID, target uuid.UUID) bool {
			for _, id := range ids {
				if id == target {
					return true
				}
			}
			return false
		},
		"sameUUIDPtr": func(a uuid.UUID, b *uuid.UUID) bool {
			return b != nil && a == *b
		},
		"statusClass": func(status string) string {
			switch status {
			case "CONFIRMED", "CHECKED_IN", "PAID", "SUCCEEDED", "CAPTURED", "MANUALLY_RESOLVED", "AUTO_REFUNDED":
				return "badge-ok"
			case "PENDING", "INQUIRY", "PARTIALLY_PAID", "MANUAL":
				return "badge-warn"
			case "CANCELLED", "NO_SHOW", "FAILED", "UNPAID", "REFUNDED", "PARTIALLY_REFUNDED", "OPEN":
				return "badge-err"
			default:
				return "badge-muted"
			}
		},
		"releaseStatusLabel": func(r domain.AvailabilityReleaseRule) string {
			return r.ReleaseStatusLabel(time.Now().UTC())
		},
		"releaseStatusClass": func(r domain.AvailabilityReleaseRule) string {
			switch r.ReleaseStatusLabel(time.Now().UTC()) {
			case "SCHEDULED":
				return "badge-warn"
			case "RELEASED", "RELEASED_MANUALLY":
				return "badge-ok"
			default:
				return "badge-muted"
			}
		},
		"channelStatusClass": func(status string) string {
			switch status {
			case "CONNECTED", "CREDENTIALS_SAVED":
				return "badge-ok"
			case "AWAITING_AUTHORIZATION", "CONFIGURATION_REQUIRED":
				return "badge-warn"
			case "CONNECTION_ERROR":
				return "badge-err"
			default:
				return "badge-muted"
			}
		},
		"joinStrings": func(ss []string, sep string) string {
			return strings.Join(ss, sep)
		},
		"changeoverStageLabel": func(s domain.ChangeoverStage) string {
			return s.Label()
		},
		"changeoverStageClass": func(s domain.ChangeoverStage) string {
			switch s {
			case domain.StageReady:
				return "badge-ok"
			case domain.StageGuestDeparting, domain.StageInspectionRequired, domain.StageCleaningInProgress:
				return "badge-info"
			case domain.StageCleaningRequired:
				return "badge-warn"
			default:
				return "badge-muted"
			}
		},
		"attentionSeverityClass": func(s domain.OpsAttentionSeverity) string {
			switch s {
			case domain.SeverityUrgent:
				return "badge-err"
			case domain.SeverityHigh:
				return "badge-warn"
			default:
				return "badge-info"
			}
		},
		"occupancyStateClass": func(state string) string {
			switch state {
			case "CHECKED_IN", "RESERVED":
				return "badge-ok"
			case "PROVISIONAL":
				return "badge-warn"
			case "ARRIVING", "HOLD":
				return "badge-info"
			case "BLOCKED":
				return "badge-err"
			default:
				return "badge-muted"
			}
		},
		"formatMoneyCur": domain.FormatReportMoney,
		"formatMoneyCurPtr": func(cur string, v *int64) string {
			if v == nil {
				return "N/A"
			}
			return domain.FormatReportMoney(cur, *v)
		},
		"formatStrPtr": func(s *string) string {
			if s == nil {
				return "—"
			}
			return *s + "d"
		},
		"bpsToPct": func(bps *int) int {
			if bps == nil {
				return 0
			}
			v := (*bps + 50) / 100
			if v < 0 {
				return 0
			}
			if v > 100 {
				return 100
			}
			return v
		},
		"bpsLabel": func(bps *int) string {
			if bps == nil {
				return "N/A"
			}
			return fmt.Sprintf("%.2f%%", float64(*bps)/100.0)
		},
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict expects even number of arguments")
			}
			out := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				out[key] = values[i+1]
			}
			return out, nil
		},
		"statusGlyph": func(cls string) string {
			switch cls {
			case "badge-ok", "ok":
				return "✓"
			case "badge-warn", "warn":
				return "!"
			case "badge-err", "err":
				return "×"
			case "badge-info", "info":
				return "i"
			default:
				return "–"
			}
		},
	}

	loginTmpl, err := template.New("login.html").Funcs(funcs).ParseFS(embeddedFS, "templates/login.html", "templates/partials.html")
	if err != nil {
		return nil, err
	}
	errorTmpl, err := template.New("error.html").Funcs(funcs).ParseFS(embeddedFS, "templates/error.html", "templates/partials.html")
	if err != nil {
		return nil, err
	}

	pageNames := []string{
		"dashboard.html",
		"maintenance_list.html",
		"maintenance_new.html",
		"maintenance_detail.html",
		"maintenance_schedule.html",
		"maintenance_facilities.html",
		"calendar.html",
		"reservations.html",
		"reservation_new.html",
		"reservation_detail.html",
		"payment_exceptions.html",
		"property.html",
		"unit_types.html",
		"units.html",
		"extras.html",
		"rates.html",
		"blocks.html",
		"releases.html",
		"channel_manager.html",
		"users.html",
		"account.html",
		"audit.html",
		"guests_list.html",
		"guest_detail.html",
		"guest_merge.html",
		"enquiries_list.html",
		"enquiry_detail.html",
		"comms_settings.html",
		"reports.html",
	}
	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(embeddedFS, "templates/partials.html", "templates/layout.html", "templates/"+name)
		if err != nil {
			return nil, err
		}
		pages[name] = t
	}

	return &Handler{
		svc:       svc,
		cfg:       cfg,
		brand:     branding.Default(),
		theme:     branding.ResolveTheme(theme),
		loginTmpl: loginTmpl,
		errorTmpl: errorTmpl,
		pages:     pages,
	}, nil
}

// Register mounts static assets and admin UI routes onto mux.
func (h *Handler) Register(mux *http.ServeMux) {
	staticSub, _ := fs.Sub(embeddedFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	mux.HandleFunc("GET /login", h.handleLoginGet)
	mux.HandleFunc("POST /login", h.handleLoginPost)
	mux.HandleFunc("POST /logout", h.requirePageAuth(true, h.handleLogoutPost))

	mux.HandleFunc("GET /{$}", h.requirePageAuth(false, h.handleDashboard))
	mux.HandleFunc("GET /property", h.requirePageAuth(false, h.handlePropertyGet))
	mux.HandleFunc("POST /property", h.requirePageAuth(true, h.handlePropertyPost))
	mux.HandleFunc("POST /property/booking-policy", h.requirePageAuth(true, h.handleBookingPolicyPost))

	mux.HandleFunc("GET /unit-types", h.requirePageAuth(false, h.handleUnitTypesGet))
	mux.HandleFunc("POST /unit-types", h.requirePageAuth(true, h.handleUnitTypesCreate))
	mux.HandleFunc("POST /unit-types/{id}/update", h.requirePageAuth(true, h.handleUnitTypesUpdate))
	mux.HandleFunc("POST /unit-types/{id}/archive", h.requirePageAuth(true, h.handleUnitTypesArchive))

	mux.HandleFunc("GET /units", h.requirePageAuth(false, h.handleUnitsGet))
	mux.HandleFunc("POST /units", h.requirePageAuth(true, h.handleUnitsCreate))
	mux.HandleFunc("POST /units/{id}/update", h.requirePageAuth(true, h.handleUnitsUpdate))
	mux.HandleFunc("POST /units/{id}/archive", h.requirePageAuth(true, h.handleUnitsArchive))

	mux.HandleFunc("GET /extras", h.requirePageAuth(false, h.handleExtrasGet))
	mux.HandleFunc("POST /extras", h.requirePageAuth(true, h.handleExtrasCreate))
	mux.HandleFunc("POST /extras/{id}/update", h.requirePageAuth(true, h.handleExtrasUpdate))
	mux.HandleFunc("POST /extras/{id}/archive", h.requirePageAuth(true, h.handleExtrasArchive))

	mux.HandleFunc("GET /rates", h.requirePageAuth(false, h.handleRatesGet))
	mux.HandleFunc("POST /rate-plans", h.requirePageAuth(true, h.handleRatePlanCreate))
	mux.HandleFunc("POST /rate-rules", h.requirePageAuth(true, h.handleRateRuleCreate))
	mux.HandleFunc("POST /rate-rules/{id}/archive", h.requirePageAuth(true, h.handleRateRuleArchive))

	mux.HandleFunc("POST /blocks", h.requirePageAuth(true, h.handleBlocksCreate))
	mux.HandleFunc("POST /blocks/{id}/release", h.requirePageAuth(true, h.handleBlocksRelease))

	mux.HandleFunc("GET /users", h.requirePageAuth(false, h.handleUsersGet))
	mux.HandleFunc("POST /users", h.requirePageAuth(true, h.handleUsersCreate))
	mux.HandleFunc("POST /users/{id}/update", h.requirePageAuth(true, h.handleUsersUpdate))

	mux.HandleFunc("GET /account", h.requirePageAuth(false, h.handleAccountGet))
	mux.HandleFunc("POST /account/password", h.requirePageAuth(true, h.handleAccountPasswordPost))

	mux.HandleFunc("GET /audit-logs", h.requirePageAuth(false, h.handleAuditGet))

	h.registerPhase1_1Routes(mux)
	h.registerOperationsRoutes(mux)
	h.registerMaintenanceRoutes(mux)
	h.registerGuestCRMRoutes(mux)
	h.registerReportRoutes(mux)
}

type pageHandler func(w http.ResponseWriter, r *http.Request, actor service.Actor)

func (h *Handler) requirePageAuth(checkCSRF bool, next pageHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := httpx.ActorFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if checkCSRF {
			r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxFormBodyBytes)
			if err := r.ParseForm(); err != nil {
				h.renderError(w, http.StatusBadRequest, "Invalid Form Submission", "The submitted form payload could not be parsed.")
				return
			}
			if !httpx.VerifyCSRFToken(actor, r.FormValue("csrf_token")) {
				h.renderError(w, http.StatusForbidden, "Security Token Invalid", "Missing or invalid CSRF token. Please reload the page and try again.")
				return
			}
		}
		if actor.MustRotatePassword {
			switch r.URL.Path {
			case "/account", "/account/password", "/logout":
			default:
				http.Redirect(w, r, "/account?rotate=1", http.StatusSeeOther)
				return
			}
		}
		next(w, r, actor)
	}
}

type baseView struct {
	Title                 string
	ActiveNav             string
	Brand                 branding.PlatformBrand
	Theme                 branding.ThemeSelection
	Actor                 service.Actor
	CSRFToken             string
	Property              domain.Property
	Flash                 string
	Error                 string
	CanWriteProperty      bool
	CanWriteInventory     bool
	CanWriteRates         bool
	CanWriteBlocks        bool
	CanReadReservations   bool
	CanWriteReservations  bool
	CanWriteReleases      bool
	CanReadChannels       bool
	CanWriteChannels      bool
	CanManageUsers        bool
	CanViewAudit          bool
	CanWriteHousekeeping  bool
	CanReadMaintenance    bool
	CanReportMaintenance  bool
	CanWriteMaintenance   bool
	CanInspectMaintenance bool
	CanManageMaintenance  bool
	CanReadGuests         bool
	CanWriteGuests        bool
	CanViewGuestNotes     bool
	CanMergeGuests        bool
	CanExportGuests       bool
	CanAnonymizeGuests    bool
	CanReadComms          bool
	CanWriteComms         bool
	CanReconcileComms     bool
	CanManageTemplates    bool
	CanReadReports        bool
	CanExportReports      bool
}

func (h *Handler) buildBaseView(ctx context.Context, r *http.Request, actor service.Actor, title, nav string) (baseView, error) {
	props, err := h.svc.ListProperties(ctx, actor)
	if err != nil {
		return baseView{}, err
	}
	var prop domain.Property
	if len(props) > 0 {
		prop = props[0]
	}
	return baseView{
		Title:                 title,
		ActiveNav:             nav,
		Brand:                 h.brand,
		Theme:                 h.theme,
		Actor:                 actor,
		CSRFToken:             actor.CSRFToken,
		Property:              prop,
		Flash:                 r.URL.Query().Get("msg"),
		Error:                 r.URL.Query().Get("err"),
		CanWriteProperty:      auth.HasPermission(actor.Role, auth.PermPropertyWrite),
		CanWriteInventory:     auth.HasPermission(actor.Role, auth.PermInventoryWrite),
		CanWriteRates:         auth.HasPermission(actor.Role, auth.PermRatesWrite),
		CanWriteBlocks:        auth.HasPermission(actor.Role, auth.PermBlocksWrite),
		CanReadReservations:   auth.HasPermission(actor.Role, auth.PermReservationsRead),
		CanWriteReservations:  auth.HasPermission(actor.Role, auth.PermReservationsWrite),
		CanWriteReleases:      auth.HasPermission(actor.Role, auth.PermReleasesWrite),
		CanReadChannels:       auth.HasPermission(actor.Role, auth.PermChannelsRead),
		CanWriteChannels:      auth.HasPermission(actor.Role, auth.PermChannelsWrite),
		CanManageUsers:        auth.HasPermission(actor.Role, auth.PermUsersManage),
		CanViewAudit:          auth.HasPermission(actor.Role, auth.PermAuditRead),
		CanWriteHousekeeping:  auth.HasPermission(actor.Role, auth.PermHousekeepingWrite),
		CanReadMaintenance:    auth.HasPermission(actor.Role, auth.PermMaintenanceRead),
		CanReportMaintenance:  auth.HasPermission(actor.Role, auth.PermMaintenanceReport),
		CanWriteMaintenance:   auth.HasPermission(actor.Role, auth.PermMaintenanceWrite),
		CanInspectMaintenance: auth.HasPermission(actor.Role, auth.PermMaintenanceInspect),
		CanManageMaintenance:  auth.HasPermission(actor.Role, auth.PermMaintenanceManage),
		CanReadGuests:         auth.HasPermission(actor.Role, auth.PermGuestsRead),
		CanWriteGuests:        auth.HasPermission(actor.Role, auth.PermGuestsWrite),
		CanViewGuestNotes:     auth.HasPermission(actor.Role, auth.PermGuestsNotes),
		CanMergeGuests:        auth.HasPermission(actor.Role, auth.PermGuestsMerge),
		CanExportGuests:       auth.HasPermission(actor.Role, auth.PermGuestsExport),
		CanAnonymizeGuests:    auth.HasPermission(actor.Role, auth.PermGuestsAnonymize),
		CanReadComms:          auth.HasPermission(actor.Role, auth.PermCommsRead),
		CanWriteComms:         auth.HasPermission(actor.Role, auth.PermCommsWrite),
		CanReconcileComms:     auth.HasPermission(actor.Role, auth.PermCommsReconcile),
		CanManageTemplates:    auth.HasPermission(actor.Role, auth.PermCommsTemplates),
		CanReadReports:        auth.HasPermission(actor.Role, auth.PermReportsRead),
		CanExportReports:      auth.HasPermission(actor.Role, auth.PermReportsExport),
	}, nil
}

func (h *Handler) renderPage(w http.ResponseWriter, tmplName string, data any) {
	t, ok := h.pages[tmplName]
	if !ok {
		h.renderError(w, http.StatusInternalServerError, "Template Not Found", "The requested console view could not be found.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		h.renderError(w, http.StatusInternalServerError, "Render Error", "Unable to render the requested administrative view.")
	}
}

func (h *Handler) renderError(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = h.errorTmpl.Execute(w, struct {
		Status  int
		Title   string
		Message string
		Brand   branding.PlatformBrand
		Theme   branding.ThemeSelection
	}{
		Status:  status,
		Title:   title,
		Message: message,
		Brand:   h.brand,
		Theme:   h.theme,
	})
}

func redirectMsg(w http.ResponseWriter, r *http.Request, path, msg string) {
	http.Redirect(w, r, path+"?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) redirectErr(w http.ResponseWriter, r *http.Request, path string, err error) {
	msg := err.Error()
	if errors.Is(err, domain.ErrForbidden) {
		h.renderError(w, http.StatusForbidden, "Access Denied", "Your administrative role does not have permission for this action.")
		return
	}
	http.Redirect(w, r, path+"?err="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) renderDomainError(w http.ResponseWriter, err error, fallbackTitle, fallbackMessage string) {
	if errors.Is(err, domain.ErrForbidden) {
		h.renderError(w, http.StatusForbidden, "Access Denied", "Your administrative role does not have permission for this action.")
		return
	}
	if errors.Is(err, domain.ErrNotFound) {
		h.renderError(w, http.StatusNotFound, "Not Found", "The requested record could not be found.")
		return
	}
	h.renderError(w, http.StatusInternalServerError, fallbackTitle, fallbackMessage)
}

func parseIntDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func parseInt64Default(s string, def int64) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return def
	}
	return n
}

func parseOptionalUUID(s string) *uuid.UUID {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return &id
}
