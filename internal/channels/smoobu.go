package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SmoobuAdapter struct {
	http *SafeHTTPClient
}

func NewSmoobuAdapter(cfg AdapterConfig) *SmoobuAdapter {
	return &SmoobuAdapter{http: NewSafeHTTPClient(cfg)}
}

func (a *SmoobuAdapter) ProviderCode() string        { return "smoobu" }
func (a *SmoobuAdapter) HasLiveImplementation() bool { return true }

func (a *SmoobuAdapter) AuthorizeURL(_ context.Context, _, _ string) (string, error) {
	return legacyAuthorizeError("smoobu")
}

func (a *SmoobuAdapter) ExchangeCode(_ context.Context, _, _ string) (map[string]string, error) {
	return legacyExchangeError("smoobu")
}

func (a *SmoobuAdapter) CapabilitiesMatrix(_ context.Context) []CapabilityItem {
	return []CapabilityItem{
		{Name: "hmac_sha256_signing", Status: CapabilitySupported, Detail: "Signs all requests using HMAC-SHA256 (X-API-Key, X-Timestamp, X-Nonce, X-Signature)"},
		{Name: "listing_discovery", Status: CapabilitySupported, Detail: "Discovers apartments/units via GET /api/apartments"},
		{Name: "availability_sync", Status: CapabilitySupported, Detail: "Updates blocked/open dates via POST /api/rates"},
		{Name: "rate_sync", Status: CapabilitySupported, Detail: "Updates nightly prices and min_length_of_stay via POST /api/rates"},
		{Name: "reservation_import", Status: CapabilitySupported, Detail: "Pulls and hydrates reservations via GET /api/reservations and GET /api/reservations/{id}"},
		{Name: "webhooks", Status: CapabilitySupported, Detail: "Verifies configured webhook secret or HMAC headers and re-fetches reservation ground truth"},
		{Name: "token_revoke", Status: CapabilityNotImplemented, Detail: "Smoobu API keys are rotated/revoked in Smoobu Dashboard → API Keys"},
	}
}

func (a *SmoobuAdapter) DiscoverCapabilities(ctx context.Context) ([]string, error) {
	return capabilitySupportedNames(a.CapabilitiesMatrix(ctx)), nil
}

func (a *SmoobuAdapter) RefreshCredentials(_ context.Context, creds map[string]string) (map[string]string, error) {
	return cloneStringMap(creds), nil
}

func (a *SmoobuAdapter) RefreshToken(_ context.Context, _ map[string]string, creds map[string]string) (map[string]string, *time.Time, error) {
	return cloneStringMap(creds), nil, nil
}

func (a *SmoobuAdapter) doSigned(ctx context.Context, creds map[string]string, spec RequestSpec) (*httpResponseData, error) {
	apiKey := strings.TrimSpace(creds["api_key"])
	apiSecret := strings.TrimSpace(creds["api_secret"])
	if apiKey == "" || apiSecret == "" {
		return nil, newProviderError(ErrAuthFailed, "smoobu", spec.Operation, 401, "HMAC_CREDENTIALS_MISSING", "Smoobu api_key and api_secret are required")
	}
	spec.ProviderCode = "smoobu"
	spec.BuildHeaders = func(_ int, reqURL *url.URL, canonicalQuery string, body []byte) (http.Header, error) {
		ts := a.http.Now().UTC().Format(time.RFC3339)
		nonce := a.http.NextNonce()
		sig := SignSmoobuRequest(spec.Method, reqURL.Path, canonicalQuery, ts, nonce, apiKey, apiSecret, body)
		h := make(http.Header)
		h.Set("X-API-Key", apiKey)
		h.Set("X-Timestamp", ts)
		h.Set("X-Nonce", nonce)
		h.Set("X-Signature", sig)
		return h, nil
	}
	return a.http.Do(ctx, spec)
}

func (a *SmoobuAdapter) VerifyConnection(ctx context.Context, publicCfg, creds map[string]string) error {
	_, err := a.VerifyConnectionDetailed(ctx, publicCfg, creds)
	return err
}

