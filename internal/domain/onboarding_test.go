package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDeriveSlugAndUnitPrefix(t *testing.T) {
	if got := DeriveSlug("Sunny Meadow Glamping & Camping!", "my-site"); got != "sunny-meadow-glamping-camping" {
		t.Fatalf("DeriveSlug got %q", got)
	}
	if got := DeriveSlug("12345678", "my-site"); got != "site-12345678" {
		t.Fatalf("DeriveSlug pure digits got %q", got)
	}
	if got := DeriveSlug("   ", "fallback-site"); got != "fallback-site" {
		t.Fatalf("DeriveSlug empty got %q", got)
	}
	if got := DeriveUnitPrefix("Meadow Grass Pitch"); got != "MGP" {
		t.Fatalf("DeriveUnitPrefix got %q, want MGP", got)
	}
	if got := DeriveUnitPrefix("Cabin"); got != "CAB" {
		t.Fatalf("DeriveUnitPrefix single word got %q, want CAB", got)
	}
}

func TestParseCurrencyMajorToMinor(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"", 0, true},
		{"0", 0, true},
		{"95", 9500, true},
		{"95.00", 9500, true},
		{"£14.50", 1450, true},
		{"1,250.5", 125050, true},
		{"-5.00", 0, false},
		{"12.345", 0, false},
		{"abc", 0, false},
	}
	for _, tc := range cases {
		got, err := ParseCurrencyMajorToMinor(tc.in)
		if tc.ok && err != nil {
			t.Errorf("ParseCurrencyMajorToMinor(%q) unexpected err: %v", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("ParseCurrencyMajorToMinor(%q) expected error, got %d", tc.in, got)
		}
		if tc.ok && got != tc.want {
			t.Errorf("ParseCurrencyMajorToMinor(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if got := FormatMinorAsDecimal(1450); got != "14.50" {
		t.Fatalf("FormatMinorAsDecimal(1450) = %q, want 14.50", got)
	}
}

func TestMergeAndDecodeOnboardingStatePreservesSiblingSettings(t *testing.T) {
	existing := json.RawMessage(`{"custom_flag":true,"nested":{"k":"v"}}`)
	now := time.Date(2026, 11, 19, 14, 0, 0, 0, time.UTC)
	merged, err := MergeOnboardingState(existing, OnboardingState{
		Preset:      OnboardingPresetAirbnb,
		CurrentStep: 4,
		StartedAt:   &now,
		UpdatedAt:   &now,
	})
	if err != nil {
		t.Fatalf("MergeOnboardingState error: %v", err)
	}
	st, ok := DecodeOnboardingState(merged)
	if !ok {
		t.Fatalf("DecodeOnboardingState returned false")
	}
	if st.Preset != OnboardingPresetAirbnb || st.CurrentStep != 4 {
		t.Fatalf("unexpected decoded state: %+v", st)
	}
	var rawMap map[string]any
	if err := json.Unmarshal(merged, &rawMap); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	if rawMap["custom_flag"] != true {
		t.Fatalf("sibling setting custom_flag lost: %v", rawMap)
	}
}
