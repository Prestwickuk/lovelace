package domain

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/tirbach/tir-bach/internal/branding"
)

// OnboardingPreset identifies the hospitality business archetype selected during first-time setup.
type OnboardingPreset string

const (
	OnboardingPresetCampsite OnboardingPreset = "campsite"
	OnboardingPresetAirbnb   OnboardingPreset = "airbnb_cottage"
	OnboardingPresetHotel    OnboardingPreset = "hotel_bnb"
	OnboardingPresetCustom   OnboardingPreset = "custom"
)

func (p OnboardingPreset) Valid() bool {
	switch p {
	case OnboardingPresetCampsite, OnboardingPresetAirbnb, OnboardingPresetHotel, OnboardingPresetCustom:
		return true
	default:
		return false
	}
}

// OnboardingAccommodationStarter provides a one-click accommodation category template for Step 2.
type OnboardingAccommodationStarter struct {
	Key                string
	Label              string
	Name               string
	Code               string
	Category           UnitCategory
	Description        string
	UnitPrefix         string
	DefaultQuantity    int
	Zone               string
	BaseCapacityAdults int
	MaxAdults          int
	MaxChildren        int
	MaxInfants         int
	MaxGuests          int
	PetsAllowed        bool
	MaxPets            int
	BaseNightlyPence   int64
	MinNights          int
	MaxNights          int
	AmenitiesCSV       string
}

// OnboardingExtraStarter provides a suggested optional extra/add-on for Step 3.
type OnboardingExtraStarter struct {
	Code         string
	Name         string
	Description  string
	PricingModel PricingModel
	PricePence   int64
	MaxQuantity  int
}

// OnboardingPresetSpec bundles preset labels, terminology, check-in/out times, and starter suggestions.
type OnboardingPresetSpec struct {
	Preset                 OnboardingPreset
	Title                  string
	Subtitle               string
	Terminology            branding.AdminTerminology
	DefaultCheckIn         string
	DefaultCheckOut        string
	DefaultCancellation    string
	DefaultQuietHours      string
	DefaultFirePolicy      string
	DefaultPetPolicy       string
	DefaultPrivacyNotice   string
	AccommodationTemplates []OnboardingAccommodationStarter
	SuggestedExtras        []OnboardingExtraStarter
}