func (a *SmoobuAdapter) VerifyConnectionDetailed(ctx context.Context, _ map[string]string, creds map[string]string) (ConnectionVerification, error) {
	resp, err := a.doSigned(ctx, creds, RequestSpec{
		Operation:  "verify_connection",
		Method:     http.MethodGet,
		Path:       "/me",
		Idempotent: true,
	})
	if err != nil {
		return ConnectionVerification{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ConnectionVerification{}, newProviderError(ErrInvalidResponse, "smoobu", "verify_connection", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Smoobu /me verification failed")
	}
	var me struct {
		ID      json.RawMessage `json:"id"`
		Company string          `json:"company"`
	}
	if err := decodeJSONStrict("smoobu", "verify_connection", resp.Body, &me); err != nil {
		return ConnectionVerification{}, err
	}
	accID := flexibleID(me.ID)
	if accID == "" {
		return ConnectionVerification{}, newProviderError(ErrInvalidResponse, "smoobu", "verify_connection", resp.StatusCode, "MISSING_ACCOUNT_ID", "Smoobu /me response omitted account ID")
	}
	accName := strings.TrimSpace(me.Company)
	if accName == "" {
		accName = "Smoobu Account #" + accID
	}
	return ConnectionVerification{
		VerifiedAt:       a.http.Now(),
		AccountID:        accID,
		AccountName:      accName,
		Environment:      "production",
		DiscoveredFields: map[string]string{"account_id": accID},
		Capabilities:     a.CapabilitiesMatrix(ctx),
	}, nil
}

func (a *SmoobuAdapter) Disconnect(ctx context.Context, publicCfg, creds map[string]string) error {
	if whID := strings.TrimSpace(publicCfg["webhook_id"]); whID != "" {
		if err := a.DeleteWebhook(ctx, publicCfg, creds, whID); err == nil {
			return nil
		}
	}
	return newProviderError(ErrNotImplemented, "smoobu", "disconnect", 0, "NO_REMOTE_REVOKE", "Smoobu API keys cannot be remotely revoked via REST API")
}

func (a *SmoobuAdapter) ListListings(ctx context.Context, publicCfg, creds map[string]string) ([]ExternalListing, error) {
	return a.DiscoverListingsDetailed(ctx, publicCfg, creds)
}

func (a *SmoobuAdapter) DiscoverListingsDetailed(ctx context.Context, _ map[string]string, creds map[string]string) ([]ExternalListing, error) {
	out := make([]ExternalListing, 0, 16)
	for page := 1; page <= 25; page++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("pageSize", "100")
		resp, err := a.doSigned(ctx, creds, RequestSpec{
			Operation:  "list_listings",
			Method:     http.MethodGet,
			Path:       "/apartments",
			Query:      q,
			Idempotent: true,
		})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, newProviderError(ErrInvalidResponse, "smoobu", "list_listings", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed listing Smoobu apartments")
		}

		var payload struct {
			PageCount  int `json:"page_count"`
			Page       int `json:"page"`
			Apartments []struct {
				ID       json.RawMessage `json:"id"`
				Name     string          `json:"name"`
				Currency string          `json:"currency"`
			} `json:"apartments"`
		}
		if err := decodeJSONStrict("smoobu", "list_listings", resp.Body, &payload); err != nil {
			return nil, err
		}
		for _, apt := range payload.Apartments {
			id := flexibleID(apt.ID)
			if id == "" {
				continue
			}
			cur := strings.ToUpper(strings.TrimSpace(apt.Currency))
			out = append(out, ExternalListing{
				ExternalID:   id,
				Name:         strings.TrimSpace(apt.Name),
				Channel:      "smoobu",
				Currency:     cur,
				SupportsPush: true,
			})
		}
		if payload.PageCount <= page || len(payload.Apartments) == 0 {
			break
		}
	}
	return out, nil
}

func (a *SmoobuAdapter) SyncAvailability(ctx context.Context, publicCfg, creds map[string]string, raw json.RawMessage) error {
	var req AvailabilityPushRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return newProviderError(ErrInvalidInput, "smoobu", "push_availability", 0, "INVALID_PAYLOAD", "invalid availability payload")
	}
	return a.PushAvailability(ctx, publicCfg, creds, req)
}

