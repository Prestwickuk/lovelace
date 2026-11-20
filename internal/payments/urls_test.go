package payments

import "testing"

func TestValidateCheckoutReturnURLPortMatching(t *testing.T) {
	origins := []string{"https://book.example.com"}
	domains := []string{"stays.example.com"}

	if _, err := ValidateCheckoutReturnURL("https://stays.example.com/booking/confirmation?ref=HLD-1", origins, domains, true); err != nil {
		t.Fatalf("expected standard HTTPS port on approved domain to pass: %v", err)
	}
	if _, err := ValidateCheckoutReturnURL("https://stays.example.com:8443/booking/confirmation", origins, domains, true); err == nil {
		t.Fatal("expected non-standard port on bare approved domain to be rejected")
	}
	if _, err := ValidateCheckoutReturnURL("https://evil.stays.example.com/booking/confirmation", origins, domains, true); err == nil {
		t.Fatal("expected subdomain spoof to be rejected")
	}
}
