package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/httpx"
	"github.com/tirbach/tir-bach/internal/service"
)

func (h *Handler) registerPublicRoutes(mux *http.ServeMux) {
	// Strictly isolated public / customer booking API (/api/public/v1/*).
	// Does NOT read administrative sessions or expose administrative data.
	mux.HandleFunc("OPTIONS /api/public/v1/", h.handlePublicPreflight)

	mux.HandleFunc("GET /api/public/v1/properties/{slug}", h.publicEndpoint(false, true, h.handlePublicGetProperty))
	mux.HandleFunc("GET /api/public/v1/properties/{slug}/logo", h.publicEndpoint(false, true, h.handlePublicGetPropertyLogo))
	mux.HandleFunc("GET /api/public/v1/accommodation", h.publicEndpoint(false, true, h.handlePublicListAccommodation))
	mux.HandleFunc("GET /api/public/v1/accommodation/{code}", h.publicEndpoint(false, true, h.handlePublicGetAccommodationDetail))
	mux.HandleFunc("POST /api/public/v1/availability", h.publicEndpoint(false, true, h.handlePublicAvailabilitySearch))
	mux.HandleFunc("POST /api/public/v1/pricing", h.publicEndpoint(false, true, h.handlePublicPricingQuote))
	mux.HandleFunc("POST /api/public/v1/booking/holds", h.publicEndpoint(true, true, h.handlePublicCreateHold))
	mux.HandleFunc("POST /api/public/v1/booking/holds/{id}/cancel", h.publicEndpoint(true, true, h.handlePublicCancelHold))
	mux.HandleFunc("POST /api/public/v1/checkout", h.publicEndpoint(true, true, h.handlePublicInitiateCheckout))
	mux.HandleFunc("POST /api/public/v1/webhooks/stripe", h.publicEndpoint(false, false, h.handleStripeWebhook))
	mux.HandleFunc("GET /api/public/v1/booking-confirmation", h.publicEndpoint(true, true, h.handlePublicConfirmationGet))
	mux.HandleFunc("POST /api/public/v1/booking-confirmation/lookup", h.publicEndpoint(true, true, h.handlePublicConfirmationLookup))
	mux.HandleFunc("POST /api/public/v1/booking-confirmation/rotate-token", h.publicEndpoint(true, true, h.handlePublicRotateConfirmationToken))
	mux.HandleFunc("POST /api/public/v1/booking-confirmation/cancel-request", h.publicEndpoint(true, true, h.handlePublicCancelRequest))

	if h.cfg.StripeSandbox && !h.cfg.IsProduction() && !h.cfg.IsStaging() {
		mux.HandleFunc("POST /api/public/v1/dev/stripe-sandbox/complete", h.publicEndpoint(true, true, h.handleStripeSandboxComplete))
	}

	// Protected administrative policy & financial visibility routes (/api/v1/*).
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/booking-policy", h.requireAuth(false, h.handleAdminGetBookingPolicy))
	mux.HandleFunc("PUT /api/v1/properties/{propertyID}/booking-policy", h.requireAuth(true, h.handleAdminUpdateBookingPolicy))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/payment-exceptions", h.requireAuth(false, h.handleAdminListPaymentExceptions))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/payment-exceptions/{id}/resolve", h.requireAuth(true, h.handleAdminResolvePaymentException))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/reservations/{id}/financials", h.requireAuth(false, h.handleAdminGetReservationFinancials))
}

func (h *Handler) applyPublicCORS(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Origin")
	origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
	if origin == "" {
		return
	}
	allowed := h.allowedPublicOrigins()
	if allowed[origin] {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key, X-Lovelace-Property, X-Guest-CSRF")
		w.Header().Set("Access-Control-Max-Age", "300")
	}
}

func (h *Handler) allowedPublicOrigins() map[string]bool {
	out := make(map[string]bool)
	for _, p := range h.cfg.AllowedPublicOrigins() {
		out[p] = true
		if !h.cfg.IsProduction() {
			if strings.Contains(p, "127.0.0.1") {
				out[strings.Replace(p, "127.0.0.1", "localhost", 1)] = true
			} else if strings.Contains(p, "localhost") {
				out[strings.Replace(p, "localhost", "127.0.0.1", 1)] = true
			}
		}
	}
	return out
}