// AllOnboardingPresets returns the ordered list of guided setup presets.
func AllOnboardingPresets() []OnboardingPresetSpec {
	return []OnboardingPresetSpec{
		{
			Preset:   OnboardingPresetCampsite,
			Title:    "Campsite, Glamping & Holiday Park",
			Subtitle: "Tent pitches, electric hook-ups, campervan bays, bell tents, yurts, or shepherd's huts.",
			Terminology: branding.AdminTerminology{
				UnitSingular:  "Pitch",
				UnitPlural:    "Pitches",
				GuestSingular: "Guest",
				GuestPlural:   "Guests",
				StayLabel:     "Booking",
			},
			DefaultCheckIn:       "15:00",
			DefaultCheckOut:      "10:30",
			DefaultCancellation:  "Full refund up to 14 days before arrival; 50% refund 7–14 days before arrival.",
			DefaultQuietHours:    "22:30 – 07:30 across all pitches and shared facilities.",
			DefaultFirePolicy:    "Raised fire-pits and off-ground BBQs only; no disposable BBQs directly on grass.",
			DefaultPetPolicy:     "Up to 2 well-behaved dogs welcome on leads; please do not leave pets unattended.",
			DefaultPrivacyNotice: "Guest contact details are used solely to manage your booking, arrival instructions, and statutory guest records.",
			AccommodationTemplates: []OnboardingAccommodationStarter{
				{
					Key:                "grass-pitch",
					Label:              "Grass Tent Pitch (×6)",
					Name:               "Meadow Grass Tent Pitch",
					Code:               "grass-pitch",
					Category:           CategoryTentPitch,
					Description:        "Spacious level grass pitch for tents with access to hot showers and washing-up sinks.",
					UnitPrefix:         "GP",
					DefaultQuantity:    6,
					Zone:               "Main Meadow",
					BaseCapacityAdults: 2,
					MaxAdults:          4,
					MaxChildren:        4,
					MaxInfants:         2,
					MaxGuests:          6,
					PetsAllowed:        true,
					MaxPets:            2,
					BaseNightlyPence:   2800,
					MinNights:          1,
					MaxNights:          21,
					AmenitiesCSV:       "hot-showers, washing-up-sink, fire-pit-hire",
				},
				{
					Key:                "electric-pitch",
					Label:              "Electric Hook-Up Pitch (×4)",
					Name:               "Electric Hook-Up Pitch",
					Code:               "electric-pitch",
					Category:           CategoryTouringPitch,
					Description:        "Hardstanding/grass pitch with 16A electric hook-up for campervans, motorhomes, or family tents.",
					UnitPrefix:         "EH",
					DefaultQuantity:    4,
					Zone:               "Touring Paddock",
					BaseCapacityAdults: 2,
					MaxAdults:          4,
					MaxChildren:        4,
					MaxInfants:         2,
					MaxGuests:          6,
					PetsAllowed:        true,
					MaxPets:            2,
					BaseNightlyPence:   3600,
					MinNights:          1,
					MaxNights:          21,
					AmenitiesCSV:       "16a-hookup, water-point, hot-showers",
				},
				{
					Key:                "bell-tent",
					Label:              "Furnished Glamping Unit (×2)",
					Name:               "Furnished Bell Tent",
					Code:               "bell-tent",
					Category:           CategoryGlamping,
					Description:        "Furnished 5m canvas bell tent with king bed, cosy bedding, and private outdoor seating.",
					UnitPrefix:         "BT",
					DefaultQuantity:    2,
					Zone:               "Glamping Glade",
					BaseCapacityAdults: 2,
					MaxAdults:          2,
					MaxChildren:        2,
					MaxInfants:         1,
					MaxGuests:          4,
					PetsAllowed:        true,
					MaxPets:            1,
					BaseNightlyPence:   9500,
					MinNights:          2,
					MaxNights:          14,
					AmenitiesCSV:       "king-bed, fire-pit, picnic-bench, solar-lighting",
				},
			},
			SuggestedExtras: []OnboardingExtraStarter{
				{
					Code:         "dog-stay",
					Name:         "Dog Stay Fee",
					Description:  "Per-night supplement for well-behaved dogs in pet-friendly pitches or glamping units.",
					PricingModel: PricingPerNight,
					PricePence:   500,
					MaxQuantity:  2,
				},
				{
					Code:         "firewood-basket",
					Name:         "Seasoned Firewood & Kindling Basket",
					Description:  "Kiln-dried hardwood logs, kindling, and natural firelighters.",
					PricingModel: PricingPerStay,
					PricePence:   1200,
					MaxQuantity:  5,
				},
				{
					Code:         "breakfast-hamper",
					Name:         "Local Breakfast Hamper",
					Description:  "Fresh local bread, butter, preserves, and free-range eggs.",
					PricingModel: PricingPerStay,
					PricePence:   2400,
					MaxQuantity:  3,
				},
				{
					Code:         "late-checkout",
					Name:         "Late Departure (12:30)",
					Description:  "Relaxed departure extension subject to changeover availability.",
					PricingModel: PricingPerStay,
					PricePence:   1500,
					MaxQuantity:  1,
				},
			},
		},
		{
			Preset:   OnboardingPresetAirbnb,
			Title:    "Holiday Let, Cottage or Airbnb",
			Subtitle: "Single cottage, cabin, lodge, apartment, or small collection of self-catering holiday homes.",
			Terminology: branding.AdminTerminology{
				UnitSingular:  "Property",
				UnitPlural:    "Properties",
				GuestSingular: "Guest",
				GuestPlural:   "Guests",
				StayLabel:     "Stay",
			},
			DefaultCheckIn:       "16:00",
			DefaultCheckOut:      "10:00",
			DefaultCancellation:  "Full refund up to 14 days before check-in; 50% refund 7–14 days before check-in.",
			DefaultQuietHours:    "Please respect neighbours between 22:00 and 08:00. No parties or unregistered events.",
			DefaultFirePolicy:    "Strictly non-smoking indoors. Use only seasoned logs in the wood-burner and outdoor BBQ in the patio area.",
			DefaultPetPolicy:     "Well-behaved dogs welcome by arrangement; please keep off beds and upstairs rooms.",
			DefaultPrivacyNotice: "Guest details are processed solely to administer your holiday let booking, key-safe access, and statutory records.",
			AccommodationTemplates: []OnboardingAccommodationStarter{
				{
					Key:                "entire-cottage",
					Label:              "Entire Cottage / Home (×1)",
					Name:               "Entire Holiday Cottage",
					Code:               "entire-cottage",
					Category:           CategoryCabin,
					Description:        "Self-contained holiday cottage with full kitchen, living room, private garden, and parking.",
					UnitPrefix:         "COT",
					DefaultQuantity:    1,
					Zone:               "Main Property",
					BaseCapacityAdults: 2,
					MaxAdults:          4,
					MaxChildren:        2,
					MaxInfants:         1,
					MaxGuests:          4,
					PetsAllowed:        true,
					MaxPets:            2,
					BaseNightlyPence:   14500,
					MinNights:          2,
					MaxNights:          28,
					AmenitiesCSV:       "wifi, full-kitchen, private-parking, wood-burner, garden",
				},
				{
					Key:                "garden-studio",
					Label:              "Studio / Annex (×1)",
					Name:               "Garden Annex Studio",
					Code:               "garden-studio",
					Category:           CategoryCabin,
					Description:        "Private self-catering studio for couples with kitchenette, en-suite shower room, and terrace.",
					UnitPrefix:         "STU",
					DefaultQuantity:    1,
					Zone:               "Garden",
					BaseCapacityAdults: 2,
					MaxAdults:          2,
					MaxChildren:        0,
					MaxInfants:         1,
					MaxGuests:          2,
					PetsAllowed:        false,
					MaxPets:            0,
					BaseNightlyPence:   9500,
					MinNights:          2,
					MaxNights:          21,
					AmenitiesCSV:       "wifi, kitchenette, en-suite, private-terrace",
				},
			},
			SuggestedExtras: []OnboardingExtraStarter{
				{
					Code:         "cleaning-fee",
					Name:         "End-of-Stay Linen & Housekeeping",
					Description:  "Fresh bed linen, towels, and full changeover clean.",
					PricingModel: PricingPerStay,
					PricePence:   4500,
					MaxQuantity:  1,
				},
				{
					Code:         "dog-stay",
					Name:         "Pet Stay Supplement",
					Description:  "Covers additional pet changeover cleaning for up to 2 dogs.",
					PricingModel: PricingPerStay,
					PricePence:   2500,
					MaxQuantity:  2,
				},
				{
					Code:         "welcome-hamper",
					Name:         "Artisan Welcome Hamper",
					Description:  "Local bakery loaf, butter, jam, milk, and Ground coffee waiting on arrival.",
					PricingModel: PricingPerStay,
					PricePence:   3000,
					MaxQuantity:  2,
				},
				{
					Code:         "late-checkout",
					Name:         "Late Check-Out (12:00)",
					Description:  "Extended departure until midday subject to changeover schedule.",
					PricingModel: PricingPerStay,
					PricePence:   2500,
					MaxQuantity:  1,
				},
			},
		},
		{
			Preset:   OnboardingPresetHotel,
			Title:    "Boutique Hotel, B&B or Guesthouse",
			Subtitle: "En-suite bedrooms, suites, or guesthouse rooms with optional breakfast and daily housekeeping.",
			Terminology: branding.AdminTerminology{
				UnitSingular:  "Room",
				UnitPlural:    "Rooms",
				GuestSingular: "Guest",
				GuestPlural:   "Guests",
				StayLabel:     "Reservation",
			},
			DefaultCheckIn:       "15:00",
			DefaultCheckOut:      "11:00",
			DefaultCancellation:  "Free cancellation up to 48 hours before check-in (15:00 local time); first night charged thereafter.",
			DefaultQuietHours:    "Quiet hours across guest corridors and lounges from 22:30 to 07:30.",
			DefaultFirePolicy:    "Strictly non-smoking and non-vaping throughout all guest bedrooms and indoor spaces.",
			DefaultPetPolicy:     "Dogs welcome in designated pet-friendly ground-floor rooms by prior arrangement.",
			DefaultPrivacyNotice: "Guest registration details are held securely to manage your room reservation and UK hospitality obligations.",
			AccommodationTemplates: []OnboardingAccommodationStarter{
				{
					Key:                "classic-double",
					Label:              "Classic Double Room (×4)",
					Name:               "Classic Double En-Suite",
					Code:               "classic-double",
					Category:           CategoryOther,
					Description:        "Comfortable double bedroom with en-suite rainfall shower, tea/coffee tray, and Wi-Fi.",
					UnitPrefix:         "RM",
					DefaultQuantity:    4,
					Zone:               "First Floor",
					BaseCapacityAdults: 2,
					MaxAdults:          2,
					MaxChildren:        0,
					MaxInfants:         1,
					MaxGuests:          2,
					PetsAllowed:        false,
					MaxPets:            0,
					BaseNightlyPence:   11500,
					MinNights:          1,
					MaxNights:          14,
					AmenitiesCSV:       "en-suite, wifi, tea-coffee, hairdryer",
				},
				{
					Key:                "king-suite",
					Label:              "King / Family Suite (×2)",
					Name:               "Superior King Suite",
					Code:               "king-suite",
					Category:           CategoryOther,
					Description:        "Spacious king suite with seating area, bath & shower en-suite, and space for children.",
					UnitPrefix:         "STE",
					DefaultQuantity:    2,
					Zone:               "Garden Wing",
					BaseCapacityAdults: 2,
					MaxAdults:          2,
					MaxChildren:        2,
					MaxInfants:         1,
					MaxGuests:          4,
					PetsAllowed:        true,
					MaxPets:            1,
					BaseNightlyPence:   16500,
					MinNights:          1,
					MaxNights:          14,
					AmenitiesCSV:       "king-bed, en-suite-bath, seating-area, wifi",
				},
			},
			SuggestedExtras: []OnboardingExtraStarter{
				{
					Code:         "cooked-breakfast",
					Name:         "Full Cooked & Continental Breakfast",
					Description:  "Freshly cooked morning breakfast per guest per night.",
					PricingModel: PricingPerPersonPerNight,
					PricePence:   1450,
					MaxQuantity:  4,
				},
				{
					Code:         "early-checkin",
					Name:         "Early Check-In (13:00)",
					Description:  "Priority room readiness from 13:00 on arrival day.",
					PricingModel: PricingPerStay,
					PricePence:   2000,
					MaxQuantity:  1,
				},
				{
					Code:         "late-checkout",
					Name:         "Late Check-Out (13:00)",
					Description:  "Extended departure until 13:00 on check-out day.",
					PricingModel: PricingPerStay,
					PricePence:   2500,
					MaxQuantity:  1,
				},
				{
					Code:         "dog-room-fee",
					Name:         "Pet-Friendly Room Supplement",
					Description:  "Nightly supplement for dogs staying in pet-designated rooms.",
					PricingModel: PricingPerNight,
					PricePence:   1500,
					MaxQuantity:  2,
				},
			},
		},
		{
			Preset:               OnboardingPresetCustom,
			Title:                "Custom / Mixed Hospitality Venue",
			Subtitle:             "Combine multiple unit styles with your own console labels, check-in times, and house rules.",
			Terminology:          branding.DefaultAdminTerminology(),
			DefaultCheckIn:       "15:00",
			DefaultCheckOut:      "10:30",
			DefaultCancellation:  "Full refund up to 14 days before arrival; 50% refund up to 7 days before arrival.",
			DefaultQuietHours:    "22:30 – 07:30.",
			DefaultFirePolicy:    "No smoking indoors; follow site fire safety notices.",
			DefaultPetPolicy:     "Pets by prior arrangement.",
			DefaultPrivacyNotice: "Guest contact details are used solely to manage your booking and arrival communications.",
			AccommodationTemplates: []OnboardingAccommodationStarter{
				{
					Key:                "standard-unit",
					Label:              "Standard Accommodation (×1)",
					Name:               "Standard Accommodation",
					Code:               "standard-unit",
					Category:           CategoryOther,
					Description:        "Primary bookable accommodation unit.",
					UnitPrefix:         "U",
					DefaultQuantity:    1,
					Zone:               "Main Area",
					BaseCapacityAdults: 2,
					MaxAdults:          2,
					MaxChildren:        2,
					MaxInfants:         1,
					MaxGuests:          4,
					PetsAllowed:        false,
					MaxPets:            0,
					BaseNightlyPence:   10000,
					MinNights:          1,
					MaxNights:          28,
					AmenitiesCSV:       "wifi, parking",
				},
			},
			SuggestedExtras: []OnboardingExtraStarter{
				{
					Code:         "late-checkout",
					Name:         "Late Check-Out",
					Description:  "Extended departure time subject to availability.",
					PricingModel: PricingPerStay,
					PricePence:   2000,
					MaxQuantity:  1,
				},
			},
		},
	}
}

