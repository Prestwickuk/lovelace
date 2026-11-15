package app_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/app"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
)

func TestPhase321PropertyLevelDataIsolationAndConstraints(t *testing.T) {
	srv, store, svc, propA, _, _, _ := setupPhase2TestServer(t, "pub-p321-isolation")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	// Create Property B as an isolated second campsite property.
	propB, err := store.CreateProperty(ctx, nil, domain.Property{
		ID:           uuid.New(),
		Slug:         "nant-glas-holiday-park",
		Name:         "Nant Glas Holiday Park",
		Tagline:      "Carmarthenshire Valley Camping",
		CountryCode:  "GB",
		Region:       "Wales",
		AddressLine1: "Nant Glas Lane",
		Town:         "Llandeilo",
		Postcode:     "SA19 6AA",
		Timezone:     "Europe/London",
		Currency:     "GBP",
		CheckInTime:  "15:00",
		CheckOutTime: "10:00",
		ContactEmail: "hello@nantglas.example",
		ContactPhone: "01558 555000",
	})
	if err != nil {
		t.Fatalf("create Property B: %v", err)
	}

	// Seed Property A and Property B domain objects.
	catsA, err := svc.ListMaintenanceCategories(ctx, ownerActor, propA.ID, false)
	if err != nil || len(catsA) == 0 {
		t.Fatalf("list Property A categories: %v", err)
	}
	catA := catsA[0]
	locsA, err := svc.ListMaintenanceLocations(ctx, ownerActor, propA.ID, false)
	if err != nil || len(locsA) == 0 {
		t.Fatalf("list Property A locations: %v", err)
	}
	locA := locsA[0]
	unitsA, err := svc.ListUnits(ctx, ownerActor, propA.ID, nil)
	if err != nil || len(unitsA) == 0 {
		t.Fatalf("list Property A units: %v", err)
	}
	unitA := unitsA[0]

	assetA, err := svc.CreateMaintenanceAsset(ctx, ownerActor, domain.MaintenanceAsset{
		PropertyID:    propA.ID,
		AssetCode:     "A-GEN-01",
		Name:          "Property A Generator",
		LocationID:    &locA.ID,
		AssetCategory: "Equipment",
		Status:        domain.AssetStatusActive,
	})
	if err != nil {
		t.Fatalf("create Property A asset: %v", err)
	}
	activeCtrA, err := svc.CreateMaintenanceContractor(ctx, ownerActor, domain.MaintenanceContractor{
		PropertyID: propA.ID,
		Name:       "Evan active A",
		IsActive:   true,
	})
	if err != nil {
		t.Fatalf("create active contractor A: %v", err)
	}
	inactiveCtrA, err := svc.CreateMaintenanceContractor(ctx, ownerActor, domain.MaintenanceContractor{
		PropertyID: propA.ID,
		Name:       "Inactive Vendor A",
		IsActive:   false,
	})
	if err != nil {
		t.Fatalf("create inactive contractor A: %v", err)
	}

	// Property B counterparts
	catB, err := svc.CreateMaintenanceCategory(ctx, ownerActor, domain.MaintenanceCategory{
		PropertyID: propB.ID,
		Code:       "PLUMBING_B",
		Name:       "Plumbing B",
		IsActive:   true,
	})
	if err != nil {
		t.Fatalf("create Property B category: %v", err)
	}
	utB, err := svc.CreateUnitType(ctx, ownerActor, domain.UnitType{
		PropertyID:           propB.ID,
		Code:                 "POD-B",
		Name:                 "Pod Type B",
		Category:             domain.UnitCategory("glamping"),
		BaseCapacityAdults:   2,
		MaxAdults:            2,
		MaxGuests:            2,
		BaseNightlyRatePence: 10000,
		MinNights:            1,
		MaxNights:            30,
		IsActive:             true,
		IsPublicBookable:     true,
	})
	if err != nil {
		t.Fatalf("create Property B unit type: %v", err)
	}
	unitB, err := svc.CreateUnit(ctx, ownerActor, domain.Unit{
		PropertyID: propB.ID,
		UnitTypeID: utB.ID,
		Code:       "B-01",
		Name:       "Nant Pod 1",
		Status:     domain.UnitStatusActive,
	})
	if err != nil {
		t.Fatalf("create Property B unit: %v", err)
	}
	locB, err := svc.CreateMaintenanceLocation(ctx, ownerActor, domain.MaintenanceLocation{
		PropertyID: propB.ID,
		Code:       "SHOWER_B",
		Name:       "Property B Shower Block",
		IsActive:   true,
	})
	if err != nil {
		t.Fatalf("create Property B location: %v", err)
	}
	assetB, err := svc.CreateMaintenanceAsset(ctx, ownerActor, domain.MaintenanceAsset{
		PropertyID:    propB.ID,
		AssetCode:     "B-BOILER-01",
		Name:          "Property B Boiler",
		LocationID:    &locB.ID,
		UnitID:        &unitB.ID,
		AssetCategory: "Equipment",
		Status:        domain.AssetStatusActive,
	})
	if err != nil {
		t.Fatalf("create Property B asset: %v", err)
	}
	ctrB, err := svc.CreateMaintenanceContractor(ctx, ownerActor, domain.MaintenanceContractor{
		PropertyID: propB.ID,
		Name:       "Contractor B",
		IsActive:   true,
	})
	if err != nil {
		t.Fatalf("create Property B contractor: %v", err)
	}

	// Property-scoped staff users
	staffA, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propA.ID,
		Email:      "maint-a@tirbach.wales",
		FullName:   "Tech Property A",
		Role:       domain.RoleMaintenanceStaff,
		Password:   "propA-tech-pass-2026",
	})
	if err != nil {
		t.Fatalf("create staff A: %v", err)
	}
	staffB, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propB.ID,
		Email:      "maint-b@nantglas.example",
		FullName:   "Tech Property B",
		Role:       domain.RoleMaintenanceStaff,
		Password:   "propB-tech-pass-2026",
	})
	if err != nil {
		t.Fatalf("create staff B: %v", err)
	}

	// 1. Cross-property locations / units on MaintenanceLocation & MaintenanceAsset create/update must fail.
	if _, err := svc.CreateMaintenanceLocation(ctx, ownerActor, domain.MaintenanceLocation{
		PropertyID: propA.ID,
		Code:       "BAD_LOC",
		Name:       "Bad Cross Unit",
		UnitID:     &unitB.ID,
		IsActive:   true,
	}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("CreateMaintenanceLocation with Property B unit expected ErrValidation, got %v", err)
	}
	if _, err := svc.CreateMaintenanceAsset(ctx, ownerActor, domain.MaintenanceAsset{
		PropertyID:    propA.ID,
		AssetCode:     "BAD-ASSET-LOC",
		Name:          "Cross loc asset",
		LocationID:    &locB.ID,
		AssetCategory: "Equipment",
		Status:        domain.AssetStatusActive,
	}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("CreateMaintenanceAsset with Property B location expected ErrValidation, got %v", err)
	}
	if _, err := svc.CreateMaintenanceAsset(ctx, ownerActor, domain.MaintenanceAsset{
		PropertyID:    propA.ID,
		AssetCode:     "BAD-ASSET-UNIT",
		Name:          "Cross unit asset",
		UnitID:        &unitB.ID,
		AssetCategory: "Equipment",
		Status:        domain.AssetStatusActive,
	}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("CreateMaintenanceAsset with Property B unit expected ErrValidation, got %v", err)
	}
	assetA.LocationID = &locB.ID
	if _, err := svc.UpdateMaintenanceAsset(ctx, ownerActor, assetA); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("UpdateMaintenanceAsset with Property B location expected ErrValidation, got %v", err)
	}
	assetA.LocationID = &locA.ID
	assetA.UnitID = &unitB.ID
	if _, err := svc.UpdateMaintenanceAsset(ctx, ownerActor, assetA); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("UpdateMaintenanceAsset with Property B unit expected ErrValidation, got %v", err)
	}

	// 2. CreateMaintenanceRequest rejects cross-property refs & inactive/foreign assignees.
	badCreateCases := []struct {
		name string
		in   service.CreateMaintenanceRequestInput
	}{
		{"foreign_category", service.CreateMaintenanceRequestInput{PropertyID: propA.ID, Title: "x", CategoryID: catB.ID, LocationID: &locA.ID}},
		{"foreign_location", service.CreateMaintenanceRequestInput{PropertyID: propA.ID, Title: "x", CategoryID: catA.ID, LocationID: &locB.ID}},
		{"foreign_unit", service.CreateMaintenanceRequestInput{PropertyID: propA.ID, Title: "x", CategoryID: catA.ID, UnitID: &unitB.ID}},
		{"foreign_asset", service.CreateMaintenanceRequestInput{PropertyID: propA.ID, Title: "x", CategoryID: catA.ID, AssetID: &assetB.ID}},
		{"foreign_contractor", service.CreateMaintenanceRequestInput{PropertyID: propA.ID, Title: "x", CategoryID: catA.ID, LocationID: &locA.ID, AssignedContractorID: &ctrB.ID}},
		{"inactive_contractor", service.CreateMaintenanceRequestInput{PropertyID: propA.ID, Title: "x", CategoryID: catA.ID, LocationID: &locA.ID, AssignedContractorID: &inactiveCtrA.ID}},
		{"foreign_staff_user", service.CreateMaintenanceRequestInput{PropertyID: propA.ID, Title: "x", CategoryID: catA.ID, LocationID: &locA.ID, AssignedUserID: &staffB.ID}},
	}
	for _, tc := range badCreateCases {
		t.Run("create_req/"+tc.name, func(t *testing.T) {
			if _, err := svc.CreateMaintenanceRequest(ctx, ownerActor, tc.in); !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("%s expected ErrValidation, got %v", tc.name, err)
			}
		})
	}

	// Valid Property A creation with Property A staff + global ownerActor permitted.
	reqA1, err := svc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID:           propA.ID,
		Title:                "Valid Property A Request 1",
		CategoryID:           catA.ID,
		Priority:             domain.MaintPriorityNormal,
		LocationID:           &locA.ID,
		UnitID:               &unitA.ID,
		AssetID:              &assetA.ID,
		AssignedUserID:       &staffA.ID,
		AssignedContractorID: &activeCtrA.ID,
	})
	if err != nil {
		t.Fatalf("create valid Property A request 1: %v", err)
	}
	reqA2, err := svc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID:     propA.ID,
		Title:          "Valid Property A Request 2",
		CategoryID:     catA.ID,
		Priority:       domain.MaintPriorityNormal,
		LocationID:     &locA.ID,
		AssignedUserID: &owner.ID, // global admin allowed
	})
	if err != nil {
		t.Fatalf("create valid Property A request 2 with global user: %v", err)
	}
	reqB, err := svc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID:           propB.ID,
		Title:                "Valid Property B Request",
		CategoryID:           catB.ID,
		Priority:             domain.MaintPriorityNormal,
		LocationID:           &locB.ID,
		AssignedUserID:       &staffB.ID,
		AssignedContractorID: &ctrB.ID,
	})
	if err != nil {
		t.Fatalf("create valid Property B request: %v", err)
	}

	// 3. UpdateMaintenanceRequest & AssignMaintenanceRequest reject cross-property and inactive targets.
	upd := reqA1.Request
	upd.CategoryID = catB.ID
	if _, err := svc.UpdateMaintenanceRequest(ctx, ownerActor, upd); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("UpdateMaintenanceRequest with foreign category expected ErrValidation, got %v", err)
	}
	upd = reqA1.Request
	upd.LocationID = &locB.ID
	if _, err := svc.UpdateMaintenanceRequest(ctx, ownerActor, upd); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("UpdateMaintenanceRequest with foreign location expected ErrValidation, got %v", err)
	}
	upd = reqA1.Request
	upd.UnitID = &unitB.ID
	if _, err := svc.UpdateMaintenanceRequest(ctx, ownerActor, upd); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("UpdateMaintenanceRequest with foreign unit expected ErrValidation, got %v", err)
	}
	upd = reqA1.Request
	upd.AssetID = &assetB.ID
	if _, err := svc.UpdateMaintenanceRequest(ctx, ownerActor, upd); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("UpdateMaintenanceRequest with foreign asset expected ErrValidation, got %v", err)
	}

	if _, err := svc.AssignMaintenanceRequest(ctx, ownerActor, propA.ID, reqA1.Request.ID, nil, &ctrB.ID, nil, "foreign", reqA1.Request.Version); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("AssignMaintenanceRequest with foreign contractor expected ErrValidation, got %v", err)
	}
	if _, err := svc.AssignMaintenanceRequest(ctx, ownerActor, propA.ID, reqA1.Request.ID, nil, &inactiveCtrA.ID, nil, "inactive", reqA1.Request.Version); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("AssignMaintenanceRequest with inactive contractor expected ErrValidation, got %v", err)
	}
	if _, err := svc.AssignMaintenanceRequest(ctx, ownerActor, propA.ID, reqA1.Request.ID, &staffB.ID, nil, nil, "foreign staff", reqA1.Request.Version); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("AssignMaintenanceRequest with foreign staff expected ErrValidation, got %v", err)
	}

	// 4. Preventative schedule create & update reject cross-property & inactive references.
	dueDate := time.Now().UTC().AddDate(0, 0, 7).Truncate(24 * time.Hour)
	badSchedCases := []struct {
		name string
		sc   domain.MaintenanceSchedule
	}{
		{"foreign_cat", domain.MaintenanceSchedule{PropertyID: propA.ID, Title: "s", CategoryID: catB.ID, LocationID: &locA.ID, Frequency: domain.MaintFreqMonthly, NextDueDate: dueDate, IsActive: true}},
		{"foreign_loc", domain.MaintenanceSchedule{PropertyID: propA.ID, Title: "s", CategoryID: catA.ID, LocationID: &locB.ID, Frequency: domain.MaintFreqMonthly, NextDueDate: dueDate, IsActive: true}},
		{"foreign_unit", domain.MaintenanceSchedule{PropertyID: propA.ID, Title: "s", CategoryID: catA.ID, UnitID: &unitB.ID, Frequency: domain.MaintFreqMonthly, NextDueDate: dueDate, IsActive: true}},
		{"foreign_asset", domain.MaintenanceSchedule{PropertyID: propA.ID, Title: "s", CategoryID: catA.ID, AssetID: &assetB.ID, Frequency: domain.MaintFreqMonthly, NextDueDate: dueDate, IsActive: true}},
		{"foreign_contractor", domain.MaintenanceSchedule{PropertyID: propA.ID, Title: "s", CategoryID: catA.ID, LocationID: &locA.ID, AssignedContractorID: &ctrB.ID, Frequency: domain.MaintFreqMonthly, NextDueDate: dueDate, IsActive: true}},
		{"inactive_contractor", domain.MaintenanceSchedule{PropertyID: propA.ID, Title: "s", CategoryID: catA.ID, LocationID: &locA.ID, AssignedContractorID: &inactiveCtrA.ID, Frequency: domain.MaintFreqMonthly, NextDueDate: dueDate, IsActive: true}},
		{"foreign_staff", domain.MaintenanceSchedule{PropertyID: propA.ID, Title: "s", CategoryID: catA.ID, LocationID: &locA.ID, AssignedUserID: &staffB.ID, Frequency: domain.MaintFreqMonthly, NextDueDate: dueDate, IsActive: true}},
	}
	for _, tc := range badSchedCases {
		t.Run("create_sched/"+tc.name, func(t *testing.T) {
			if _, err := svc.CreateMaintenanceSchedule(ctx, ownerActor, tc.sc); !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("%s expected ErrValidation, got %v", tc.name, err)
			}
		})
	}
	schedA, err := svc.CreateMaintenanceSchedule(ctx, ownerActor, domain.MaintenanceSchedule{
		PropertyID:           propA.ID,
		Title:                "Valid Property A Schedule",
		CategoryID:           catA.ID,
		LocationID:           &locA.ID,
		AssignedUserID:       &staffA.ID,
		AssignedContractorID: &activeCtrA.ID,
		Frequency:            domain.MaintFreqMonthly,
		NextDueDate:          dueDate,
		IsActive:             true,
	})
	if err != nil {
		t.Fatalf("create valid Property A schedule: %v", err)
	}
	schedBad := schedA
	schedBad.AssignedContractorID = &inactiveCtrA.ID
	if _, err := svc.UpdateMaintenanceSchedule(ctx, ownerActor, schedBad); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("UpdateMaintenanceSchedule with inactive contractor expected ErrValidation, got %v", err)
	}
	schedBad = schedA
	schedBad.LocationID = &locB.ID
	if _, err := svc.UpdateMaintenanceSchedule(ctx, ownerActor, schedBad); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("UpdateMaintenanceSchedule with foreign location expected ErrValidation, got %v", err)
	}

	// 5. Attachment work_entry_id must belong to BOTH the same request AND same property.
	entryA1, err := svc.AddMaintenanceWorkEntry(ctx, ownerActor, propA.ID, reqA1.Request.ID, domain.WorkEntryGeneralNote, "Entry on reqA1", "")
	if err != nil {
		t.Fatalf("add entryA1: %v", err)
	}
	entryA2, err := svc.AddMaintenanceWorkEntry(ctx, ownerActor, propA.ID, reqA2.Request.ID, domain.WorkEntryGeneralNote, "Entry on reqA2", "")
	if err != nil {
		t.Fatalf("add entryA2: %v", err)
	}
	entryB, err := svc.AddMaintenanceWorkEntry(ctx, ownerActor, propB.ID, reqB.Request.ID, domain.WorkEntryGeneralNote, "Entry on propB", "")
	if err != nil {
		t.Fatalf("add entryB: %v", err)
	}

	pngPayload := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xFF, 0xFF, 0x3F,
		0x00, 0x05, 0xFE, 0x02, 0xFE, 0xDC, 0xCC, 0x59,
		0xE7, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
		0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	if _, err := svc.UploadMaintenanceAttachment(ctx, ownerActor, propA.ID, reqA1.Request.ID, &entryB.ID, "cross-prop.png", pngPayload, "bad", false); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("UploadMaintenanceAttachment with Property B work_entry_id expected ErrValidation, got %v", err)
	}
	if _, err := svc.UploadMaintenanceAttachment(ctx, ownerActor, propA.ID, reqA1.Request.ID, &entryA2.ID, "wrong-req.png", pngPayload, "bad", false); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("UploadMaintenanceAttachment with another request's work_entry_id in same property expected ErrValidation, got %v", err)
	}
	if _, err := svc.UploadMaintenanceAttachment(ctx, ownerActor, propA.ID, reqA1.Request.ID, &entryA1.ID, "good.png", pngPayload, "ok", false); err != nil {
		t.Fatalf("UploadMaintenanceAttachment with matching (property_id, request_id, work_entry_id) failed: %v", err)
	}

	// 6. Verify PostgreSQL composite FK constraints (0010) enforce isolation directly at SQL level.
	dbFKCases := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name: "fk_req_cross_location",
			query: `INSERT INTO maintenance_requests (id, property_id, reference, title, category_id, location_id)
			        VALUES ($1, $2, 'MNT-DB-0001', 'Direct DB cross loc', $3, $4)`,
			args: []any{uuid.New(), propA.ID, catA.ID, locB.ID},
		},
		{
			name: "fk_req_cross_contractor",
			query: `INSERT INTO maintenance_requests (id, property_id, reference, title, category_id, location_id, assigned_contractor_id)
			        VALUES ($1, $2, 'MNT-DB-0002', 'Direct DB cross ctr', $3, $4, $5)`,
			args: []any{uuid.New(), propA.ID, catA.ID, locA.ID, ctrB.ID},
		},
		{
			name: "fk_att_same_prop_wrong_request_work_entry",
			query: `INSERT INTO maintenance_attachments (id, property_id, request_id, work_entry_id, storage_key, original_filename, content_type, size_bytes, sha256)
			        VALUES ($1, $2, $3, $4, $5, 'x.png', 'image/png', 10, 'abc')`,
			args: []any{uuid.New(), propA.ID, reqA1.Request.ID, entryA2.ID, "k-" + uuid.NewString()},
		},
	}
	for _, tc := range dbFKCases {
		t.Run("db_constraint/"+tc.name, func(t *testing.T) {
			err := store.WithTx(ctx, func(tx pgx.Tx) error {
				_, execErr := tx.Exec(ctx, tc.query, tc.args...)
				return execErr
			})
			if err == nil {
				t.Fatalf("%s: expected PostgreSQL composite FK violation, got nil", tc.name)
			}
		})
	}
}