func (h *Handler) handlePublicPreflight(w http.ResponseWriter, r *http.Request) {
	h.applyPublicCORS(w, r)
	origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
	if origin != "" && !h.allowedPublicOrigins()[origin] {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "CORS origin is not permitted")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) publicEndpoint(strictLimit bool, checkPublicSecret bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.applyPublicCORS(w, r)

		if h.cfg.IsProduction() && checkPublicSecret && strings.TrimSpace(h.cfg.PublicAPISecret) == "" {
			writeError(w, http.StatusServiceUnavailable, "public_api_unconfigured", "public API client authentication is not configured")
			return
		}

		bffAuthenticated := false
		if checkPublicSecret && h.cfg.PublicAPISecret != "" {
			if !auth.ConstantTimeEqual(r.Header.Get("X-Lovelace-Public-Key"), h.cfg.PublicAPISecret) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "invalid public client credentials")
				return
			}
			bffAuthenticated = true
		}

		nowUTC := time.Now().UTC()
		if !h.publicGlobalLimiter.Allow("global", nowUTC) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "public service rate limit exceeded; please retry shortly")
			return
		}

		meta := httpx.MetaFromContext(r.Context())
		ipKey := httpx.RateLimitKeyForIP(httpx.ResolveForwardedCustomerIP(r, bffAuthenticated, meta.IPAddress))
		if ipKey == "" {
			ipKey = "unknown"
		}
		limiter := h.publicReadLimiter
		if strictLimit {
			limiter = h.publicWriteLimiter
		}
		if !limiter.Allow(ipKey, nowUTC) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests; please wait a moment and try again")
			return
		}

		next(w, r)
	}
}

func (h *Handler) publicActorMeta(r *http.Request) service.Actor {
	meta := httpx.MetaFromContext(r.Context())
	bffAuth := h.cfg.PublicAPISecret != "" && auth.ConstantTimeEqual(r.Header.Get("X-Lovelace-Public-Key"), h.cfg.PublicAPISecret)
	return service.Actor{
		Role:      "public_guest",
		RequestID: meta.RequestID,
		IPAddress: httpx.ResolveForwardedCustomerIP(r, bffAuth, meta.IPAddress),
		UserAgent: meta.UserAgent,
	}
}

func (h *Handler) handlePublicGetProperty(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	prop, err := h.svc.GetPublicProperty(r.Context(), slug)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"property": prop})
}

func (h *Handler) handlePublicListAccommodation(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.URL.Query().Get("property"))
	if slug == "" {
		slug = "tir-bach"
	}
	catalog, err := h.svc.ListPublicAccommodation(r.Context(), slug)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (h *Handler) handlePublicGetAccommodationDetail(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.URL.Query().Get("property"))
	if slug == "" {
		slug = "tir-bach"
	}
	code := strings.TrimSpace(r.PathValue("code"))
	detail, err := h.svc.GetPublicAccommodationDetail(r.Context(), slug, code)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) handlePublicAvailabilitySearch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PropertySlug  string `json:"property_slug"`
		ArrivalDate   string `json:"arrival_date"`
		DepartureDate string `json:"departure_date"`
		Adults        int    `json:"adults"`
		Children      int    `json:"children"`
		Infants       int    `json:"infants"`
		Category      string `json:"category"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.PropertySlug) == "" {
		body.PropertySlug = "tir-bach"
	}
	arr, err := domain.ParseDate(body.ArrivalDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"arrival_date": "must be YYYY-MM-DD"}))
		return
	}
	dep, err := domain.ParseDate(body.DepartureDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"departure_date": "must be YYYY-MM-DD"}))
		return
	}
	res, err := h.svc.SearchPublicAvailability(r.Context(), domain.AvailabilitySearchInput{
		PropertySlug: body.PropertySlug,
		ArrivalDate:  arr,
		Departure:    dep,
		Adults:       body.Adults,
		Children:     body.Children,
		Infants:      body.Infants,
		Category:     body.Category,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handlePublicPricingQuote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PropertySlug  string                       `json:"property_slug"`
		UnitTypeCode  string                       `json:"unit_type_code"`
		ArrivalDate   string                       `json:"arrival_date"`
		DepartureDate string                       `json:"departure_date"`
		Adults        int                          `json:"adults"`
		Children      int                          `json:"children"`
		Infants       int                          `json:"infants"`
		Extras        []domain.QuoteExtraSelection `json:"extras"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.PropertySlug) == "" {
		body.PropertySlug = "tir-bach"
	}
	arr, err := domain.ParseDate(body.ArrivalDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"arrival_date": "must be YYYY-MM-DD"}))
		return
	}
	dep, err := domain.ParseDate(body.DepartureDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"departure_date": "must be YYYY-MM-DD"}))
		return
	}
	quote, err := h.svc.ComputePublicPricing(r.Context(), body.PropertySlug, body.UnitTypeCode, arr, dep, body.Adults, body.Children, body.Infants, body.Extras)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"quote": quote})
}

