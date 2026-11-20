package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/domain"
)

func TestNormalizationAndValidationHelpers(t *testing.T) {
	if got := domain.NormalizeEmail("  Sian.Davies@Example.COM "); got != "sian.davies@example.com" {
		t.Fatalf("NormalizeEmail mismatch: %q", got)
	}
	if got := domain.NormalizePhone(" +44 (0) 7700-900 123 "); got != "+4407700900123" {
		t.Fatalf("NormalizePhone mismatch: %q", got)
	}
	if got := domain.NormalizePhone("0044 1234 567890"); got != "+441234567890" {
		t.Fatalf("NormalizePhone 00 prefix mismatch: %q", got)
	}

	if _, err := domain.SanitizeEmailHeaderValue("Hello\r\nBcc: evil@example.com"); err == nil {
		t.Fatal("expected CRLF header injection to be rejected")
	}
	if _, err := domain.ValidateEmailRecipient("guest@example.com\nCc: attacker@example.com"); err == nil {
		t.Fatal("expected recipient newline injection to be rejected")
	}
	addr, err := domain.ValidateEmailRecipient("Sian Davies <Sian.Davies@Example.org>")
	if err != nil || addr != "sian.davies@example.org" {
		t.Fatalf("expected normalised email address, got %q err=%v", addr, err)
	}
}

func TestTemplatePlaceholdersAndSafeRendering(t *testing.T) {
	if err := domain.ValidateTemplatePlaceholders(
		"Arrival at {{property_name}}",
		"Hello {{guest_first_name}}, ref {{reservation_reference}}",
		"<p>{{unit_names}}</p>",
	); err != nil {
		t.Fatalf("expected valid placeholders to pass, got %v", err)
	}
	if err := domain.ValidateTemplatePlaceholders(
		"Subject {{internal_staff_notes}}",
		"Body",
		"",
	); err == nil {
		t.Fatal("expected unsupported/internal placeholder to be rejected")
	}

	subj, textOut, htmlOut, err := domain.RenderTemplate(
		"Welcome {{guest_first_name}}!",
		"Hi {{guest_full_name}}, stay on {{arrival_date}}.",
		"<p>Hi {{guest_full_name}}</p>",
		map[string]string{
			"guest_first_name": "Sian",
			"guest_full_name":  `Sian <script>alert("xss")</script>`,
			"arrival_date":     "2026-07-10",
		},
	)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}
	if subj != "Welcome Sian!" || !strings.Contains(textOut, "2026-07-10") {
		t.Fatalf("unexpected rendered text outputs: subj=%q text=%q", subj, textOut)
	}
	if strings.Contains(htmlOut, "<script>") || !strings.Contains(htmlOut, "&lt;script&gt;") {
		t.Fatalf("expected HTML escaping on template variables, got %q", htmlOut)
	}
}

func TestComputeRuleTriggerUTCRespectsPropertyTimezoneBSTAndGMT(t *testing.T) {
	prop := domain.Property{
		Timezone:     "Europe/London",
		CheckInTime:  "15:00",
		CheckOutTime: "10:00",
	}
	hourNine := 9
	rule := domain.CommunicationAutomationRule{
		TriggerEvent:  domain.TriggerPreArrival,
		OffsetHours:   -72, // 3 days before arrival
		SendHourLocal: &hourNine,
	}

	// July is BST (UTC+1): 09:00 Europe/London == 08:00 UTC
	summerRes := domain.Reservation{
		ArrivalDate:   time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC),
		DepartureDate: time.Date(2026, time.July, 23, 0, 0, 0, 0, time.UTC),
	}
	summerUTC := domain.ComputeRuleTriggerUTC(rule, prop, summerRes, time.Now().UTC())
	wantSummer := time.Date(2026, time.July, 17, 8, 0, 0, 0, time.UTC)
	if !summerUTC.Equal(wantSummer) {
		t.Fatalf("summer BST schedule mismatch: got %s, want %s", summerUTC, wantSummer)
	}

	// December is GMT (UTC+0): 09:00 Europe/London == 09:00 UTC
	winterRes := domain.Reservation{
		ArrivalDate:   time.Date(2026, time.December, 20, 0, 0, 0, 0, time.UTC),
		DepartureDate: time.Date(2026, time.December, 22, 0, 0, 0, 0, time.UTC),
	}
	winterUTC := domain.ComputeRuleTriggerUTC(rule, prop, winterRes, time.Now().UTC())
	wantWinter := time.Date(2026, time.December, 17, 9, 0, 0, 0, time.UTC)
	if !winterUTC.Equal(wantWinter) {
		t.Fatalf("winter GMT schedule mismatch: got %s, want %s", winterUTC, wantWinter)
	}
}

func TestDuplicateCandidateScoring(t *testing.T) {
	idA, idB, idC := uuid.New(), uuid.New(), uuid.New()
	gA := domain.GuestListItem{Guest: domain.Guest{ID: idA, FullName: "Rhys Thomas", FirstName: "Rhys", LastName: "Thomas", Email: "rhys@example.com", Phone: "07700 900111", Status: domain.GuestStatusActive}}
	gB := domain.GuestListItem{Guest: domain.Guest{ID: idB, FullName: "R. Thomas", FirstName: "R.", LastName: "Thomas", Email: "RHYS@example.com", Phone: "+44 7700 900222", Status: domain.GuestStatusActive}}
	gC := domain.GuestListItem{Guest: domain.Guest{ID: idC, FullName: "Merged Guest", Email: "rhys@example.com", Status: domain.GuestStatusMerged}}

	scoreAB, reasonsAB := domain.ScoreDuplicatePair(gA, gB)
	if scoreAB < 70 || len(reasonsAB) == 0 {
		t.Fatalf("expected strong email duplicate score, got %d (%v)", scoreAB, reasonsAB)
	}
	if scoreAC, _ := domain.ScoreDuplicatePair(gA, gC); scoreAC != 0 {
		t.Fatalf("merged guests must not be flagged as duplicate candidates, got %d", scoreAC)
	}
}

func TestValidateOperationalGuestStatusAndConsentState(t *testing.T) {
	for _, valid := range []domain.GuestStatus{
		domain.GuestStatusActive,
		domain.GuestStatusVIP,
		domain.GuestStatusDoNotContact,
	} {
		if err := domain.ValidateOperationalGuestStatus(valid); err != nil {
			t.Fatalf("expected operational status %s to pass validation, got %v", valid, err)
		}
	}
	for _, protected := range []domain.GuestStatus{
		domain.GuestStatusMerged,
		domain.GuestStatusAnonymized,
		domain.GuestStatus("BOGUS"),
		domain.GuestStatus(""),
	} {
		if err := domain.ValidateOperationalGuestStatus(protected); err == nil {
			t.Fatalf("expected status %q to be rejected on generic create/update paths", protected)
		}
	}
	if domain.ConsentNotSubscribed != "NOT_SUBSCRIBED" {
		t.Fatalf("unexpected ConsentNotSubscribed constant: %q", domain.ConsentNotSubscribed)
	}
}
