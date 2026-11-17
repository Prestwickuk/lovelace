package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/httpx"
	"github.com/tirbach/tir-bach/internal/service"
)

func (h *Handler) registerPublicPortalRoutes(mux *http.ServeMux) {
	// Strictly isolated Self-Service Guest Portal API under /api/public/v1/*.
	// Never reads admin_sessions / tirbach_session and never exposes internal CRM notes or staff identities.
	register := func(prefix string) {
		mux.HandleFunc("POST "+prefix+"/auth/challenge", h.publicEndpoint(true, true, h.handlePortalAuthChallenge))
		mux.HandleFunc("POST "+prefix+"/auth/verify", h.publicEndpoint(true, true, h.handlePortalAuthVerify))
		mux.HandleFunc("POST "+prefix+"/auth/redeem", h.publicEndpoint(true, true, h.handlePortalAuthVerify))
		mux.HandleFunc("POST "+prefix+"/auth/booking-token", h.publicEndpoint(true, true, h.handlePortalAuthBookingToken))
		mux.HandleFunc("GET "+prefix+"/auth/session", h.publicEndpoint(false, true, h.handlePortalGetSession))
		mux.HandleFunc("GET "+prefix+"/session", h.publicEndpoint(false, true, h.handlePortalGetSession))
		mux.HandleFunc("POST "+prefix+"/auth/logout", h.publicEndpoint(true, true, h.handlePortalLogout))
		mux.HandleFunc("POST "+prefix+"/logout", h.publicEndpoint(true, true, h.handlePortalLogout))
		mux.HandleFunc("POST "+prefix+"/auth/step-up/challenge", h.publicEndpoint(true, true, h.handlePortalStepUpChallenge))
		mux.HandleFunc("POST "+prefix+"/auth/step-up/verify", h.publicEndpoint(true, true, h.handlePortalStepUpVerify))

		mux.HandleFunc("GET "+prefix+"/bookings", h.publicEndpoint(false, true, h.handlePortalListBookings))
		mux.HandleFunc("GET "+prefix+"/bookings/{reference}", h.publicEndpoint(false, true, h.handlePortalGetBooking))
		mux.HandleFunc("PATCH "+prefix+"/bookings/{reference}/arrival", h.publicEndpoint(true, true, h.handlePortalUpdateArrival))
		mux.HandleFunc("POST "+prefix+"/bookings/{reference}/change-request", h.publicEndpoint(true, true, h.handlePortalChangeRequest))
		mux.HandleFunc("POST "+prefix+"/bookings/{reference}/cancel-request", h.publicEndpoint(true, true, h.handlePortalCancelRequest))

		mux.HandleFunc("GET "+prefix+"/profile", h.publicEndpoint(false, true, h.handlePortalGetProfile))
		mux.HandleFunc("PATCH "+prefix+"/profile", h.publicEndpoint(true, true, h.handlePortalUpdateProfile))
		mux.HandleFunc("POST "+prefix+"/profile/email-change", h.publicEndpoint(true, true, h.handlePortalInitiateEmailChange))
		mux.HandleFunc("POST "+prefix+"/profile/email-change/confirm", h.publicEndpoint(true, true, h.handlePortalConfirmEmailChange))
		mux.HandleFunc("POST "+prefix+"/consent", h.publicEndpoint(true, true, h.handlePortalUpdateConsent))

		mux.HandleFunc("GET "+prefix+"/enquiries", h.publicEndpoint(false, true, h.handlePortalListEnquiries))
		mux.HandleFunc("POST "+prefix+"/enquiries", h.publicEndpoint(true, true, h.handlePortalCreateEnquiry))
		mux.HandleFunc("POST "+prefix+"/enquiries/{id}/messages", h.publicEndpoint(true, true, h.handlePortalReplyEnquiry))

		mux.HandleFunc("POST "+prefix+"/privacy/sar", h.publicEndpoint(true, true, h.handlePortalRequestSAR))
		mux.HandleFunc("POST "+prefix+"/privacy/sar-request", h.publicEndpoint(true, true, h.handlePortalRequestSAR))
		mux.HandleFunc("POST "+prefix+"/privacy/erasure", h.publicEndpoint(true, true, h.handlePortalRequestErasure))
		mux.HandleFunc("POST "+prefix+"/privacy/erasure-request", h.publicEndpoint(true, true, h.handlePortalRequestErasure))
	}

	register("/api/public/v1/properties/{slug}/portal")
	register("/api/public/v1/portal")
}

func applyPortalSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
}

func decodePortalOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "malformed or invalid JSON request body")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain a single JSON object")
		return false
	}
	return true
}

func rejectPortalQuerySecrets(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	for _, forbidden := range []string{"token", "session", "session_token", "otp", "code", "magic_token", "csrf", "csrf_token"} {
		if q.Get(forbidden) != "" {
			writeError(w, http.StatusBadRequest, "query_token_forbidden", "portal credentials and verification tokens must never be passed in URL query parameters")
			return false
		}
	}
	return true
}

func parseStrictPortalOriginHeader(raw string) (string, bool) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", false
	}
	if strings.EqualFold(clean, "null") {
		return "", true
	}
	u, err := url.Parse(clean)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", true
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), true
}

func extractPortalRefererOrigin(raw string) (string, bool) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", false
	}
	u, err := url.Parse(clean)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return "", true
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), true
}

func (h *Handler) checkPortalMutationOrigin(w http.ResponseWriter, r *http.Request) bool {
	allowedMap := make(map[string]bool)
	allowedHosts := make(map[string]bool)
	for k := range h.allowedPublicOrigins() {
		norm, _ := parseStrictPortalOriginHeader(k)
		if norm != "" {
			allowedMap[norm] = true
			if u, err := url.Parse(norm); err == nil && u.Host != "" {
				allowedHosts[strings.ToLower(u.Host)] = true
			}
		}
	}
	bffAuth := h.cfg.PublicAPISecret != "" && auth.ConstantTimeEqual(r.Header.Get("X-Lovelace-Public-Key"), h.cfg.PublicAPISecret)
	if h.cfg.TrustProxy || bffAuth {
		if xfh := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])); xfh != "" {
			if len(allowedHosts) > 0 && !allowedHosts[xfh] {
				slog.Warn("portal.origin.rejected")
				writeError(w, http.StatusForbidden, "origin_not_allowed", "cross-site portal mutation rejected")
				return false
			}
		}
	}
	if origin, present := parseStrictPortalOriginHeader(r.Header.Get("Origin")); present {
		if origin == "" || !allowedMap[origin] {
			slog.Warn("portal.origin.rejected")
			writeError(w, http.StatusForbidden, "origin_not_allowed", "cross-site portal mutation rejected")
			return false
		}
		return true
	}
	if refOrigin, present := extractPortalRefererOrigin(r.Header.Get("Referer")); present {
		if refOrigin == "" || !allowedMap[refOrigin] {
			slog.Warn("portal.origin.rejected")
			writeError(w, http.StatusForbidden, "origin_not_allowed", "cross-site portal mutation rejected")
			return false
		}
		return true
	}
	return true
}

func extractPortalSessionAndCSRF(r *http.Request, bodySlug string) (sessionToken, csrfToken, propertySlug string) {
	sessionToken = strings.TrimSpace(r.Header.Get("X-Guest-Session"))
	csrfToken = strings.TrimSpace(r.Header.Get("X-Guest-CSRF"))
	if csrfToken == "" {
		csrfToken = strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
	}
	propertySlug = strings.TrimSpace(r.PathValue("slug"))
	if propertySlug == "" {
		propertySlug = strings.TrimSpace(bodySlug)
	}
	if propertySlug == "" {
		propertySlug = strings.TrimSpace(r.URL.Query().Get("property"))
	}
	if propertySlug == "" {
		propertySlug = strings.TrimSpace(r.Header.Get("X-Lovelace-Property"))
	}
	return sessionToken, csrfToken, propertySlug
}

func hashLimiterDimension(prefix, raw string) string {
	clean := strings.ToLower(strings.TrimSpace(raw))
	if clean == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("lovelace-portal-rl:" + prefix + ":" + clean))
	return prefix + ":" + hex.EncodeToString(sum[:16])
}

