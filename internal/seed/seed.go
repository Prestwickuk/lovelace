package seed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Prestwickuk/lovelace/internal/auth"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
)

// Options configures initial idempotent seeding for the sample demo property.
type Options struct {
	AdminEmail         string
	AdminName          string
	AdminPassword      string
	MustRotatePassword bool
}

// EnsureBaseline idempotently seeds a sample demo property, initial inventory, default rate plan,
// and an owner_admin account if missing.
func EnsureBaseline(ctx context.Context, store *postgres.Store, opts Options) (domain.Property, error) {
	prop, err := store.GetPropertyBySlug(ctx, nil, "demo-property")
	if errors.Is(err, domain.ErrNotFound) {
		propID, err := postgres.NewUUID()
		if err != nil {
			return domain.Property{}, err
		}
		prop, err = store.CreateProperty(ctx, nil, domain.Property{
			ID:           propID,
			Slug:         "demo-property",
			Name:         "Meadowbrook Estate",
			Tagline:      "Countryside glamping, handcrafted cabins & meadow camping",
			CountryCode:  "GB",
			Region:       "Countryside",
			Town:         "Market Town",
			Postcode:     "AB1 2CD",
			Timezone:     "Europe/London",
			Currency:     "GBP",
			CheckInTime:  "15:00",
			CheckOutTime: "10:30",
			ContactEmail: "hello@example.com",
		})
		if err != nil {
			return domain.Property{}, fmt.Errorf("create demo-property: %w", err)
		}
	} else if err != nil {
		return domain.Property{}, err
	}

	// Ensure initial owner_admin ONLY if no account with that email exists yet.
	// Existing admin accounts and passwords are never overwritten on restart.
	if opts.AdminEmail != "" && opts.AdminPassword != "" {
		if _, err := store.GetAdminUserByEmail(ctx, nil, opts.AdminEmail); errors.Is(err, domain.ErrNotFound) {
			hash, err := auth.HashPassword(opts.AdminPassword)
			if err != nil {
				return domain.Property{}, fmt.Errorf("hash admin password: %w", err)
			}
			uid, err := postgres.NewUUID()
			if err != nil {
				return domain.Property{}, err
			}
			name := opts.AdminName
			if name == "" {
				name = "Property Owner"
			}
			if _, err := store.CreateAdminUser(ctx, nil, domain.AdminUser{
				ID:                 uid,
				PropertyID:         nil, // global owner_admin
				Email:              opts.AdminEmail,
				FullName:           name,
				Role:               domain.RoleOwnerAdmin,
				PasswordHash:       hash,
				IsActive:           true,
				MustRotatePassword: opts.MustRotatePassword,
			}); err != nil {
				return domain.Property{}, fmt.Errorf("create owner_admin: %w", err)
			}
		} else if err != nil {
			return domain.Property{}, err
		}
	}

	// Seed sample unit types, units, extras, and default rate plan only if no unit types exist yet.
	existingUTs, err := store.ListUnitTypes(ctx, prop.ID, true)
	if err != nil {
		return domain.Property{}, err
	}
	if len(existingUTs) == 0 {
		if err := seedInventory(ctx, store, prop.ID); err != nil {
			return domain.Property{}, err
		}
	}

	if err := store.EnsureDefaultMaintenanceData(ctx, nil, prop.ID); err != nil {
		return domain.Property{}, fmt.Errorf("seed default maintenance data: %w", err)
	}
	if err := store.EnsureDefaultCommsTemplatesAndRules(ctx, prop.ID); err != nil {
		return domain.Property{}, fmt.Errorf("seed default comms templates: %w", err)
	}

	return prop, nil
}