func (a *SmoobuAdapter) PushAvailability(ctx context.Context, _ map[string]string, creds map[string]string, req AvailabilityPushRequest) error {
	aptID, err := strconv.ParseInt(strings.TrimSpace(req.ExternalListingID), 10, 64)
	if err != nil || aptID <= 0 || len(req.Days) == 0 {
		return newProviderError(ErrInvalidInput, "smoobu", "push_availability", 0, "INVALID_LISTING_ID", "valid numeric Smoobu apartment ID and days are required")
	}

	openDates := make([]string, 0, len(req.Days))
	blockedDates := make([]string, 0, len(req.Days))
	for _, d := range req.Days {
		blocked := d.AvailableUnits <= 0 || (d.StopSell != nil && *d.StopSell)
		if blocked {
			blockedDates = append(blockedDates, d.Date)
		} else {
			openDates = append(openDates, d.Date)
		}
	}

	ops := make([]map[string]any, 0, 2)
	if len(openDates) > 0 {
		ops = append(ops, map[string]any{
			"dates":   openDates,
			"blocked": 0,
		})
	}
	if len(blockedDates) > 0 {
		ops = append(ops, map[string]any{
			"dates":   blockedDates,
			"blocked": 1,
		})
	}
	return a.postSmoobuRates(ctx, creds, "push_availability", aptID, ops)
}

func (a *SmoobuAdapter) SyncRates(ctx context.Context, publicCfg, creds map[string]string, raw json.RawMessage) error {
	var req RatePushRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return newProviderError(ErrInvalidInput, "smoobu", "push_rates", 0, "INVALID_PAYLOAD", "invalid rates payload")
	}
	return a.PushRates(ctx, publicCfg, creds, req)
}

func (a *SmoobuAdapter) PushRates(ctx context.Context, _ map[string]string, creds map[string]string, req RatePushRequest) error {
	aptID, err := strconv.ParseInt(strings.TrimSpace(req.ExternalListingID), 10, 64)
	if err != nil || aptID <= 0 || len(req.Days) == 0 {
		return newProviderError(ErrInvalidInput, "smoobu", "push_rates", 0, "INVALID_LISTING_ID", "valid numeric Smoobu apartment ID and days are required")
	}

	ops := make([]map[string]any, 0, len(req.Days))
	for _, d := range req.Days {
		num, err := FormatMinorToNumber(d.PriceMinor, d.Currency)
		if err != nil {
			return err
		}
		op := map[string]any{
			"dates":       []string{d.Date},
			"daily_price": num,
		}
		if d.MinStay > 0 {
			op["min_length_of_stay"] = d.MinStay
		}
		if d.StopSell != nil {
			if *d.StopSell {
				op["blocked"] = 1
			} else {
				op["blocked"] = 0
			}
		}
		ops = append(ops, op)
	}
	return a.postSmoobuRates(ctx, creds, "push_rates", aptID, ops)
}