func (h *Handler) checkPortalAuthRateLimit(w http.ResponseWriter, r *http.Request, email, bookingRef string) bool {
	bffAuth := h.cfg.PublicAPISecret != "" && auth.ConstantTimeEqual(r.Header.Get("X-Lovelace-Public-Key"), h.cfg.PublicAPISecret)
	meta := httpx.MetaFromContext(r.Context())
	ipKey := httpx.RateLimitKeyForIP(httpx.ResolveForwardedCustomerIP(r, bffAuth, meta.IPAddress))
	if ipKey == "" {
		ipKey = "unknown"
	}
	nowUTC := time.Now().UTC()
	if !h.portalAuthLimiter.Allow("ip:"+ipKey, nowUTC) {
		slog.Warn("portal.auth.rate_limited", "dimension", "ip")
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many verification attempts; please wait a moment and try again")
		return false
	}
	if emKey := hashLimiterDimension("em", domain.NormalizeEmail(email)); emKey != "" {
		if !h.portalAuthLimiter.Allow(emKey, nowUTC) {
			slog.Warn("portal.auth.rate_limited", "dimension", "email")
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many verification attempts; please wait a moment and try again")
			return false
		}
	}
	if refKey := hashLimiterDimension("ref", bookingRef); refKey != "" {
		if !h.portalAuthLimiter.Allow(refKey, nowUTC) {
			slog.Warn("portal.auth.rate_limited", "dimension", "reference")
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many verification attempts; please wait a moment and try again")
			return false
		}
	}
	return true
}

func (h *Handler) checkPortalSessionRateLimit(w http.ResponseWriter, sessTok string, limiter *httpx.PublicRateLimiter, retryAfter string) bool {
	sessKey := hashLimiterDimension("sess", sessTok)
	if sessKey == "" {
		return true
	}
	if !limiter.Allow(sessKey, time.Now().UTC()) {
		slog.Warn("portal.session.rate_limited")
		w.Header().Set("Retry-After", retryAfter)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded; please wait before submitting again")
		return false
	}
	return true
}

func (h *Handler) handlePortalAuthChallenge(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug     string `json:"property_slug"`
		Email            string `json:"email"`
		Purpose          string `json:"purpose"`
		BookingReference string `json:"booking_reference"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !h.checkPortalAuthRateLimit(w, r, body.Email, body.BookingReference) {
		return
	}
	_, _, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	res, err := h.svc.RequestPortalAuthChallenge(r.Context(), h.publicActorMeta(r), service.RequestPortalChallengeInput{
		PropertySlug:     slug,
		Email:            body.Email,
		Purpose:          domain.PortalChallengePurpose(strings.ToUpper(strings.TrimSpace(body.Purpose))),
		BookingReference: body.BookingReference,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}

func (h *Handler) handlePortalAuthVerify(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug     string `json:"property_slug"`
		Purpose          string `json:"purpose"`
		Email            string `json:"email"`
		Code             string `json:"code"`
		MagicToken       string `json:"magic_token"`
		BrowserNonce     string `json:"browser_nonce"`
		BookingReference string `json:"booking_reference"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !h.checkPortalAuthRateLimit(w, r, body.Email, body.BookingReference) {
		return
	}
	if body.Purpose != "" && !strings.EqualFold(strings.TrimSpace(body.Purpose), string(domain.PortalChallengeLogin)) {
		writeDomainError(w, domain.ErrUnauthorized)
		return
	}
	_, _, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	res, err := h.svc.RedeemPortalChallenge(r.Context(), h.publicActorMeta(r), service.RedeemPortalChallengeInput{
		PropertySlug:     slug,
		Email:            body.Email,
		OTPCode:          body.Code,
		MagicToken:       body.MagicToken,
		BrowserNonce:     body.BrowserNonce,
		BookingReference: body.BookingReference,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handlePortalAuthBookingToken(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug      string `json:"property_slug"`
		BookingReference  string `json:"booking_reference"`
		ConfirmationToken string `json:"confirmation_token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !h.checkPortalAuthRateLimit(w, r, "", body.BookingReference) {
		return
	}
	_, _, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	tok := body.ConfirmationToken
	if strings.TrimSpace(tok) == "" {
		tok = extractBearerToken(r)
	}
	res, err := h.svc.CreatePortalBookingTokenSession(r.Context(), h.publicActorMeta(r), slug, body.BookingReference, tok)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handlePortalGetSession(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) {
		return
	}
	sessTok, _, slug := extractPortalSessionAndCSRF(r, "")
	view, err := h.svc.GetPortalSessionView(r.Context(), slug, sessTok)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": view})
}

func (h *Handler) handlePortalLogout(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug string `json:"property_slug"`
	}
	if !decodePortalOptionalJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if err := h.svc.LogoutPortalSession(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"logged_out": true})
}

func (h *Handler) handlePortalStepUpChallenge(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug string `json:"property_slug"`
	}
	if !decodePortalOptionalJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalAuthRateLimit(w, r, "", "") || !h.checkPortalSessionRateLimit(w, sessTok, h.portalMutationLimiter, "60") {
		return
	}
	if err := h.svc.RequestPortalStepUpChallenge(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (h *Handler) handlePortalStepUpVerify(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug string `json:"property_slug"`
		Code         string `json:"code"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalAuthRateLimit(w, r, "", "") || !h.checkPortalSessionRateLimit(w, sessTok, h.portalMutationLimiter, "60") {
		return
	}
	view, err := h.svc.VerifyPortalStepUpChallenge(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok, body.Code)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": view})
}

func (h *Handler) handlePortalListBookings(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) {
		return
	}
	sessTok, _, slug := extractPortalSessionAndCSRF(r, "")
	bookings, err := h.svc.ListPortalBookings(r.Context(), slug, sessTok)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bookings": nonNil(bookings)})
}

func (h *Handler) handlePortalGetBooking(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) {
		return
	}
	sessTok, _, slug := extractPortalSessionAndCSRF(r, "")
	ref := strings.TrimSpace(r.PathValue("reference"))
	detail, err := h.svc.GetPortalBookingDetail(r.Context(), slug, sessTok, ref)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"booking": detail})
}