// GetOnboardingPresetSpec returns the spec for preset, falling back to Campsite when unrecognized.
func GetOnboardingPresetSpec(preset OnboardingPreset) OnboardingPresetSpec {
	for _, s := range AllOnboardingPresets() {
		if s.Preset == preset {
			return s
		}
	}
	return AllOnboardingPresets()[0]
}

// OnboardingState tracks guided wizard progress inside Property.Settings["onboarding"].
type OnboardingState struct {
	Preset      OnboardingPreset `json:"preset"`
	CurrentStep int              `json:"current_step"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	UpdatedAt   *time.Time       `json:"updated_at,omitempty"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
}

func (s OnboardingState) IsComplete() bool {
	return s.CompletedAt != nil && !s.CompletedAt.IsZero()
}

// DecodeOnboardingState extracts OnboardingState from Property.Settings JSONB if present.
func DecodeOnboardingState(raw json.RawMessage) (OnboardingState, bool) {
	if len(raw) == 0 {
		return OnboardingState{}, false
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return OnboardingState{}, false
	}
	sub, ok := doc["onboarding"]
	if !ok || len(sub) == 0 {
		return OnboardingState{}, false
	}
	var st OnboardingState
	if err := json.Unmarshal(sub, &st); err != nil {
		return OnboardingState{}, false
	}
	if !st.Preset.Valid() {
		st.Preset = OnboardingPresetCampsite
	}
	if st.CurrentStep < 1 || st.CurrentStep > 6 {
		st.CurrentStep = 1
	}
	return st, true
}