func (a *SmoobuAdapter) postSmoobuRates(ctx context.Context, creds map[string]string, op string, aptID int64, ops []map[string]any) error {
	payload := map[string]any{
		"apartments": []int64{aptID},
		"operations": ops,
	}
	body, _ := json.Marshal(payload)
	resp, err := a.doSigned(ctx, creds, RequestSpec{
		Operation:  op,
		Method:     http.MethodPost,
		Path:       "/rates",
		Body:       body,
		Idempotent: true,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newProviderError(ErrInvalidResponse, "smoobu", op, resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Smoobu /api/rates update failed")
	}
	return nil
}

func (a *SmoobuAdapter) ImportReservations(ctx context.Context, publicCfg, creds map[string]string, since time.Time) ([]json.RawMessage, error) {
	items, _, err := a.PullReservations(ctx, publicCfg, creds, since, "")
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(items))
	for _, it := range items {
		b, _ := json.Marshal(it)
		out = append(out, b)
	}
	return out, nil
}

func (a *SmoobuAdapter) PullReservations(ctx context.Context, _ map[string]string, creds map[string]string, since time.Time, cursor string) ([]ExternalReservation, string, error) {
	startPage := 1
	if p, err := strconv.Atoi(strings.TrimSpace(cursor)); err == nil && p > 0 {
		startPage = p
	}
	out := make([]ExternalReservation, 0, 32)
	for page := startPage; page < startPage+15; page++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("pageSize", "100")
		q.Set("showCancellation", "true")
		if !since.IsZero() {
			q.Set("modifiedFrom", since.UTC().Format("2006-01-02 15:04:05"))
		}
		resp, err := a.doSigned(ctx, creds, RequestSpec{
			Operation:  "pull_reservations",
			Method:     http.MethodGet,
			Path:       "/reservations",
			Query:      q,
			Idempotent: true,
		})
		if err != nil {
			return nil, "", err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, "", newProviderError(ErrInvalidResponse, "smoobu", "pull_reservations", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed pulling Smoobu reservations")
		}
		var payload struct {
			PageCount int                 `json:"page_count"`
			Bookings  []smoobuBookingJSON `json:"bookings"`
		}
		if err := decodeJSONStrict("smoobu", "pull_reservations", resp.Body, &payload); err != nil {
			return nil, "", err
		}
		for _, b := range payload.Bookings {
			if er, ok := b.toExternal(); ok {
				out = append(out, er)
			}
		}
		if payload.PageCount <= page || len(payload.Bookings) == 0 {
			break
		}
	}
	return out, "", nil
}

func (a *SmoobuAdapter) HydrateReservation(ctx context.Context, _ map[string]string, creds map[string]string, _ string, externalReservationID string) (ExternalReservation, error) {
	id := strings.TrimSpace(externalReservationID)
	if id == "" {
		return ExternalReservation{}, newProviderError(ErrInvalidInput, "smoobu", "hydrate_reservation", 0, "MISSING_ID", "reservation ID is required")
	}
	resp, err := a.doSigned(ctx, creds, RequestSpec{
		Operation:  "hydrate_reservation",
		Method:     http.MethodGet,
		Path:       "/reservations/" + url.PathEscape(id),
		Idempotent: true,
	})
	if err != nil {
		return ExternalReservation{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ExternalReservation{}, newProviderError(ErrInvalidResponse, "smoobu", "hydrate_reservation", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed fetching Smoobu reservation detail")
	}
	var b smoobuBookingJSON
	if err := decodeJSONStrict("smoobu", "hydrate_reservation", resp.Body, &b); err != nil {
		return ExternalReservation{}, err
	}
	er, ok := b.toExternal()
	if !ok {
		return ExternalReservation{}, newProviderError(ErrInvalidResponse, "smoobu", "hydrate_reservation", resp.StatusCode, "INVALID_BOOKING", "Smoobu reservation omitted ID")
	}
	return er, nil
}

type smoobuBookingJSON struct {
	ID        json.RawMessage `json:"id"`
	Type      string          `json:"type"` // reservation | cancellation | modification
	Arrival   string          `json:"arrival"`
	Departure string          `json:"departure"`
	Apartment struct {
		ID   json.RawMessage `json:"id"`
		Name string          `json:"name"`
	} `json:"apartment"`
	Channel struct {
		ID   json.RawMessage `json:"id"`
		Name string          `json:"name"`
	} `json:"channel"`
	GuestName  string          `json:"guest-name"`
	FirstName  string          `json:"firstname"`
	LastName   string          `json:"lastname"`
	Email      string          `json:"email"`
	Phone      string          `json:"phone"`
	Adults     int             `json:"adults"`
	Children   int             `json:"children"`
	Price      json.RawMessage `json:"price"`
	Currency   string          `json:"currency"`
	ModifiedAt string          `json:"modified-at"`
	CreatedAt  string          `json:"created-at"`
}

func (b smoobuBookingJSON) toExternal() (ExternalReservation, bool) {
	id := flexibleID(b.ID)
	if id == "" {
		return ExternalReservation{}, false
	}
	cur := strings.ToUpper(strings.TrimSpace(b.Currency))
	if cur == "" {
		cur = "GBP"
	}
	name := strings.TrimSpace(b.GuestName)
	if name == "" {
		name = strings.TrimSpace(strings.TrimSpace(b.FirstName) + " " + strings.TrimSpace(b.LastName))
	}
	if name == "" {
		name = "Smoobu Guest"
	}
	status := "CONFIRMED"
	if strings.EqualFold(strings.TrimSpace(b.Type), "cancellation") {
		status = "CANCELLED"
	}
	modRaw := strings.TrimSpace(b.ModifiedAt)
	if modRaw == "" {
		modRaw = strings.TrimSpace(b.CreatedAt)
	}
	rev := modRaw
	if rev == "" {
		rev = status
	}
	er := ExternalReservation{
		ProviderCode:          "smoobu",
		ExternalReservationID: id,
		ExternalRevision:      rev,
		ExternalListingID:     flexibleID(b.Apartment.ID),
		Status:                status,
		SourceChannel:         normalizeSourceChannel(b.Channel.Name, "smoobu"),
		CheckIn:               parseDateOnlyString(b.Arrival),
		CheckOut:              parseDateOnlyString(b.Departure),
		Currency:              cur,
		TotalMinor:            flexibleMoneyMinor(b.Price, cur),
		Adults:                maxInt(b.Adults, 1),
		Children:              maxInt(b.Children, 0),
		GuestName:             name,
		GuestEmail:            strings.TrimSpace(b.Email),
		GuestPhone:            strings.TrimSpace(b.Phone),
		ProviderUpdatedAt:     parseFlexibleTimestamp(modRaw),
	}
	er.PayloadHash = computePayloadDigest(er)
	return er, true
}

func (a *SmoobuAdapter) AcknowledgeRevision(_ context.Context, _, _ map[string]string, _ string) error {
	return nil
}

func (a *SmoobuAdapter) HandleWebhook(_ context.Context, _ map[string]string, _ []byte) error {
	return newProviderError(ErrWebhookVerificationFailed, "smoobu", "webhook", 401, "CONNECTION_CONTEXT_REQUIRED", "connection credentials required to verify webhook")
}

func (a *SmoobuAdapter) VerifyWebhook(ctx context.Context, _ map[string]string, creds map[string]string, headers map[string]string, body []byte, checkNonce NonceChecker) (WebhookEvent, error) {
	norm := normalizeHeaderMap(headers)
	if norm["x-signature"] != "" && norm["x-timestamp"] != "" && norm["x-nonce"] != "" {
		reqPath := norm["x-lovelace-webhook-path"]
		if err := VerifySmoobuSignature(ctx, http.MethodPost, reqPath, "", headers, body, creds["api_key"], creds["api_secret"], a.http.Now(), checkNonce); err != nil {
			return WebhookEvent{}, err
		}
	} else {
		if err := VerifySharedSecretWebhook("smoobu", headers, body, []string{creds["webhook_secret"]}); err != nil {
			return WebhookEvent{}, err
		}
	}

	var payload struct {
		Action string            `json:"action"`
		Data   smoobuBookingJSON `json:"data"`
	}
	if err := decodeJSONStrict("smoobu", "webhook", body, &payload); err != nil {
		return WebhookEvent{}, err
	}
	resID := flexibleID(payload.Data.ID)
	aptID := flexibleID(payload.Data.Apartment.ID)
	action := "reservation_sync"
	switch strings.ToLower(strings.TrimSpace(payload.Action)) {
	case "cancelreservation":
		action = "reservation_cancel"
	case "updaterates":
		action = "availability_ping"
	}
	rev := strings.TrimSpace(payload.Data.ModifiedAt)
	if rev == "" {
		rev = SHA256Hex(body)[:16]
	}
	evType := strings.TrimSpace(payload.Action)
	if evType == "" {
		evType = "reservationWebhook"
	}
	return WebhookEvent{
		ProviderCode:          "smoobu",
		ExternalEventID:       computePayloadDigest(map[string]string{"action": evType, "id": resID, "sha": SHA256Hex(body)})[:32],
		EventType:             evType,
		Action:                action,
		ExternalListingID:     aptID,
		ExternalReservationID: resID,
		ExternalRevision:      rev,
		SignatureVerified:     true,
	}, nil
}

func (a *SmoobuAdapter) RegisterWebhook(ctx context.Context, _ map[string]string, creds map[string]string, callbackURL string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"url":     callbackURL,
		"actions": []string{"newReservation", "updateReservation", "cancelReservation"},
	})
	resp, err := a.doSigned(ctx, creds, RequestSpec{
		Operation: "register_webhook",
		Method:    http.MethodPost,
		Path:      "/users/webhooks",
		Body:      body,
	})
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", newProviderError(ErrInvalidResponse, "smoobu", "register_webhook", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Smoobu webhook registration failed")
	}
	var out struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(resp.Body, &out)
	return flexibleID(out.ID), nil
}

func (a *SmoobuAdapter) DeleteWebhook(ctx context.Context, _ map[string]string, creds map[string]string, webhookID string) error {
	resp, err := a.doSigned(ctx, creds, RequestSpec{
		Operation:  "delete_webhook",
		Method:     http.MethodDelete,
		Path:       "/users/webhooks/" + url.PathEscape(strings.TrimSpace(webhookID)),
		Idempotent: true,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newProviderError(ErrInvalidResponse, "smoobu", "delete_webhook", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Smoobu webhook deletion failed")
	}
	return nil
}