func (h *Handler) handlePublicCreateHold(w http.ResponseWriter, r *http.Request) {
	applyConfirmationSecurityHeaders(w)
	var body struct {
		PropertySlug    string                       `json:"property_slug"`
		UnitTypeCode    string                       `json:"unit_type_code"`
		ArrivalDate     string                       `json:"arrival_date"`
		DepartureDate   string                       `json:"departure_date"`
		Adults          int                          `json:"adults"`
		Children        int                          `json:"children"`
		Infants         int                          `json:"infants"`
		Extras          []domain.QuoteExtraSelection `json:"extras"`
		GuestName       string                       `json:"guest_name"`
		GuestEmail      string                       `json:"guest_email"`
		GuestPhone      string                       `json:"guest_phone"`
		SpecialRequests string                       `json:"special_requests"`
		AcceptTerms     bool                         `json:"accept_terms"`
		SessionToken    string                       `json:"session_token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.PropertySlug) == "" {
		body.PropertySlug = "tir-bach"
	}
	arr, err := domain.ParseDate(body.ArrivalDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"arrival_date": "must be YYYY-MM-DD"}))
		return
	}
	dep, err := domain.ParseDate(body.DepartureDate)
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"departure_date": "must be YYYY-MM-DD"}))
		return
	}
	res, err := h.svc.CreatePublicHold(r.Context(), h.publicActorMeta(r), service.CreateHoldRequest{
		PropertySlug:    body.PropertySlug,
		UnitTypeCode:    body.UnitTypeCode,
		ArrivalDate:     arr,
		DepartureDate:   dep,
		Adults:          body.Adults,
		Children:        body.Children,
		Infants:         body.Infants,
		Extras:          body.Extras,
		GuestName:       body.GuestName,
		GuestEmail:      body.GuestEmail,
		GuestPhone:      body.GuestPhone,
		SpecialRequests: body.SpecialRequests,
		AcceptTerms:     body.AcceptTerms,
		SessionToken:    body.SessionToken,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (h *Handler) handlePublicCancelHold(w http.ResponseWriter, r *http.Request) {
	applyConfirmationSecurityHeaders(w)
	holdID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	hold, err := h.svc.CancelPublicHold(r.Context(), h.publicActorMeta(r), holdID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hold": hold})
}

func (h *Handler) handlePublicInitiateCheckout(w http.ResponseWriter, r *http.Request) {
	applyConfirmationSecurityHeaders(w)
	var body struct {
		HoldID         string `json:"hold_id"`
		IdempotencyKey string `json:"idempotency_key"`
		SuccessURL     string `json:"success_url"`
		CancelURL      string `json:"cancel_url"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.SuccessURL) != "" || strings.TrimSpace(body.CancelURL) != "" {
		fields := map[string]string{}
		if strings.TrimSpace(body.SuccessURL) != "" {
			fields["success_url"] = "client-supplied checkout redirect URLs are prohibited"
		}
		if strings.TrimSpace(body.CancelURL) != "" {
			fields["cancel_url"] = "client-supplied checkout redirect URLs are prohibited"
		}
		writeDomainError(w, domain.NewValidationError(fields))
		return
	}
	holdID, err := uuid.Parse(strings.TrimSpace(body.HoldID))
	if err != nil {
		writeDomainError(w, domain.NewValidationError(map[string]string{"hold_id": "valid hold_id UUID is required"}))
		return
	}
	idemKey := strings.TrimSpace(body.IdempotencyKey)
	if idemKey == "" {
		idemKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	resp, err := h.svc.InitiatePublicCheckout(r.Context(), h.publicActorMeta(r), service.CreateCheckoutRequest{
		HoldID:         holdID,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	const maxWebhookBytes = 1 << 20 // 1 MiB
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBytes)
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "failed to read webhook payload")
		return
	}
	sig := r.Header.Get("Stripe-Signature")
	res, err := h.svc.ProcessStripeWebhook(r.Context(), h.publicActorMeta(r), rawBody, sig)
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			writeError(w, http.StatusUnauthorized, "invalid_signature", "webhook signature verification failed")
			return
		}
		writeDomainError(w, err)
		return
	}
	out := map[string]any{"received": true, "duplicate": res.Duplicate}
	if res.Rejected {
		out["rejected"] = true
		if res.Reason != "" {
			out["reason"] = res.Reason
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func applyConfirmationSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
}

func extractBearerToken(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authHeader) > 7 && strings.EqualFold(authHeader[:7], "Bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	return ""
}

func (h *Handler) checkConfirmationRateLimit(w http.ResponseWriter, r *http.Request) bool {
	bffAuth := h.cfg.PublicAPISecret != "" && auth.ConstantTimeEqual(r.Header.Get("X-Lovelace-Public-Key"), h.cfg.PublicAPISecret)
	meta := httpx.MetaFromContext(r.Context())
	ipKey := httpx.RateLimitKeyForIP(httpx.ResolveForwardedCustomerIP(r, bffAuth, meta.IPAddress))
	if ipKey == "" {
		ipKey = "unknown"
	}
	if !h.publicLookupLimiter.Allow(ipKey, time.Now().UTC()) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many booking lookup requests; please try again shortly")
		return false
	}
	return true
}