// MergeOnboardingState writes state into Property.Settings["onboarding"] while preserving all sibling JSON keys.
func MergeOnboardingState(existing json.RawMessage, state OnboardingState) (json.RawMessage, error) {
	doc := make(map[string]json.RawMessage)
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &doc)
	}
	if !state.Preset.Valid() {
		state.Preset = OnboardingPresetCampsite
	}
	if state.CurrentStep < 1 {
		state.CurrentStep = 1
	}
	if state.CurrentStep > 6 {
		state.CurrentStep = 6
	}
	sub, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("marshal onboarding state: %w", err)
	}
	doc["onboarding"] = sub
	merged, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal property settings: %w", err)
	}
	return json.RawMessage(merged), nil
}

// DeriveSlug converts a human-readable property or accommodation name into a valid lowercase slug.
func DeriveSlug(input string, fallback string) string {
	s := strings.ToLower(strings.TrimSpace(input))
	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '/' || r == '&' || r == '+':
			if b.Len() > 0 && !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
		if b.Len() >= 48 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return fallback
	}
	// Avoid pure-digit slugs so URLs and audit codes remain unambiguous.
	allDigits := true
	for _, r := range out {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		out = "site-" + out
	}
	return out
}

// DeriveUnitPrefix builds an uppercase short code prefix (e.g. "Meadow Grass Pitch" -> "MGP").
func DeriveUnitPrefix(name string) string {
	words := strings.FieldsFunc(strings.TrimSpace(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	var initials strings.Builder
	for _, w := range words {
		for _, r := range w {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				initials.WriteRune(unicode.ToUpper(r))
				break
			}
		}
		if initials.Len() >= 4 {
			break
		}
	}
	if initials.Len() >= 2 {
		return initials.String()
	}
	var clean strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			clean.WriteRune(r)
		}
		if clean.Len() >= 3 {
			break
		}
	}
	if clean.Len() > 0 {
		return clean.String()
	}
	return "UNIT"
}