func (h *Handler) handlePortalUpdateArrival(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug         string `json:"property_slug"`
		EstimatedArrivalTime string `json:"estimated_arrival_time"`
		GuestArrivalNotes    string `json:"guest_arrival_notes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalSessionRateLimit(w, sessTok, h.portalMutationLimiter, "60") {
		return
	}
	ref := strings.TrimSpace(r.PathValue("reference"))
	detail, err := h.svc.UpdatePortalArrivalDetails(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok, ref, body.EstimatedArrivalTime, body.GuestArrivalNotes)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"booking": detail})
}

func (h *Handler) handlePortalChangeRequest(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug       string `json:"property_slug"`
		RequestedArrival   string `json:"requested_arrival"`
		RequestedDeparture string `json:"requested_departure"`
		RequestedAdults    int    `json:"requested_adults"`
		RequestedChildren  int    `json:"requested_children"`
		RequestedInfants   int    `json:"requested_infants"`
		Notes              string `json:"notes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalSessionRateLimit(w, sessTok, h.portalMutationLimiter, "60") {
		return
	}
	ref := strings.TrimSpace(r.PathValue("reference"))
	enq, err := h.svc.RequestPortalBookingChange(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok, ref, service.PortalBookingChangeRequestInput{
		RequestedArrival:   body.RequestedArrival,
		RequestedDeparture: body.RequestedDeparture,
		RequestedAdults:    body.RequestedAdults,
		RequestedChildren:  body.RequestedChildren,
		RequestedInfants:   body.RequestedInfants,
		Notes:              body.Notes,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"enquiry": enq})
}

func (h *Handler) handlePortalCancelRequest(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug string `json:"property_slug"`
		Note         string `json:"note"`
	}
	if !decodePortalOptionalJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalSessionRateLimit(w, sessTok, h.portalMutationLimiter, "60") {
		return
	}
	ref := strings.TrimSpace(r.PathValue("reference"))
	detail, err := h.svc.RequestPortalBookingCancellation(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok, ref, body.Note)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"booking": detail})
}

func (h *Handler) handlePortalGetProfile(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) {
		return
	}
	sessTok, _, slug := extractPortalSessionAndCSRF(r, "")
	profile, err := h.svc.GetPortalProfile(r.Context(), slug, sessTok)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": profile})
}