func TestPhase321InspectionCycleInvalidatedAfterReopen(t *testing.T) {
	srv, store, svc, prop, _, _, _ := setupPhase2TestServer(t, "pub-p321-insp")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	cats, err := svc.ListMaintenanceCategories(ctx, ownerActor, prop.ID, false)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	locs, err := svc.ListMaintenanceLocations(ctx, ownerActor, prop.ID, false)
	if err != nil {
		t.Fatalf("list locations: %v", err)
	}

	// 1. Create inspection-required request.
	det, err := svc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID:         prop.ID,
		Title:              "Electrical distribution board fault",
		CategoryID:         cats[0].ID,
		Priority:           domain.MaintPriorityHigh,
		LocationID:         &locs[0].ID,
		InspectionRequired: true,
	})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	// 2. Work -> Completed -> Initial PASSED inspection -> Closed.
	det, err = svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, det.Request.ID, domain.MaintStatusInProgress, "Isolating circuit", nil, det.Request.Version)
	if err != nil {
		t.Fatalf("to IN_PROGRESS: %v", err)
	}
	if _, err := svc.AddMaintenanceWorkEntry(ctx, ownerActor, prop.ID, det.Request.ID, domain.WorkEntryPerformed, "Replaced breaker", "32A RCBO"); err != nil {
		t.Fatalf("add work entry: %v", err)
	}
	det, err = svc.GetMaintenanceRequestDetail(ctx, ownerActor, prop.ID, det.Request.ID)
	if err != nil {
		t.Fatalf("reload detail cycle 1: %v", err)
	}
	det, err = svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, det.Request.ID, domain.MaintStatusCompleted, "Initial repair complete", nil, det.Request.Version)
	if err != nil {
		t.Fatalf("to COMPLETED cycle 1: %v", err)
	}
	det, insp1, err := svc.RecordMaintenanceInspection(ctx, ownerActor, prop.ID, det.Request.ID, domain.MaintInspectionPassed, "Cycle 1 inspection passed", false, det.Request.Version)
	if err != nil {
		t.Fatalf("record inspection 1: %v", err)
	}
	if det.Request.LatestInspectionResult != "PASSED" {
		t.Fatalf("expected LatestInspectionResult PASSED in cycle 1, got %q", det.Request.LatestInspectionResult)
	}
	det, err = svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, det.Request.ID, domain.MaintStatusClosed, "Closing after cycle 1 pass", nil, det.Request.Version)
	if err != nil {
		t.Fatalf("to CLOSED cycle 1: %v", err)
	}

	// 3. Reopen -> must invalidate current inspection approval while keeping insp1 in history.
	det, err = svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, det.Request.ID, domain.MaintStatusInProgress, "Intermittent trip reported again", nil, det.Request.Version)
	if err != nil {
		t.Fatalf("reopen to IN_PROGRESS: %v", err)
	}
	if det.Request.CompletedAt != nil || det.Request.ClosedAt != nil || det.Request.LatestInspectionResult != "" {
		t.Fatalf("reopen must clear completed_at/closed_at and current inspection approval; got completed=%v closed=%v latest=%q",
			det.Request.CompletedAt, det.Request.ClosedAt, det.Request.LatestInspectionResult)
	}
	if len(det.Inspections) != 1 || det.Inspections[0].ID != insp1.ID {
		t.Fatalf("historical inspection must remain intact after reopen: %+v", det.Inspections)
	}

	// 4. Further rework -> Completed again -> old PASSED inspection must NOT allow closure.
	if _, err := svc.AddMaintenanceWorkEntry(ctx, ownerActor, prop.ID, det.Request.ID, domain.WorkEntryPerformed, "Replaced busbar connector", "Copper link"); err != nil {
		t.Fatalf("add rework entry: %v", err)
	}
	det, err = svc.GetMaintenanceRequestDetail(ctx, ownerActor, prop.ID, det.Request.ID)
	if err != nil {
		t.Fatalf("reload detail cycle 2: %v", err)
	}
	det, err = svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, det.Request.ID, domain.MaintStatusCompleted, "Rework complete", nil, det.Request.Version)
	if err != nil {
		t.Fatalf("to COMPLETED cycle 2: %v", err)
	}
	if det.Request.CompletedAt == nil || !det.Request.CompletedAt.After(insp1.InspectedAt) {
		t.Fatalf("expected cycle 2 completed_at (%v) strictly after cycle 1 inspection (%v)", det.Request.CompletedAt, insp1.InspectedAt)
	}
	if det.Request.LatestInspectionResult != "" {
		t.Fatalf("expected stale cycle 1 PASSED inspection not to satisfy cycle 2, got LatestInspectionResult=%q", det.Request.LatestInspectionResult)
	}
	awaiting, err := svc.ListMaintenanceRequests(ctx, ownerActor, prop.ID, domain.MaintenanceRequestFilter{Status: "AWAITING_INSPECTION"})
	if err != nil || len(awaiting) != 1 || awaiting[0].ID != det.Request.ID {
		t.Fatalf("expected recompleted reopened request in AWAITING_INSPECTION list, got %+v err=%v", awaiting, err)
	}
	if _, err := svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, det.Request.ID, domain.MaintStatusClosed, "Attempt close with stale inspection", nil, det.Request.Version); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation when closing with pre-reopen inspection, got %v", err)
	}

	// Also verify direct repository call refuses stale inspection.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	err = store.WithTx(ctx, func(tx pgx.Tx) error {
		_, txErr := store.TransitionMaintenanceStatus(ctx, tx, prop.ID, det.Request.ID, domain.MaintStatusClosed, ownerActor.UserID, "direct close bypass", nil, det.Request.Version, today)
		return txErr
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected store.TransitionMaintenanceStatus to reject stale inspection closure, got %v", err)
	}

	// 5. Record NEW PASSED inspection after cycle 2 completion -> closure succeeds and both inspections preserved.
	det, insp2, err := svc.RecordMaintenanceInspection(ctx, ownerActor, prop.ID, det.Request.ID, domain.MaintInspectionPassed, "Cycle 2 inspection passed", false, det.Request.Version)
	if err != nil {
		t.Fatalf("record inspection 2: %v", err)
	}
	if !insp2.InspectedAt.After(insp1.InspectedAt) || det.Request.LatestInspectionResult != "PASSED" {
		t.Fatalf("expected new PASSED inspection after cycle 2 completion; latest=%q insp2=%v insp1=%v", det.Request.LatestInspectionResult, insp2.InspectedAt, insp1.InspectedAt)
	}
	if len(det.Inspections) != 2 {
		t.Fatalf("expected both historical inspections preserved, got %d", len(det.Inspections))
	}
	closedDet, err := svc.TransitionMaintenanceStatus(ctx, ownerActor, prop.ID, det.Request.ID, domain.MaintStatusClosed, "Closing after cycle 2 pass", nil, det.Request.Version)
	if err != nil {
		t.Fatalf("close after new PASSED inspection failed: %v", err)
	}
	if closedDet.Request.Status != domain.MaintStatusClosed {
		t.Fatalf("expected CLOSED, got %s", closedDet.Request.Status)
	}
}

