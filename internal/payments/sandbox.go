package payments

import (
	"fmt"
	"net/url"
	"strings"
)

// SandboxProvider implements local non-production Stripe Checkout simulation
// that routes the guest to an explicit sandbox card approval page and still
// triggers signed HMAC-SHA256 webhooks through the real webhook pipeline.
type SandboxProvider struct {
	PublicOrigin string
}

func NewSandboxProvider(publicOrigin string) *SandboxProvider {
	return &SandboxProvider{PublicOrigin: strings.TrimRight(publicOrigin, "/")}
}

func (p *SandboxProvider) CreateCheckoutSession(req CheckoutRequest) (CheckoutSession, error) {
	sid := "cs_sandbox_" + strings.ReplaceAll(req.PaymentAttemptID, "-", "")[:16]
	piID := "pi_sandbox_" + strings.ReplaceAll(req.PaymentAttemptID, "-", "")[:16]

	// Only approved non-sensitive display metadata is placed in the local sandbox query string;
	// post-payment destination routes are derived strictly on the frontend/backend from hold_ref.
	q := url.Values{}
	q.Set("session_id", sid)
	q.Set("payment_attempt_id", req.PaymentAttemptID)
	q.Set("hold_ref", req.HoldReference)
	q.Set("amount_pence", fmt.Sprintf("%d", req.AmountPence))
	q.Set("currency", req.Currency)
	q.Set("description", req.Description)

	return CheckoutSession{
		SessionID:       sid,
		PaymentIntentID: piID,
		CheckoutURL:     p.PublicOrigin + "/checkout/sandbox?" + q.Encode(),
		Status:          "open",
	}, nil
}

func (p *SandboxProvider) Refund(req RefundRequest) (RefundResult, error) {
	return RefundResult{
		RefundID: "re_sandbox_" + strings.ReplaceAll(req.IdempotencyKey, "-", ""),
		Status:   "succeeded",
	}, nil
}
