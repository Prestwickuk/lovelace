package payments

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CheckoutRequest carries server-computed payment parameters to Stripe Checkout Sessions.
type CheckoutRequest struct {
	IdempotencyKey   string
	AmountPence      int64
	Currency         string
	Description      string
	CustomerEmail    string
	SuccessURL       string
	CancelURL        string
	PropertyID       string
	HoldID           string
	HoldReference    string
	PaymentAttemptID string
}

// CheckoutSession represents the created provider payment session.
type CheckoutSession struct {
	SessionID       string
	PaymentIntentID string
	CheckoutURL     string
	Status          string
}

// RefundRequest requests an idempotent provider refund.
type RefundRequest struct {
	IdempotencyKey  string
	PaymentIntentID string
	AmountPence     int64
	Reason          string
}

// RefundResult captures the provider refund outcome.
type RefundResult struct {
	RefundID string
	Status   string
}

// Provider abstracts payment gateway execution so live Stripe REST API and test gateways share one contract.
type Provider interface {
	CreateCheckoutSession(req CheckoutRequest) (CheckoutSession, error)
	Refund(req RefundRequest) (RefundResult, error)
}

// StripeClient communicates directly with Stripe's HTTP REST API v1 over TLS using server-side secret keys.
type StripeClient struct {
	SecretKey  string
	APIBaseURL string
	HTTPClient *http.Client
}

func NewStripeClient(secretKey, apiBaseURL string) *StripeClient {
	if apiBaseURL == "" {
		apiBaseURL = "https://api.stripe.com"
	}
	return &StripeClient{
		SecretKey:  strings.TrimSpace(secretKey),
		APIBaseURL: strings.TrimRight(apiBaseURL, "/"),
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *StripeClient) CreateCheckoutSession(req CheckoutRequest) (CheckoutSession, error) {
	if c.SecretKey == "" {
		return CheckoutSession{}, errors.New("stripe secret key is not configured")
	}

	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("success_url", req.SuccessURL)
	form.Set("cancel_url", req.CancelURL)
	form.Set("client_reference_id", req.HoldReference)
	if req.CustomerEmail != "" {
		form.Set("customer_email", req.CustomerEmail)
	}
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", strings.ToLower(req.Currency))
	form.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(req.AmountPence, 10))
	form.Set("line_items[0][price_data][product_data][name]", req.Description)
	form.Set("metadata[property_id]", req.PropertyID)
	form.Set("metadata[hold_id]", req.HoldID)
	form.Set("metadata[hold_reference]", req.HoldReference)
	form.Set("metadata[payment_attempt_id]", req.PaymentAttemptID)
	form.Set("payment_intent_data[metadata][property_id]", req.PropertyID)
	form.Set("payment_intent_data[metadata][hold_id]", req.HoldID)
	form.Set("payment_intent_data[metadata][payment_attempt_id]", req.PaymentAttemptID)

	httpReq, err := http.NewRequest(http.MethodPost, c.APIBaseURL+"/v1/checkout/sessions", strings.NewReader(form.Encode()))
	if err != nil {
		return CheckoutSession{}, err
	}
	httpReq.SetBasicAuth(c.SecretKey, "")
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if req.IdempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return CheckoutSession{}, fmt.Errorf("stripe checkout session request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return CheckoutSession{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CheckoutSession{}, fmt.Errorf("stripe API error (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		ID            string `json:"id"`
		URL           string `json:"url"`
		Status        string `json:"status"`
		PaymentIntent string `json:"payment_intent"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return CheckoutSession{}, fmt.Errorf("decode stripe session response: %w", err)
	}
	return CheckoutSession{
		SessionID:       parsed.ID,
		PaymentIntentID: parsed.PaymentIntent,
		CheckoutURL:     parsed.URL,
		Status:          parsed.Status,
	}, nil
}

func (c *StripeClient) Refund(req RefundRequest) (RefundResult, error) {
	if c.SecretKey == "" {
		return RefundResult{}, errors.New("stripe secret key is not configured")
	}
	form := url.Values{}
	form.Set("payment_intent", req.PaymentIntentID)
	if req.AmountPence > 0 {
		form.Set("amount", strconv.FormatInt(req.AmountPence, 10))
	}
	if req.Reason != "" {
		form.Set("metadata[reason]", req.Reason)
	}

	httpReq, err := http.NewRequest(http.MethodPost, c.APIBaseURL+"/v1/refunds", strings.NewReader(form.Encode()))
	if err != nil {
		return RefundResult{}, err
	}
	httpReq.SetBasicAuth(c.SecretKey, "")
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if req.IdempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return RefundResult{}, fmt.Errorf("stripe refund request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return RefundResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RefundResult{}, fmt.Errorf("stripe refund error (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return RefundResult{}, err
	}
	return RefundResult{RefundID: parsed.ID, Status: parsed.Status}, nil
}

// VerifyStripeSignature validates the official Stripe-Signature header (`t=<ts>,v1=<hex_hmac>`)
// against the raw webhook request body within tolerance using constant-time HMAC comparison.
func VerifyStripeSignature(payload []byte, header, secret string, tolerance time.Duration, now time.Time) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("stripe webhook secret is not configured")
	}
	if strings.TrimSpace(header) == "" {
		return errors.New("missing Stripe-Signature header")
	}

	var tsStr string
	var sigs [][]byte
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			tsStr = v
		case "v1":
			b, err := hex.DecodeString(v)
			if err == nil {
				sigs = append(sigs, b)
			}
		}
	}
	if tsStr == "" || len(sigs) == 0 {
		return errors.New("invalid Stripe-Signature header format")
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return errors.New("invalid Stripe-Signature timestamp")
	}
	signedAt := time.Unix(ts, 0)
	if tolerance > 0 && now.Sub(signedAt).Abs() > tolerance {
		return errors.New("Stripe-Signature timestamp outside tolerance window")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tsStr))
	mac.Write([]byte("."))
	mac.Write(payload)
	expected := mac.Sum(nil)

	for _, s := range sigs {
		if hmac.Equal(s, expected) {
			return nil
		}
	}
	return errors.New("Stripe-Signature verification failed")
}

// ComputeStripeSignature builds a valid Stripe-Signature header (used for automated tests and local test helpers).
func ComputeStripeSignature(payload []byte, secret string, ts time.Time) string {
	tsStr := strconv.FormatInt(ts.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tsStr))
	mac.Write([]byte("."))
	mac.Write(payload)
	return fmt.Sprintf("t=%s,v1=%s", tsStr, hex.EncodeToString(mac.Sum(nil)))
}