func TestPhase321OverdueScheduleCatchUpAndConcurrency(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p321-catchup")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	cats, err := svc.ListMaintenanceCategories(ctx, ownerActor, prop.ID, false)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	locs, err := svc.ListMaintenanceLocations(ctx, ownerActor, prop.ID, false)
	if err != nil {
		t.Fatalf("list locations: %v", err)
	}

	// 1. Seed a historical preventative schedule starting 2024-01-31 (day 31 anchor).
	startDate := time.Date(2024, time.January, 31, 0, 0, 0, 0, time.UTC)
	var curNowMu sync.Mutex
	curNow := startDate.Add(8 * time.Hour)
	svc.SetClock(func() time.Time {
		curNowMu.Lock()
		defer curNowMu.Unlock()
		return curNow
	})

	monthlySched, err := svc.CreateMaintenanceSchedule(ctx, ownerActor, domain.MaintenanceSchedule{
		PropertyID:         prop.ID,
		Title:              "End-of-Month Septic Tank Inspection",
		Description:        "Anchored on 31st day of month",
		CategoryID:         cats[0].ID,
		Priority:           domain.MaintPriorityHigh,
		LocationID:         &locs[0].ID,
		Frequency:          domain.MaintFreqMonthly,
		NextDueDate:        startDate,
		AnchorDay:          31,
		InspectionRequired: true,
		IsActive:           true,
	})
	if err != nil {
		t.Fatalf("create monthly schedule: %v", err)
	}

	// Run on 2024-01-31 -> generates historical 2024-01-31 occurrence and advances to 2024-02-29 (leap!).
	gen0, err := svc.RunDuePreventativeSchedules(ctx, ownerActor, prop.ID, &startDate)
	if err != nil || len(gen0) != 1 {
		t.Fatalf("expected 1 initial occurrence, got %d err=%v", len(gen0), err)
	}
	if gen0[0].ScheduledOccurrenceDate == nil || !gen0[0].ScheduledOccurrenceDate.Equal(startDate) {
		t.Fatalf("expected initial occurrence 2024-01-31, got %v", gen0[0].ScheduledOccurrenceDate)
	}
	schedAfter0, err := svc.ListMaintenanceSchedules(ctx, ownerActor, prop.ID, true)
	if err != nil || len(schedAfter0) != 1 {
		t.Fatalf("list schedules after initial run: %v", err)
	}
	wantFeb29 := time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC)
	if !schedAfter0[0].NextDueDate.Equal(wantFeb29) || schedAfter0[0].AnchorDay != 31 {
		t.Fatalf("expected next_due_date=2024-02-29 anchor=31, got %s anchor=%d", schedAfter0[0].NextDueDate.Format("2006-01-02"), schedAfter0[0].AnchorDay)
	}

	// Also add a weekly schedule ~100 weeks overdue as of 2026-05-15 (last ran 2024-06-03).
	weeklyStart := time.Date(2024, time.June, 3, 0, 0, 0, 0, time.UTC)
	weeklySched, err := svc.CreateMaintenanceSchedule(ctx, ownerActor, domain.MaintenanceSchedule{
		PropertyID:  prop.ID,
		Title:       "Weekly Legionella Flush",
		CategoryID:  cats[0].ID,
		Priority:    domain.MaintPriorityNormal,
		LocationID:  &locs[0].ID,
		Frequency:   domain.MaintFreqWeekly,
		NextDueDate: weeklyStart,
		IsActive:    true,
	})
	if err != nil {
		t.Fatalf("create weekly schedule: %v", err)
	}

	// 2. Jump clock ~27 months ahead to 2026-05-15 and run background worker SweepDueMaintenanceSchedules.
	asOf := time.Date(2026, time.May, 15, 0, 0, 0, 0, time.UTC)
	curNowMu.Lock()
	curNow = asOf.Add(9 * time.Hour)
	curNowMu.Unlock()
	_ = clk

	swept1, err := svc.SweepDueMaintenanceSchedules(ctx)
	if err != nil {
		t.Fatalf("SweepDueMaintenanceSchedules overdue run: %v", err)
	}
	if swept1 != 2 {
		t.Fatalf("expected consolidated catch-up to emit exactly 2 work orders (1 per overdue schedule, no burst!), got %d", swept1)
	}

	// Verify next repeated 30s ticks emit ZERO extra work orders.
	for i := 0; i < 3; i++ {
		again, err := svc.SweepDueMaintenanceSchedules(ctx)
		if err != nil || again != 0 {
			t.Fatalf("tick %d after consolidated catch-up expected 0 new orders, got %d err=%v", i, again, err)
		}
	}

	// Verify schedule next_due_date values jumped strictly into the future (> 2026-05-15) preserving anchor 31 -> 2026-05-31!
	allScheds, err := svc.ListMaintenanceSchedules(ctx, ownerActor, prop.ID, true)
	if err != nil {
		t.Fatalf("list schedules after catch-up: %v", err)
	}
	for _, sc := range allScheds {
		switch sc.ID {
		case monthlySched.ID:
			wantNext := time.Date(2026, time.May, 31, 0, 0, 0, 0, time.UTC)
			if !sc.NextDueDate.Equal(wantNext) {
				t.Fatalf("monthly anchor 31 expected next_due_date 2026-05-31, got %s", sc.NextDueDate.Format("2006-01-02"))
			}
			wantLast := time.Date(2026, time.April, 30, 0, 0, 0, 0, time.UTC)
			if sc.LastGeneratedForDate == nil || !sc.LastGeneratedForDate.Equal(wantLast) {
				t.Fatalf("monthly expected last_generated_for_date 2026-04-30, got %v", sc.LastGeneratedForDate)
			}
		case weeklySched.ID:
			if !sc.NextDueDate.After(asOf) {
				t.Fatalf("weekly next_due_date (%s) must be strictly > %s", sc.NextDueDate.Format("2006-01-02"), asOf.Format("2006-01-02"))
			}
		}
	}

	// Verify historical 2024-01-31 request remained completely intact + catch-up request contains missed interval context.
	reqs, err := svc.ListMaintenanceRequests(ctx, ownerActor, prop.ID, domain.MaintenanceRequestFilter{Status: "ALL"})
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	if len(reqs) != 3 {
		t.Fatalf("expected exactly 3 requests (1 historical + 2 consolidated catch-up), got %d", len(reqs))
	}
	foundHistorical := false
	foundMonthlyCatchUp := false
	for _, r := range reqs {
		if r.ScheduleID != nil && *r.ScheduleID == monthlySched.ID && r.ScheduledOccurrenceDate != nil {
			if r.ScheduledOccurrenceDate.Equal(startDate) {
				foundHistorical = true
			}
			if r.ScheduledOccurrenceDate.Equal(time.Date(2026, time.April, 30, 0, 0, 0, 0, time.UTC)) && strings.Contains(r.InternalNotes, "Overdue Catch-Up") {
				foundMonthlyCatchUp = true
			}
		}
	}
	if !foundHistorical || !foundMonthlyCatchUp {
		t.Fatalf("missing historical or catch-up monthly request: historical=%v catchUp=%v", foundHistorical, foundMonthlyCatchUp)
	}

	// 3. Concurrent SweepDueMaintenanceSchedules across 4 new due schedules: zero errors, zero duplicates.
	for i := 0; i < 4; i++ {
		if _, err := svc.CreateMaintenanceSchedule(ctx, ownerActor, domain.MaintenanceSchedule{
			PropertyID:  prop.ID,
			Title:       fmt.Sprintf("Concurrent Rule %d", i+1),
			CategoryID:  cats[0].ID,
			Priority:    domain.MaintPriorityNormal,
			LocationID:  &locs[0].ID,
			Frequency:   domain.MaintFreqOnce,
			NextDueDate: asOf,
			IsActive:    true,
		}); err != nil {
			t.Fatalf("create concurrent schedule %d: %v", i, err)
		}
	}
	startGate := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	totalCreated := 0
	var sweepErrs []error
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startGate
			n, sErr := svc.SweepDueMaintenanceSchedules(ctx)
			mu.Lock()
			totalCreated += n
			if sErr != nil {
				sweepErrs = append(sweepErrs, sErr)
			}
			mu.Unlock()
		}()
	}
	close(startGate)
	wg.Wait()
	if len(sweepErrs) > 0 {
		t.Fatalf("concurrent SweepDueMaintenanceSchedules returned errors: %v", sweepErrs)
	}
	if totalCreated != 4 {
		t.Fatalf("expected exactly 4 created requests across 8 concurrent workers, got %d", totalCreated)
	}
}

