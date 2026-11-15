package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidationRules(t *testing.T) {
	prop := Property{
		Slug:          "tir-bach",
		Name:          "Tir Bach Campsite",
		CountryCode:   "GB",
		Timezone:      "Europe/London",
		Currency:      "GBP",
		CheckInTime:   "15:00",
		CheckOutTime:  "10:30",
		VATRegistered: true,
		VATNumber:     "",
	}
	if err := prop.Validate(); err == nil {
		t.Fatal("expected VAT registered without VAT number to fail validation")
	}
	prop.VATNumber = "GB123456789"
	if err := prop.Validate(); err != nil {
		t.Fatalf("expected valid property, got %v", err)
	}

	ut := UnitType{
		Code:                 "bell-tent",
		Name:                 "Bell Tent",
		Category:             CategoryGlamping,
		BaseCapacityAdults:   2,
		MaxAdults:            2,
		MaxGuests:            4,
		BaseNightlyRatePence: 9500,
		MinNights:            3,
		MaxNights:            2,
	}
	if err := ut.Validate(); err == nil {
		t.Fatal("expected max_nights < min_nights to fail validation")
	}
	ut.MaxNights = 14
	if err := ut.Validate(); err != nil {
		t.Fatalf("expected valid unit type, got %v", err)
	}

	blk := AvailabilityBlock{
		UnitID:    uuid.New(),
		StartDate: time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC),
		BlockType: BlockMaintenance,
	}
	if err := blk.Validate(); err == nil {
		t.Fatal("expected empty [start, end) range to fail validation")
	}
}