func seedInventory(ctx context.Context, store *postgres.Store, propID uuid.UUID) error {
	return store.WithTx(ctx, func(tx pgx.Tx) error {
		bellID, _ := postgres.NewUUID()
		hutID, _ := postgres.NewUUID()
		grassID, _ := postgres.NewUUID()
		ehID, _ := postgres.NewUUID()

		unitTypes := []domain.UnitType{
			{
				ID:                   bellID,
				PropertyID:           propID,
				Code:                 "bell-tent",
				Name:                 "Furnished Bell Tent",
				Category:             domain.CategoryGlamping,
				Description:          "5m canvas bell tent with king bed, wool blankets, and private fire-pit.",
				BaseCapacityAdults:   2,
				MaxAdults:            2,
				MaxChildren:          2,
				MaxInfants:           1,
				MaxGuests:            4,
				PetsAllowed:          true,
				MaxPets:              1,
				BaseNightlyRatePence: 9500,
				MinNights:            2,
				MaxNights:            14,
				Amenities:            []string{"king-bed", "fire-pit", "solar-lighting", "picnic-bench"},
				IsActive:             true,
				SortOrder:            10,
			},
			{
				ID:                   hutID,
				PropertyID:           propID,
				Code:                 "shepherds-hut",
				Name:                 "Woodland Shepherd's Hut",
				Category:             domain.CategoryCabin,
				Description:          "Handcrafted timber shepherd's hut with wood-burning stove and valley views.",
				BaseCapacityAdults:   2,
				MaxAdults:            2,
				MaxChildren:          0,
				MaxInfants:           1,
				MaxGuests:            2,
				PetsAllowed:          false,
				MaxPets:              0,
				BaseNightlyRatePence: 12500,
				MinNights:            2,
				MaxNights:            14,
				Amenities:            []string{"wood-burner", "en-suite", "kitchenette", "fire-pit"},
				IsActive:             true,
				SortOrder:            20,
			},
			{
				ID:                   grassID,
				PropertyID:           propID,
				Code:                 "grass-pitch",
				Name:                 "Meadow Grass Tent Pitch",
				Category:             domain.CategoryTentPitch,
				Description:          "Spacious wildflower meadow pitch for tents (no electric hook-up).",
				BaseCapacityAdults:   2,
				MaxAdults:            4,
				MaxChildren:          4,
				MaxInfants:           2,
				MaxGuests:            6,
				PetsAllowed:          true,
				MaxPets:              2,
				BaseNightlyRatePence: 2800,
				MinNights:            1,
				MaxNights:            21,
				Amenities:            []string{"fire-pit-hire", "hot-showers", "washing-up-sink"},
				IsActive:             true,
				SortOrder:            30,
			},
			{
				ID:                   ehID,
				PropertyID:           propID,
				Code:                 "electric-pitch",
				Name:                 "Electric Hook-Up Pitch",
				Category:             domain.CategoryTouringPitch,
				Description:          "Level gravel/grass pitch with 16A hook-up for campervans or larger tents.",
				BaseCapacityAdults:   2,
				MaxAdults:            4,
				MaxChildren:          4,
				MaxInfants:           2,
				MaxGuests:            6,
				PetsAllowed:          true,
				MaxPets:              2,
				BaseNightlyRatePence: 3600,
				MinNights:            1,
				MaxNights:            21,
				Amenities:            []string{"16a-hookup", "hot-showers", "water-point"},
				IsActive:             true,
				SortOrder:            40,
			},
		}

		for _, ut := range unitTypes {
			if _, err := store.CreateUnitType(ctx, tx, ut); err != nil {
				return err
			}
		}

		units := []struct {
			utID uuid.UUID
			code string
			name string
			zone string
		}{
			{bellID, "BT-OAK", "Oak Bell Tent", "Top Meadow"},
			{bellID, "BT-ASH", "Ash Bell Tent", "Top Meadow"},
			{hutID, "SH-WOODLAND", "Woodland Shepherd's Hut", "Orchard Ridge"},
			{grassID, "GP-01", "Meadow Pitch 01", "Wildflower Field"},
			{grassID, "GP-02", "Meadow Pitch 02", "Wildflower Field"},
			{ehID, "EH-01", "Hook-Up Pitch 01", "Lower Paddock"},
		}
		for i, u := range units {
			uid, _ := postgres.NewUUID()
			if _, err := store.CreateUnit(ctx, tx, domain.Unit{
				ID:         uid,
				PropertyID: propID,
				UnitTypeID: u.utID,
				Code:       u.code,
				Name:       u.name,
				Zone:       u.zone,
				Status:     domain.UnitStatusActive,
				SortOrder:  (i + 1) * 10,
			}); err != nil {
				return err
			}
		}

		extras := []domain.Extra{
			{
				PropertyID:   propID,
				Code:         "firewood-basket",
				Name:         "Seasoned Firewood & Kindling Basket",
				Description:  "Locally coppiced hardwood logs, kindling, and natural firelighters.",
				PricingModel: domain.PricingPerStay,
				PricePence:   1200,
				MaxQuantity:  5,
				IsActive:     true,
				SortOrder:    10,
			},
			{
				PropertyID:   propID,
				Code:         "dog-stay",
				Name:         "Dog Stay Fee",
				Description:  "Welcomes well-behaved dogs on leads in pet-friendly pitches and bell tents.",
				PricingModel: domain.PricingPerNight,
				PricePence:   500,
				MaxQuantity:  2,
				IsActive:     true,
				SortOrder:    20,
			},
			{
				PropertyID:   propID,
				Code:         "breakfast-hamper",
				Name:         "Artisan Breakfast Hamper",
				Description:  "Local sourdough, farmhouse salted butter, preserves, and free-range eggs.",
				PricingModel: domain.PricingPerStay,
				PricePence:   2400,
				MaxQuantity:  3,
				IsActive:     true,
				SortOrder:    30,
			},
		}
		for _, ex := range extras {
			ex.ID, _ = postgres.NewUUID()
			if _, err := store.CreateExtra(ctx, tx, ex); err != nil {
				return err
			}
		}

		planID, _ := postgres.NewUUID()
		if _, err := store.CreateRatePlan(ctx, tx, domain.RatePlan{
			ID:          planID,
			PropertyID:  propID,
			Code:        "standard",
			Name:        "Standard Flexible Tariff",
			Description: "Default flexible seasonal rate plan for Meadowbrook Estate.",
			IsDefault:   true,
			IsActive:    true,
			Priority:    100,
		}); err != nil {
			return err
		}

		ruleID, _ := postgres.NewUUID()
		minThree := 3
		year := time.Now().UTC().Year()
		if _, err := store.CreateRateRule(ctx, tx, domain.RateRule{
			ID:                ruleID,
			PropertyID:        propID,
			RatePlanID:        planID,
			UnitTypeID:        bellID,
			Name:              "Peak Summer Weekend",
			StartDate:         time.Date(year, 7, 15, 0, 0, 0, 0, time.UTC),
			EndDate:           time.Date(year, 9, 1, 0, 0, 0, 0, time.UTC),
			DaysOfWeek:        domain.DayFri | domain.DaySat,
			NightlyRatePence:  13000,
			MinNightsOverride: &minThree,
			Priority:          200,
			IsActive:          true,
		}); err != nil {
			return err
		}

		return nil
	})
}