func TestPhase321AttachmentStorageReadinessAndStartupFailure(t *testing.T) {
	_, store, _, prop, _, _, _ := setupPhase2TestServer(t, "pub-p321-storage")
	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	cats, _ := store.ListMaintenanceCategories(ctx, nil, prop.ID, false)
	locs, _ := store.ListMaintenanceLocations(ctx, nil, prop.ID, false)

	// Disable Stripe on the seeded test property so ValidateProductionStartup isolates attachment storage.
	pol, err := store.GetBookingPolicy(ctx, nil, prop.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	pol.StripeEnabled = false
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		var uErr error
		pol, uErr = store.UpdateBookingPolicy(ctx, tx, pol)
		return uErr
	}); err != nil {
		t.Fatalf("disable stripe policy: %v", err)
	}

	// 1. Misconfigured AttachmentStorageDir (path points to regular file) fails ValidateProductionStartup and Readiness/readyz.
	tmp := t.TempDir()
	blockerFile := filepath.Join(tmp, "not-a-directory")
	if err := os.WriteFile(blockerFile, []byte("occupied"), 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	badProdCfg := config.Config{
		AppEnv:               "production",
		DatabaseURL:          os.Getenv("TEST_DATABASE_URL"),
		CookieSecure:         true,
		EncryptionKey:        testEncryptionKey,
		PublicOrigin:         "https://book.tirbach.wales",
		PublicAPISecret:      "prod-storage-secret-0123456789",
		AttachmentStorageDir: blockerFile,
	}
	badSvc := service.New(store, badProdCfg)
	if err := badSvc.ValidateProductionStartup(ctx); err == nil || !strings.Contains(err.Error(), "attachment storage") {
		t.Fatalf("expected ValidateProductionStartup to fail on invalid attachment storage, got %v", err)
	}
	repBad := badSvc.Readiness(ctx)
	if repBad.Ready || repBad.Attachments.Status != "unhealthy" {
		t.Fatalf("expected Ready=false and attachments=unhealthy, got %+v", repBad)
	}
	badRouter, err := app.NewRouter(badProdCfg, store, badSvc)
	if err != nil {
		t.Fatalf("build badRouter: %v", err)
	}
	badSrv := httptest.NewServer(badRouter)
	defer badSrv.Close()
	codeBad, bodyBad := fetchReadyz(t, badSrv.URL)
	attBad := bodyBad["attachments"].(map[string]any)
	if codeBad != http.StatusServiceUnavailable || bodyBad["ready"] != false || attBad["status"] != "unhealthy" {
		t.Fatalf("expected 503 with attachments=unhealthy, got code=%d body=%+v", codeBad, bodyBad)
	}

	// Verify upload fails with explicit storage unavailable error (rather than silent nil panic/discard).
	req, err := badSvc.CreateMaintenanceRequest(ctx, ownerActor, service.CreateMaintenanceRequestInput{
		PropertyID: prop.ID,
		Title:      "Storage failure upload check",
		CategoryID: cats[0].ID,
		LocationID: &locs[0].ID,
	})
	if err != nil {
		t.Fatalf("create req: %v", err)
	}
	if _, err := badSvc.UploadMaintenanceAttachment(ctx, ownerActor, prop.ID, req.Request.ID, nil, "test.txt", []byte("hello"), "note", false); err == nil || !strings.Contains(err.Error(), "attachment storage") {
		t.Fatalf("expected explicit attachment storage error on upload, got %v", err)
	}

	// 2. Healthy storage passes ValidateProductionStartup & /readyz (200), then degrades live to 503 when directory becomes missing.
	healthyDir := filepath.Join(tmp, "attachments-ok")
	okCfg := badProdCfg
	okCfg.AttachmentStorageDir = healthyDir
	okSvc := service.New(store, okCfg)
	if err := okSvc.ValidateProductionStartup(ctx); err != nil {
		t.Fatalf("expected healthy ValidateProductionStartup to succeed, got %v", err)
	}
	okRouter, err := app.NewRouter(okCfg, store, okSvc)
	if err != nil {
		t.Fatalf("build okRouter: %v", err)
	}
	okSrv := httptest.NewServer(okRouter)
	defer okSrv.Close()
	codeOK, bodyOK := fetchReadyz(t, okSrv.URL)
	attOK := bodyOK["attachments"].(map[string]any)
	if codeOK != http.StatusOK || bodyOK["ready"] != true || attOK["status"] != "ok" {
		t.Fatalf("expected 200 ready=true with attachments=ok, got code=%d body=%+v", codeOK, bodyOK)
	}

	if err := os.RemoveAll(healthyDir); err != nil {
		t.Fatalf("remove healthyDir: %v", err)
	}
	codeDown, bodyDown := fetchReadyz(t, okSrv.URL)
	attDown := bodyDown["attachments"].(map[string]any)
	if codeDown != http.StatusServiceUnavailable || bodyDown["ready"] != false || attDown["status"] != "unhealthy" {
		t.Fatalf("expected 503 not_ready when storage dir removed at runtime, got code=%d body=%+v", codeDown, bodyDown)
	}
}

