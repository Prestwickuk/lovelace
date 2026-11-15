package domain

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	slugRegex = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	codeRegex = regexp.MustCompile(`^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$`)
)

// ParseDate parses an ISO-8601 calendar date (YYYY-MM-DD) into UTC midnight.
func ParseDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("must be YYYY-MM-DD")
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
}

// FormatDate formats a date as YYYY-MM-DD.
func FormatDate(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// Role represents an administrative RBAC role.
type Role string

const (
	RoleOwnerAdmin        Role = "owner_admin"
	RoleManager           Role = "manager"
	RoleStaff             Role = "staff"
	RoleMaintenanceStaff  Role = "maintenance_staff"
	RoleHousekeepingStaff Role = "housekeeping_staff"
	RoleViewer            Role = "viewer"
)

func (r Role) Valid() bool {
	switch r {
	case RoleOwnerAdmin, RoleManager, RoleStaff, RoleMaintenanceStaff, RoleHousekeepingStaff, RoleViewer:
		return true
	default:
		return false
	}
}

// UnitCategory classifies an accommodation unit type.
type UnitCategory string

const (
	CategoryGlamping     UnitCategory = "glamping"
	CategoryTentPitch    UnitCategory = "tent_pitch"
	CategoryTouringPitch UnitCategory = "touring_pitch"
	CategoryCabin        UnitCategory = "cabin"
	CategoryOther        UnitCategory = "other"
)

func (c UnitCategory) Valid() bool {
	switch c {
	case CategoryGlamping, CategoryTentPitch, CategoryTouringPitch, CategoryCabin, CategoryOther:
		return true
	default:
		return false
	}
}

// UnitStatus represents the operational readiness of a physical unit/pitch.
type UnitStatus string

const (
	UnitStatusActive      UnitStatus = "active"
	UnitStatusMaintenance UnitStatus = "maintenance"
	UnitStatusInactive    UnitStatus = "inactive"
	UnitStatusRetired     UnitStatus = "retired"
)

func (s UnitStatus) Valid() bool {
	switch s {
	case UnitStatusActive, UnitStatusMaintenance, UnitStatusInactive, UnitStatusRetired:
		return true
	default:
		return false
	}
}

// PricingModel defines how an extra/add-on is billed (ready for Phase 2/3).
type PricingModel string

const (
	PricingPerStay           PricingModel = "per_stay"
	PricingPerNight          PricingModel = "per_night"
	PricingPerPersonPerStay  PricingModel = "per_person_per_stay"
	PricingPerPersonPerNight PricingModel = "per_person_per_night"
)

func (m PricingModel) Valid() bool {
	switch m {
	case PricingPerStay, PricingPerNight, PricingPerPersonPerStay, PricingPerPersonPerNight:
		return true
	default:
		return false
	}
}

// BlockType classifies an operational availability block.
type BlockType string

const (
	BlockMaintenance     BlockType = "maintenance"
	BlockOwnerHold       BlockType = "owner_hold"
	BlockSeasonalClosure BlockType = "seasonal_closure"
	BlockWeatherClosure  BlockType = "weather_closure"
)

func (b BlockType) Valid() bool {
	switch b {
	case BlockMaintenance, BlockOwnerHold, BlockSeasonalClosure, BlockWeatherClosure:
		return true
	default:
		return false
	}
}

// DaysOfWeekMask encodes Mon..Sun as bits 0..6 (1..127).
type DaysOfWeekMask int16

const (
	DayMon DaysOfWeekMask = 1 << 0
	DayTue DaysOfWeekMask = 1 << 1
	DayWed DaysOfWeekMask = 1 << 2
	DayThu DaysOfWeekMask = 1 << 3
	DayFri DaysOfWeekMask = 1 << 4
	DaySat DaysOfWeekMask = 1 << 5
	DaySun DaysOfWeekMask = 1 << 6
	DayAll DaysOfWeekMask = 127
)

// MatchesWeekday returns true if the mask includes t.Weekday().
func (m DaysOfWeekMask) MatchesWeekday(wd time.Weekday) bool {
	var bit DaysOfWeekMask
	switch wd {
	case time.Monday:
		bit = DayMon
	case time.Tuesday:
		bit = DayTue
	case time.Wednesday:
		bit = DayWed
	case time.Thursday:
		bit = DayThu
	case time.Friday:
		bit = DayFri
	case time.Saturday:
		bit = DaySat
	case time.Sunday:
		bit = DaySun
	}
	return (m & bit) != 0
}

// Label returns a human-readable summary of active weekdays.
func (m DaysOfWeekMask) Label() string {
	if m == DayAll {
		return "All days"
	}
	if m == (DayFri | DaySat) {
		return "Fri–Sat"
	}
	if m == (DayMon | DayTue | DayWed | DayThu | DaySun) {
		return "Sun–Thu"
	}
	names := []struct {
		bit  DaysOfWeekMask
		name string
	}{
		{DayMon, "Mon"}, {DayTue, "Tue"}, {DayWed, "Wed"}, {DayThu, "Thu"},
		{DayFri, "Fri"}, {DaySat, "Sat"}, {DaySun, "Sun"},
	}
	var out []string
	for _, n := range names {
		if m&n.bit != 0 {
			out = append(out, n.name)
		}
	}
	return strings.Join(out, ", ")
}

// Property represents a physical campsite or glamping site.
type Property struct {
	ID             uuid.UUID       `json:"id"`
	Slug           string          `json:"slug"`
	Name           string          `json:"name"`
	Tagline        string          `json:"tagline"`
	CountryCode    string          `json:"country_code"`
	Region         string          `json:"region"`
	AddressLine1   string          `json:"address_line1"`
	AddressLine2   string          `json:"address_line2"`
	Town           string          `json:"town"`
	Postcode       string          `json:"postcode"`
	Timezone       string          `json:"timezone"`
	Currency       string          `json:"currency"`
	CheckInTime    string          `json:"check_in_time"`
	CheckOutTime   string          `json:"check_out_time"`
	VATRegistered  bool            `json:"vat_registered"`
	VATNumber      string          `json:"vat_number"`
	ContactEmail   string          `json:"contact_email"`
	ContactPhone   string          `json:"contact_phone"`
	Settings       json.RawMessage `json:"settings"`
	BrandingConfig json.RawMessage `json:"branding_config,omitempty"`
	Version        int             `json:"version"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func (p *Property) Validate() error {
	errs := map[string]string{}
	p.Name = strings.TrimSpace(p.Name)
	p.Slug = strings.TrimSpace(strings.ToLower(p.Slug))
	p.CountryCode = strings.TrimSpace(strings.ToUpper(p.CountryCode))
	p.Currency = strings.TrimSpace(strings.ToUpper(p.Currency))
	p.Timezone = strings.TrimSpace(p.Timezone)
	p.ContactEmail = strings.TrimSpace(p.ContactEmail)

	if p.Name == "" {
		errs["name"] = "is required"
	}
	if !slugRegex.MatchString(p.Slug) {
		errs["slug"] = "must be lowercase alphanumeric with hyphens"
	}
	if len(p.CountryCode) != 2 {
		errs["country_code"] = "must be 2-letter ISO code"
	}
	if len(p.Currency) != 3 {
		errs["currency"] = "must be 3-letter ISO-4217 code"
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil || p.Timezone == "" {
		errs["timezone"] = "must be a valid IANA timezone (e.g. Europe/London)"
	}
	if p.ContactEmail != "" {
		if _, err := mail.ParseAddress(p.ContactEmail); err != nil {
			errs["contact_email"] = "must be a valid email address"
		}
	}
	if _, err := time.Parse("15:04", normalizeHHMM(p.CheckInTime)); err != nil {
		errs["check_in_time"] = "must be HH:MM"
	}
	if _, err := time.Parse("15:04", normalizeHHMM(p.CheckOutTime)); err != nil {
		errs["check_out_time"] = "must be HH:MM"
	}
	if p.VATRegistered && strings.TrimSpace(p.VATNumber) == "" {
		errs["vat_number"] = "is required when VAT registered"
	}
	return NewValidationError(errs)
}

func normalizeHHMM(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 5 {
		return s[:5]
	}
	return s
}

// UnitType represents an accommodation category and base tariff.
type UnitType struct {
	ID                   uuid.UUID       `json:"id"`
	PropertyID           uuid.UUID       `json:"property_id"`
	Code                 string          `json:"code"`
	Name                 string          `json:"name"`
	Category             UnitCategory    `json:"category"`
	Description          string          `json:"description"`
	BaseCapacityAdults   int             `json:"base_capacity_adults"`
	MaxAdults            int             `json:"max_adults"`
	MaxChildren          int             `json:"max_children"`
	MaxInfants           int             `json:"max_infants"`
	MaxGuests            int             `json:"max_guests"`
	PetsAllowed          bool            `json:"pets_allowed"`
	MaxPets              int             `json:"max_pets"`
	BaseNightlyRatePence int64           `json:"base_nightly_rate_pence"`
	TaxRateBPS           int             `json:"tax_rate_bps"`
	MinNights            int             `json:"min_nights"`
	MaxNights            int             `json:"max_nights"`
	Amenities            []string        `json:"amenities"`
	Metadata             json.RawMessage `json:"metadata"`
	IsActive             bool            `json:"is_active"`
	IsPublicBookable     bool            `json:"is_public_bookable,omitempty"`
	SortOrder            int             `json:"sort_order"`
	Version              int             `json:"version"`
	ArchivedAt           *time.Time      `json:"archived_at,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

func (u *UnitType) Validate() error {
	errs := map[string]string{}
	u.Code = strings.TrimSpace(strings.ToLower(u.Code))
	u.Name = strings.TrimSpace(u.Name)
	u.Description = strings.TrimSpace(u.Description)

	if !slugRegex.MatchString(u.Code) {
		errs["code"] = "must be lowercase alphanumeric with hyphens"
	}
	if u.Name == "" {
		errs["name"] = "is required"
	}
	if !u.Category.Valid() {
		errs["category"] = "invalid category"
	}
	if u.BaseCapacityAdults < 1 {
		errs["base_capacity_adults"] = "must be at least 1"
	}
	if u.MaxAdults < 1 {
		errs["max_adults"] = "must be at least 1"
	}
	if u.MaxChildren < 0 {
		errs["max_children"] = "cannot be negative"
	}
	if u.MaxInfants < 0 {
		errs["max_infants"] = "cannot be negative"
	}
	if u.MaxGuests < 1 || u.MaxGuests < u.MaxAdults {
		errs["max_guests"] = "must be at least max_adults"
	}
	if !u.PetsAllowed {
		u.MaxPets = 0
	} else if u.MaxPets < 0 {
		errs["max_pets"] = "cannot be negative"
	}
	if u.BaseNightlyRatePence < 0 {
		errs["base_nightly_rate_pence"] = "cannot be negative"
	}
	if u.TaxRateBPS < 0 || u.TaxRateBPS > 10000 {
		errs["tax_rate_bps"] = "must be between 0 and 10000"
	}
	if u.MinNights < 1 {
		errs["min_nights"] = "must be at least 1"
	}
	if u.MaxNights < u.MinNights {
		errs["max_nights"] = "must be >= min_nights"
	}
	return NewValidationError(errs)
}

// Unit represents an individual physical pitch or glamping unit.
type Unit struct {
	ID               uuid.UUID       `json:"id"`
	PropertyID       uuid.UUID       `json:"property_id"`
	UnitTypeID       uuid.UUID       `json:"unit_type_id"`
	UnitTypeName     string          `json:"unit_type_name,omitempty"`
	Code             string          `json:"code"`
	Name             string          `json:"name"`
	Zone             string          `json:"zone"`
	Status           UnitStatus      `json:"status"`
	AccessNotes      string          `json:"access_notes"`
	OperationalNotes string          `json:"operational_notes"`
	Attributes       json.RawMessage `json:"attributes"`
	SortOrder        int             `json:"sort_order"`
	Version          int             `json:"version"`
	ArchivedAt       *time.Time      `json:"archived_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

func (u *Unit) Validate() error {
	errs := map[string]string{}
	u.Code = strings.TrimSpace(strings.ToUpper(u.Code))
	u.Name = strings.TrimSpace(u.Name)
	u.Zone = strings.TrimSpace(u.Zone)

	if u.UnitTypeID == uuid.Nil {
		errs["unit_type_id"] = "is required"
	}
	if !codeRegex.MatchString(u.Code) {
		errs["code"] = "must be alphanumeric with hyphens (e.g. BT-OAK)"
	}
	if u.Name == "" {
		errs["name"] = "is required"
	}
	if !u.Status.Valid() {
		errs["status"] = "invalid status"
	}
	return NewValidationError(errs)
}

// Extra represents an optional add-on or fee item.
type Extra struct {
	ID           uuid.UUID       `json:"id"`
	PropertyID   uuid.UUID       `json:"property_id"`
	Code         string          `json:"code"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	PricingModel PricingModel    `json:"pricing_model"`
	PricePence   int64           `json:"price_pence"`
	TaxRateBPS   int             `json:"tax_rate_bps"`
	MaxQuantity  int             `json:"max_quantity"`
	IsActive     bool            `json:"is_active"`
	SortOrder    int             `json:"sort_order"`
	Metadata     json.RawMessage `json:"metadata"`
	Version      int             `json:"version"`
	ArchivedAt   *time.Time      `json:"archived_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func (e *Extra) Validate() error {
	errs := map[string]string{}
	e.Code = strings.TrimSpace(strings.ToLower(e.Code))
	e.Name = strings.TrimSpace(e.Name)

	if !slugRegex.MatchString(e.Code) {
		errs["code"] = "must be lowercase alphanumeric with hyphens"
	}
	if e.Name == "" {
		errs["name"] = "is required"
	}
	if !e.PricingModel.Valid() {
		errs["pricing_model"] = "invalid pricing model"
	}
	if e.PricePence < 0 {
		errs["price_pence"] = "cannot be negative"
	}
	if e.TaxRateBPS < 0 || e.TaxRateBPS > 10000 {
		errs["tax_rate_bps"] = "must be between 0 and 10000"
	}
	if e.MaxQuantity < 1 {
		errs["max_quantity"] = "must be at least 1"
	}
	return NewValidationError(errs)
}

// RatePlan groups seasonal rate rules (e.g. Standard BAR, Non-Refundable).
type RatePlan struct {
	ID          uuid.UUID  `json:"id"`
	PropertyID  uuid.UUID  `json:"property_id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	IsDefault   bool       `json:"is_default"`
	IsActive    bool       `json:"is_active"`
	Priority    int        `json:"priority"`
	Version     int        `json:"version"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (r *RatePlan) Validate() error {
	errs := map[string]string{}
	r.Code = strings.TrimSpace(strings.ToLower(r.Code))
	r.Name = strings.TrimSpace(r.Name)
	if !slugRegex.MatchString(r.Code) {
		errs["code"] = "must be lowercase alphanumeric with hyphens"
	}
	if r.Name == "" {
		errs["name"] = "is required"
	}
	return NewValidationError(errs)
}

// RateRule overrides base unit_type rates over [StartDate, EndDate) and matching weekdays.
type RateRule struct {
	ID                uuid.UUID      `json:"id"`
	PropertyID        uuid.UUID      `json:"property_id"`
	RatePlanID        uuid.UUID      `json:"rate_plan_id"`
	RatePlanName      string         `json:"rate_plan_name,omitempty"`
	UnitTypeID        uuid.UUID      `json:"unit_type_id"`
	UnitTypeName      string         `json:"unit_type_name,omitempty"`
	Name              string         `json:"name"`
	StartDate         time.Time      `json:"start_date"`
	EndDate           time.Time      `json:"end_date"` // Exclusive upper bound
	DaysOfWeek        DaysOfWeekMask `json:"days_of_week"`
	NightlyRatePence  int64          `json:"nightly_rate_pence"`
	MinNightsOverride *int           `json:"min_nights_override,omitempty"`
	Priority          int            `json:"priority"`
	IsActive          bool           `json:"is_active"`
	Version           int            `json:"version"`
	ArchivedAt        *time.Time     `json:"archived_at,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

func (r *RateRule) Validate() error {
	errs := map[string]string{}
	r.Name = strings.TrimSpace(r.Name)
	if r.RatePlanID == uuid.Nil {
		errs["rate_plan_id"] = "is required"
	}
	if r.UnitTypeID == uuid.Nil {
		errs["unit_type_id"] = "is required"
	}
	if r.Name == "" {
		errs["name"] = "is required"
	}
	if r.StartDate.IsZero() || r.EndDate.IsZero() || !r.EndDate.After(r.StartDate) {
		errs["date_range"] = "end_date must be strictly after start_date"
	}
	if r.DaysOfWeek < 1 || r.DaysOfWeek > 127 {
		errs["days_of_week"] = "must select at least one day (1..127)"
	}
	if r.NightlyRatePence < 0 {
		errs["nightly_rate_pence"] = "cannot be negative"
	}
	if r.MinNightsOverride != nil && *r.MinNightsOverride < 1 {
		errs["min_nights_override"] = "must be at least 1"
	}
	return NewValidationError(errs)
}

// AvailabilityBlock marks a physical unit unavailable across [StartDate, EndDate).
type AvailabilityBlock struct {
	ID         uuid.UUID  `json:"id"`
	PropertyID uuid.UUID  `json:"property_id"`
	UnitID     uuid.UUID  `json:"unit_id"`
	UnitCode   string     `json:"unit_code,omitempty"`
	UnitName   string     `json:"unit_name,omitempty"`
	StartDate  time.Time  `json:"start_date"`
	EndDate    time.Time  `json:"end_date"` // Exclusive upper bound
	BlockType  BlockType  `json:"block_type"`
	Reason     string     `json:"reason"`
	CreatedBy  *uuid.UUID `json:"created_by,omitempty"`
	ReleasedAt *time.Time `json:"released_at,omitempty"`
	ReleasedBy *uuid.UUID `json:"released_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (b *AvailabilityBlock) Validate() error {
	errs := map[string]string{}
	b.Reason = strings.TrimSpace(b.Reason)
	if b.UnitID == uuid.Nil {
		errs["unit_id"] = "is required"
	}
	if b.StartDate.IsZero() || b.EndDate.IsZero() || !b.EndDate.After(b.StartDate) {
		errs["date_range"] = "end_date must be strictly after start_date"
	}
	if !b.BlockType.Valid() {
		errs["block_type"] = "invalid block_type"
	}
	return NewValidationError(errs)
}

// AdminUser represents a back-office operator account.
type AdminUser struct {
	ID                 uuid.UUID  `json:"id"`
	PropertyID         *uuid.UUID `json:"property_id,omitempty"`
	Email              string     `json:"email"`
	FullName           string     `json:"full_name"`
	Role               Role       `json:"role"`
	PasswordHash       string     `json:"-"`
	IsActive           bool       `json:"is_active"`
	MustRotatePassword bool       `json:"must_rotate_password"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
	Version            int        `json:"version"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func (u *AdminUser) ValidateNew() error {
	errs := map[string]string{}
	u.Email = strings.TrimSpace(strings.ToLower(u.Email))
	u.FullName = strings.TrimSpace(u.FullName)
	if _, err := mail.ParseAddress(u.Email); err != nil || u.Email == "" {
		errs["email"] = "must be a valid email address"
	}
	if u.FullName == "" {
		errs["full_name"] = "is required"
	}
	if !u.Role.Valid() {
		errs["role"] = "invalid role"
	}
	return NewValidationError(errs)
}

// AdminSession represents an authenticated operator session.
type AdminSession struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	TokenHash  []byte
	CSRFToken  string
	IPAddress  string
	UserAgent  string
	ExpiresAt  time.Time
	LastSeenAt time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

// AuditLog records an immutable administrative event (with optional redaction overlay metadata).
type AuditLog struct {
	ID              int64           `json:"id"`
	PropertyID      *uuid.UUID      `json:"property_id,omitempty"`
	ActorUserID     *uuid.UUID      `json:"actor_user_id,omitempty"`
	ActorEmail      string          `json:"actor_email"`
	ActorRole       string          `json:"actor_role"`
	Action          string          `json:"action"`
	ResourceType    string          `json:"resource_type"`
	ResourceID      string          `json:"resource_id"`
	RequestID       string          `json:"request_id"`
	IPAddress       string          `json:"ip_address"`
	UserAgent       string          `json:"user_agent"`
	BeforeState     json.RawMessage `json:"before_state,omitempty"`
	AfterState      json.RawMessage `json:"after_state,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	IsRedacted      bool            `json:"is_redacted,omitempty"`
	RedactionReason string          `json:"redaction_reason,omitempty"`
	RedactedBy      *uuid.UUID      `json:"redacted_by,omitempty"`
	RedactedAt      *time.Time      `json:"redacted_at,omitempty"`
}