func (h *Handler) handlePortalUpdateProfile(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug           string                              `json:"property_slug"`
		Phone                  string                              `json:"phone"`
		PreferredContactMethod string                              `json:"preferred_contact_method"`
		PreferredLanguage      string                              `json:"preferred_language"`
		Preferences            *[]domain.PortalGuestPreferenceView `json:"preferences"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalSessionRateLimit(w, sessTok, h.portalMutationLimiter, "60") {
		return
	}
	profile, err := h.svc.UpdatePortalProfile(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok, service.UpdatePortalProfileInput{
		Phone:                  body.Phone,
		PreferredContactMethod: body.PreferredContactMethod,
		PreferredLanguage:      body.PreferredLanguage,
		Preferences:            body.Preferences,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": profile})
}

func (h *Handler) handlePortalInitiateEmailChange(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug string `json:"property_slug"`
		NewEmail     string `json:"new_email"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalAuthRateLimit(w, r, body.NewEmail, "") || !h.checkPortalSessionRateLimit(w, sessTok, h.portalMutationLimiter, "60") {
		return
	}
	if err := h.svc.InitiatePortalEmailChange(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok, body.NewEmail); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"message":  "If this email address is eligible, a 6-digit verification code has been sent to the new address.",
	})
}

func (h *Handler) handlePortalConfirmEmailChange(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug string `json:"property_slug"`
		Code         string `json:"code"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalAuthRateLimit(w, r, "", "") || !h.checkPortalSessionRateLimit(w, sessTok, h.portalMutationLimiter, "60") {
		return
	}
	profile, err := h.svc.ConfirmPortalEmailChange(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok, body.Code)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": profile})
}

func (h *Handler) handlePortalUpdateConsent(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug string `json:"property_slug"`
		Status       string `json:"status"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalSessionRateLimit(w, sessTok, h.portalMutationLimiter, "60") {
		return
	}
	profile, err := h.svc.UpdatePortalMarketingConsent(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok, domain.ConsentStatus(strings.ToUpper(strings.TrimSpace(body.Status))))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": profile})
}

func (h *Handler) handlePortalListEnquiries(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) {
		return
	}
	sessTok, _, slug := extractPortalSessionAndCSRF(r, "")
	items, err := h.svc.ListPortalEnquiries(r.Context(), slug, sessTok)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enquiries": nonNil(items)})
}

func (h *Handler) handlePortalCreateEnquiry(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug     string `json:"property_slug"`
		Category         string `json:"category"`
		BookingReference string `json:"booking_reference"`
		Subject          string `json:"subject"`
		Message          string `json:"message"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalSessionRateLimit(w, sessTok, h.portalEnquiryLimiter, "3600") {
		return
	}
	enq, err := h.svc.CreatePortalEnquiry(
		r.Context(),
		h.publicActorMeta(r),
		slug,
		sessTok,
		csrfTok,
		domain.EnquiryCategory(strings.ToUpper(strings.TrimSpace(body.Category))),
		body.BookingReference,
		body.Subject,
		body.Message,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"enquiry": enq})
}

func (h *Handler) handlePortalReplyEnquiry(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	enqID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		PropertySlug string `json:"property_slug"`
		Message      string `json:"message"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalSessionRateLimit(w, sessTok, h.portalEnquiryLimiter, "3600") {
		return
	}
	enq, err := h.svc.ReplyPortalEnquiry(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok, enqID, body.Message)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"enquiry": enq})
}

func (h *Handler) handlePortalRequestSAR(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug string `json:"property_slug"`
		Notes        string `json:"notes"`
	}
	if !decodePortalOptionalJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalSessionRateLimit(w, sessTok, h.portalMutationLimiter, "60") {
		return
	}
	receipt, err := h.svc.RequestPortalSAR(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok, body.Notes)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

func (h *Handler) handlePortalRequestErasure(w http.ResponseWriter, r *http.Request) {
	applyPortalSecurityHeaders(w)
	if !rejectPortalQuerySecrets(w, r) || !h.checkPortalMutationOrigin(w, r) {
		return
	}
	var body struct {
		PropertySlug string `json:"property_slug"`
		Reason       string `json:"reason"`
	}
	if !decodePortalOptionalJSON(w, r, &body) {
		return
	}
	sessTok, csrfTok, slug := extractPortalSessionAndCSRF(r, body.PropertySlug)
	if !h.checkPortalSessionRateLimit(w, sessTok, h.portalMutationLimiter, "60") {
		return
	}
	receipt, err := h.svc.RequestPortalErasure(r.Context(), h.publicActorMeta(r), slug, sessTok, csrfTok, body.Reason)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}