func TestPhase322PreventativeScheduleAssigneeValidation(t *testing.T) {
	_, store, svc, propA, _, _, _ := setupPhase2TestServer(t, "pub-p322-sched")
	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{UserID: owner.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role}

	catsA, err := svc.ListMaintenanceCategories(ctx, ownerActor, propA.ID, false)
	if err != nil || len(catsA) == 0 {
		t.Fatalf("list categories: %v", err)
	}
	locsA, err := svc.ListMaintenanceLocations(ctx, ownerActor, propA.ID, false)
	if err != nil || len(locsA) == 0 {
		t.Fatalf("list locations: %v", err)
	}

	staff, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propA.ID,
		Email:      "sched-tech@tirbach.wales",
		FullName:   "Schedule Tech",
		Role:       domain.RoleMaintenanceStaff,
		Password:   "sched-tech-pass-2026",
	})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}
	ctr, err := svc.CreateMaintenanceContractor(ctx, ownerActor, domain.MaintenanceContractor{
		PropertyID: propA.ID,
		Name:       "Schedule Contractor",
		IsActive:   true,
	})
	if err != nil {
		t.Fatalf("create contractor: %v", err)
	}

	propB, err := store.CreateProperty(ctx, nil, domain.Property{
		ID:           uuid.New(),
		Slug:         "sched-prop-b",
		Name:         "Sched Property B",
		CountryCode:  "GB",
		Timezone:     "Europe/London",
		Currency:     "GBP",
		CheckInTime:  "15:00",
		CheckOutTime: "10:00",
	})
	if err != nil {
		t.Fatalf("create propB: %v", err)
	}
	staffMoved, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		PropertyID: &propA.ID,
		Email:      "sched-moved@tirbach.wales",
		FullName:   "Schedule Moved Tech",
		Role:       domain.RoleMaintenanceStaff,
		Password:   "sched-moved-pass-2026",
	})
	if err != nil {
		t.Fatalf("create staffMoved: %v", err)
	}
	activeCtr, err := svc.CreateMaintenanceContractor(ctx, ownerActor, domain.MaintenanceContractor{
		PropertyID: propA.ID,
		Name:       "Active Schedule Contractor",
		IsActive:   true,
	})
	if err != nil {
		t.Fatalf("create activeCtr: %v", err)
	}

	dueYesterday := time.Now().UTC().AddDate(0, 0, -1)

	// Schedule A: both staff and contractor valid at creation; later BOTH are deactivated before sweep.
	schedBothCleared, err := svc.CreateMaintenanceSchedule(ctx, ownerActor, domain.MaintenanceSchedule{
		PropertyID:           propA.ID,
		Title:                "Cleared Both Assignees Check",
		CategoryID:           catsA[0].ID,
		Priority:             domain.MaintPriorityHigh,
		LocationID:           &locsA[0].ID,
		Frequency:            domain.MaintFreqOnce,
		NextDueDate:          dueYesterday,
		AssignedUserID:       &staff.ID,
		AssignedContractorID: &ctr.ID,
		IsActive:             true,
	})
	if err != nil {
		t.Fatalf("create schedBothCleared: %v", err)
	}
	// Schedule B: staff moved to Property B before sweep, active contractor stays valid.
	schedMovedStaff, err := svc.CreateMaintenanceSchedule(ctx, ownerActor, domain.MaintenanceSchedule{
		PropertyID:           propA.ID,
		Title:                "Moved Staff Keeps Active Contractor",
		CategoryID:           catsA[0].ID,
		Priority:             domain.MaintPriorityNormal,
		LocationID:           &locsA[0].ID,
		Frequency:            domain.MaintFreqOnce,
		NextDueDate:          dueYesterday,
		AssignedUserID:       &staffMoved.ID,
		AssignedContractorID: &activeCtr.ID,
		IsActive:             true,
	})
	if err != nil {
		t.Fatalf("create schedMovedStaff: %v", err)
	}

	// Deactivate staff user and contractor, and move staffMoved to propB before schedule sweep.
	if _, err := store.Pool().Exec(ctx, `UPDATE admin_users SET is_active = FALSE WHERE id = $1`, staff.ID); err != nil {
		t.Fatalf("deactivate staff: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE admin_users SET property_id = $2 WHERE id = $1`, staffMoved.ID, propB.ID); err != nil {
		t.Fatalf("move staffMoved: %v", err)
	}
	ctr.IsActive = false
	if _, err := svc.UpdateMaintenanceContractor(ctx, ownerActor, ctr); err != nil {
		t.Fatalf("deactivate contractor: %v", err)
	}

	// Sweep should succeed without failure, clearing invalid assignees cleanly.
	count, err := svc.SweepDueMaintenanceSchedules(ctx)
	if err != nil {
		t.Fatalf("sweep schedules: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 created requests, got %d", count)
	}
	reqs, err := svc.ListMaintenanceRequests(ctx, ownerActor, propA.ID, domain.MaintenanceRequestFilter{Status: "ALL"})
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	var gotBothCleared, gotMovedStaff *domain.MaintenanceRequest
	for i := range reqs {
		if reqs[i].ScheduleID != nil && *reqs[i].ScheduleID == schedBothCleared.ID {
			gotBothCleared = &reqs[i]
		}
		if reqs[i].ScheduleID != nil && *reqs[i].ScheduleID == schedMovedStaff.ID {
			gotMovedStaff = &reqs[i]
		}
	}
	if gotBothCleared == nil || gotMovedStaff == nil {
		t.Fatalf("expected both generated requests, got cleared=%v moved=%v", gotBothCleared != nil, gotMovedStaff != nil)
	}
	if gotBothCleared.AssignedUserID != nil || gotBothCleared.AssignedContractorID != nil {
		t.Fatalf("expected deactivated staff and contractor to be cleared, got user=%v ctr=%v", gotBothCleared.AssignedUserID, gotBothCleared.AssignedContractorID)
	}
	if gotBothCleared.Status != domain.MaintStatusReported {
		t.Fatalf("expected initial status REPORTED when both assignees cleared, got %s", gotBothCleared.Status)
	}
	if !strings.Contains(gotBothCleared.InternalNotes, "[Schedule Assignment Warning]") {
		t.Fatalf("expected schedule assignment warning in internal notes, got %q", gotBothCleared.InternalNotes)
	}
	if gotMovedStaff.AssignedUserID != nil || gotMovedStaff.AssignedContractorID == nil || *gotMovedStaff.AssignedContractorID != activeCtr.ID {
		t.Fatalf("expected moved staff cleared and active contractor preserved, got user=%v ctr=%v", gotMovedStaff.AssignedUserID, gotMovedStaff.AssignedContractorID)
	}
	if gotMovedStaff.Status != domain.MaintStatusScheduled {
		t.Fatalf("expected SCHEDULED when contractor remains valid, got %s", gotMovedStaff.Status)
	}
	if !strings.Contains(gotMovedStaff.InternalNotes, "[Schedule Assignment Warning]") {
		t.Fatalf("expected schedule assignment warning on moved staff request, got %q", gotMovedStaff.InternalNotes)
	}
}
