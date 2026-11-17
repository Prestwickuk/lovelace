package payments

import "testing"

func TestValidateCheckoutReturnURLPortMatching(t *testing.T) {
	origins := []string{"https://book.tirbach.wales"}
	domains := []string{"stays.tirbach.wales"}

	if _, err := ValidateCheckoutReturnURL("https://stays.tirbach.wales/booking/confirmation?ref=HLD-1", origins, domains, true); err != nil {
		t.Fatalf("expected standard HTTPS port on approved domain to pass: %v", err)
	}
	if _, err := ValidateCheckoutReturnURL("https://stays.tirbach.wales:8443/booking/confirmation", origins, domains, true); err == nil {
		t.Fatal("expected non-standard port on bare approved domain to be rejected")
	}
	if _, err := ValidateCheckoutReturnURL("https://evil.stays.tirbach.wales/booking/confirmation", origins, domains, true); err == nil {
		t.Fatal("expected subdomain spoof to be rejected")
	}
}