func (h *Handler) handlePublicConfirmationGet(w http.ResponseWriter, r *http.Request) {
	applyConfirmationSecurityHeaders(w)
	if !h.checkConfirmationRateLimit(w, r) {
		return
	}
	if r.URL.Query().Get("token") != "" {
		writeError(w, http.StatusBadRequest, "query_token_forbidden", "confirmation access tokens must not be passed in URL query parameters; use Authorization: Bearer or POST /booking-confirmation/lookup")
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	token := extractBearerToken(r)
	if ref == "" || token == "" {
		writeError(w, http.StatusBadRequest, "missing_credentials", "booking reference and Authorization: Bearer confirmation token are required")
		return
	}
	view, err := h.svc.GetPublicConfirmation(r.Context(), ref, token)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handlePublicConfirmationLookup(w http.ResponseWriter, r *http.Request) {
	applyConfirmationSecurityHeaders(w)
	if !h.checkConfirmationRateLimit(w, r) {
		return
	}
	var body struct {
		BookingReference string `json:"booking_reference"`
		Token            string `json:"token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	token := extractBearerToken(r)
	if token == "" {
		token = strings.TrimSpace(body.Token)
	}
	ref := strings.TrimSpace(body.BookingReference)
	if ref == "" || token == "" {
		writeError(w, http.StatusBadRequest, "missing_credentials", "booking_reference and confirmation token are required")
		return
	}
	view, err := h.svc.GetPublicConfirmation(r.Context(), ref, token)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handlePublicRotateConfirmationToken(w http.ResponseWriter, r *http.Request) {
	applyConfirmationSecurityHeaders(w)
	if !h.checkConfirmationRateLimit(w, r) {
		return
	}
	var body struct {
		BookingReference string `json:"booking_reference"`
		Token            string `json:"token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	token := extractBearerToken(r)
	if token == "" {
		token = strings.TrimSpace(body.Token)
	}
	rotated, err := h.svc.RotateConfirmationToken(r.Context(), h.publicActorMeta(r), body.BookingReference, token)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"confirmation_token": rotated})
}

func (h *Handler) handlePublicCancelRequest(w http.ResponseWriter, r *http.Request) {
	applyConfirmationSecurityHeaders(w)
	if !h.checkConfirmationRateLimit(w, r) {
		return
	}
	var body struct {
		BookingReference string `json:"booking_reference"`
		Token            string `json:"token"`
		Note             string `json:"note"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	token := extractBearerToken(r)
	if token == "" {
		token = strings.TrimSpace(body.Token)
	}
	view, err := h.svc.RequestPublicCancellation(r.Context(), h.publicActorMeta(r), body.BookingReference, token, body.Note)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// --- Protected Administrative Financial & Policy API ---

func (h *Handler) handleAdminGetBookingPolicy(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	policy, err := h.svc.GetBookingPolicy(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"booking_policy": policy})
}

func (h *Handler) handleAdminUpdateBookingPolicy(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body domain.PropertyBookingPolicy
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PropertyID = propID
	updated, err := h.svc.UpdateBookingPolicy(r.Context(), actor, body)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"booking_policy": updated})
}

func (h *Handler) handleAdminListPaymentExceptions(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	items, err := h.svc.ListPaymentExceptions(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

func (h *Handler) handleAdminResolvePaymentException(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	exID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		ResolutionNotes string `json:"resolution_notes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	updated, err := h.svc.ResolvePaymentException(r.Context(), actor, propID, exID, body.ResolutionNotes)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payment_exception": updated})
}

func (h *Handler) handleAdminGetReservationFinancials(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	resID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	fin, err := h.svc.GetReservationFinancials(r.Context(), actor, propID, resID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fin)
}

func (h *Handler) handleStripeSandboxComplete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
		Outcome   string `json:"outcome"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := h.svc.CompleteSandboxPayment(r.Context(), h.publicActorMeta(r), body.SessionID, body.Outcome); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