// ParseCurrencyMajorToMinor parses a major-unit decimal string (e.g. "95", "95.00", "£14.50") into exact minor units (pence/cents) without float rounding.
func ParseCurrencyMajorToMinor(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	for _, sym := range []string{"£", "$", "€", ","} {
		s = strings.ReplaceAll(s, sym, "")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("amount cannot be negative")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return 0, fmt.Errorf("must be a valid amount such as 95 or 95.00")
	}
	wholeStr := parts[0]
	if wholeStr == "" {
		wholeStr = "0"
	}
	whole, err := strconv.ParseInt(wholeStr, 10, 64)
	if err != nil || whole < 0 || whole > 250000 {
		return 0, fmt.Errorf("must be a valid amount between 0.00 and 250000.00")
	}
	var frac int64
	if len(parts) == 2 {
		f := parts[1]
		switch len(f) {
		case 0:
			frac = 0
		case 1:
			v, err := strconv.ParseInt(f, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid decimal places")
			}
			frac = v * 10
		case 2:
			v, err := strconv.ParseInt(f, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid decimal places")
			}
			frac = v
		default:
			return 0, fmt.Errorf("at most 2 decimal places are supported")
		}
	}
	return whole*100 + frac, nil
}

// FormatMinorAsDecimal formats minor units (pence) as "95.00" suitable for HTML inputs.
func FormatMinorAsDecimal(minor int64) string {
	if minor < 0 {
		return fmt.Sprintf("-%d.%02d", (-minor)/100, (-minor)%100)
	}
	return fmt.Sprintf("%d.%02d", minor/100, minor%100)
}
