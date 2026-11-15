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

type LodgifyAdapter struct {
	http *SafeHTTPClient
}

func NewLodgifyAdapter(cfg AdapterConfig) *LodgifyAdapter {
	return &LodgifyAdapter{http: NewSafeHTTPClient(cfg)}
}

func (a *LodgifyAdapter) ProviderCode() string        { return "lodgify" }
func (a *LodgifyAdapter) HasLiveImplementation() bool { return true }

func (a *LodgifyAdapter) AuthorizeURL(_ context.Context, _, _ string) (string, error) {
	return legacyAuthorizeError("lodgify")
}

func (a *LodgifyAdapter) ExchangeCode(_ context.Context, _, _ string) (map[string]string, error) {
	return legacyExchangeError("lodgify")
}

func (a *LodgifyAdapter) CapabilitiesMatrix(_ context.Context) []CapabilityItem {
	return []CapabilityItem{
		{Name: "listing_discovery", Status: CapabilitySupported, Detail: "Discovers properties and room types via GET /v2/properties"},
		{Name: "availability_sync", Status: CapabilityBlocked, Detail: "BLOCKED: Lodgify Public API v1/v2 does not support direct outbound availability count writes without Partner Connectivity approval"},
		{Name: "rate_sync", Status: CapabilitySupported, Detail: "Updates nightly rates without altering availability via POST /v1/rates/savewithoutavailability"},
		{Name: "reservation_import", Status: CapabilitySupported, Detail: "Pulls and hydrates bookings via GET /v2/reservations/bookings and GET /v2/reservations/bookings/{id}"},
		{Name: "webhooks", Status: CapabilitySupported, Detail: "Receives configured-secret or HMAC webhooks and re-fetches booking state via v2 API"},
		{Name: "token_revoke", Status: CapabilityNotImplemented, Detail: "Lodgify Public API keys are rotated/revoked manually in Lodgify Settings → Public API"},
	}
}

func (a *LodgifyAdapter) DiscoverCapabilities(ctx context.Context) ([]string, error) {
	return capabilitySupportedNames(a.CapabilitiesMatrix(ctx)), nil
}

func (a *LodgifyAdapter) RefreshCredentials(_ context.Context, creds map[string]string) (map[string]string, error) {
	return cloneStringMap(creds), nil
}

func (a *LodgifyAdapter) RefreshToken(_ context.Context, _ map[string]string, creds map[string]string) (map[string]string, *time.Time, error) {
	return cloneStringMap(creds), nil, nil
}

func (a *LodgifyAdapter) doAPIKey(ctx context.Context, creds map[string]string, spec RequestSpec) (*httpResponseData, error) {
	apiKey := strings.TrimSpace(creds["api_key"])
	if apiKey == "" {
		return nil, newProviderError(ErrAuthFailed, "lodgify", spec.Operation, 401, "API_KEY_MISSING", "Lodgify api_key is required")
	}
	spec.ProviderCode = "lodgify"
	spec.BuildHeaders = func(_ int, _ *url.URL, _ string, _ []byte) (http.Header, error) {
		h := make(http.Header)
		h.Set("X-ApiKey", apiKey)
		return h, nil
	}
	return a.http.Do(ctx, spec)
}

func (a *LodgifyAdapter) VerifyConnection(ctx context.Context, publicCfg, creds map[string]string) error {
	_, err := a.VerifyConnectionDetailed(ctx, publicCfg, creds)
	return err
}

func (a *LodgifyAdapter) VerifyConnectionDetailed(ctx context.Context, publicCfg, creds map[string]string) (ConnectionVerification, error) {
	q := url.Values{}
	q.Set("page", "1")
	q.Set("size", "1")
	resp, err := a.doAPIKey(ctx, creds, RequestSpec{
		Operation:  "verify_connection",
		Method:     http.MethodGet,
		Path:       "/v2/properties",
		Query:      q,
		Idempotent: true,
	})
	if err != nil {
		return ConnectionVerification{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ConnectionVerification{}, newProviderError(ErrInvalidResponse, "lodgify", "verify_connection", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Lodgify API key verification failed")
	}
	var payload struct {
		Items []struct {
			ID   json.RawMessage `json:"id"`
			Name string          `json:"name"`
		} `json:"items"`
	}
	if err := decodeJSONStrict("lodgify", "verify_connection", resp.Body, &payload); err != nil {
		return ConnectionVerification{}, err
	}
	accID := strings.TrimSpace(publicCfg["account_id"])
	if accID == "" && len(payload.Items) > 0 {
		accID = flexibleID(payload.Items[0].ID)
	}
	return ConnectionVerification{
		VerifiedAt:   a.http.Now(),
		AccountID:    accID,
		AccountName:  "Lodgify Account " + accID,
		Environment:  "production",
		Capabilities: a.CapabilitiesMatrix(ctx),
	}, nil
}

func (a *LodgifyAdapter) Disconnect(ctx context.Context, publicCfg, creds map[string]string) error {
	if whID := strings.TrimSpace(publicCfg["webhook_id"]); whID != "" {
		_ = a.DeleteWebhook(ctx, publicCfg, creds, whID)
	}
	return newProviderError(ErrNotImplemented, "lodgify", "disconnect", 0, "NO_REMOTE_REVOKE", "Lodgify Public API keys cannot be remotely revoked")
}

func (a *LodgifyAdapter) ListListings(ctx context.Context, publicCfg, creds map[string]string) ([]ExternalListing, error) {
	return a.DiscoverListingsDetailed(ctx, publicCfg, creds)
}

func (a *LodgifyAdapter) DiscoverListingsDetailed(ctx context.Context, _ map[string]string, creds map[string]string) ([]ExternalListing, error) {
	out := make([]ExternalListing, 0, 16)
	for page := 1; page <= 25; page++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("size", "50")
		q.Set("includeCount", "true")
		resp, err := a.doAPIKey(ctx, creds, RequestSpec{
			Operation:  "list_listings",
			Method:     http.MethodGet,
			Path:       "/v2/properties",
			Query:      q,
			Idempotent: true,
		})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, newProviderError(ErrInvalidResponse, "lodgify", "list_listings", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed listing Lodgify properties")
		}
		var payload struct {
			Count int `json:"count"`
			Items []struct {
				ID           json.RawMessage `json:"id"`
				Name         string          `json:"name"`
				CurrencyCode string          `json:"currency_code"`
				Rooms        []struct {
					ID   json.RawMessage `json:"id"`
					Name string          `json:"name"`
				} `json:"rooms"`
			} `json:"items"`
		}
		if err := decodeJSONStrict("lodgify", "list_listings", resp.Body, &payload); err != nil {
			return nil, err
		}
		for _, prop := range payload.Items {
			pid := flexibleID(prop.ID)
			cur := strings.ToUpper(strings.TrimSpace(prop.CurrencyCode))
			if len(prop.Rooms) == 0 && pid != "" {
				out = append(out, ExternalListing{
					ExternalID:         pid,
					ExternalPropertyID: pid,
					Name:               strings.TrimSpace(prop.Name),
					Channel:            "lodgify",
					Currency:           cur,
					SupportsPush:       true, // Rate sync supported; availability blocked in CapabilitiesMatrix.
				})
				continue
			}
			for _, rm := range prop.Rooms {
				rid := flexibleID(rm.ID)
				if rid == "" {
					continue
				}
				name := strings.TrimSpace(rm.Name)
				if prop.Name != "" && name != "" {
					name = prop.Name + " — " + name
				}
				out = append(out, ExternalListing{
					ExternalID:         rid,
					ExternalPropertyID: pid,
					Name:               name,
					Channel:            "lodgify",
					Currency:           cur,
					SupportsPush:       true,
				})
			}
		}
		if len(payload.Items) == 0 || (payload.Count > 0 && len(out) >= payload.Count) || len(payload.Items) < 50 {
			break
		}
	}
	return out, nil
}

func (a *LodgifyAdapter) SyncAvailability(_ context.Context, _, _ map[string]string, _ json.RawMessage) error {
	return newProviderError(ErrOperationBlocked, "lodgify", "push_availability", 0, "AVAILABILITY_PUSH_BLOCKED", "Lodgify Public API does not support direct outbound availability count overwrite; Partner Connectivity required")
}

func (a *LodgifyAdapter) PushAvailability(_ context.Context, _, _ map[string]string, _ AvailabilityPushRequest) error {
	return newProviderError(ErrOperationBlocked, "lodgify", "push_availability", 0, "AVAILABILITY_PUSH_BLOCKED", "Lodgify Public API does not support direct outbound availability count overwrite; Partner Connectivity required")
}

func (a *LodgifyAdapter) SyncRates(ctx context.Context, publicCfg, creds map[string]string, raw json.RawMessage) error {
	var req RatePushRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return newProviderError(ErrInvalidInput, "lodgify", "push_rates", 0, "INVALID_PAYLOAD", "invalid rates payload")
	}
	return a.PushRates(ctx, publicCfg, creds, req)
}

func (a *LodgifyAdapter) PushRates(ctx context.Context, _ map[string]string, creds map[string]string, req RatePushRequest) error {
	propID, pErr := strconv.ParseInt(strings.TrimSpace(req.ExternalPropertyID), 10, 64)
	roomTypeID, rErr := strconv.ParseInt(strings.TrimSpace(req.ExternalListingID), 10, 64)
	if rErr != nil || roomTypeID <= 0 || len(req.Days) == 0 {
		return newProviderError(ErrInvalidInput, "lodgify", "push_rates", 0, "INVALID_LISTING_ID", "valid numeric Lodgify room type ID and days are required")
	}
	if pErr != nil || propID <= 0 {
		propID = roomTypeID
	}

	rates := make([]map[string]any, 0, len(req.Days))
	for _, d := range req.Days {
		num, err := FormatMinorToNumber(d.PriceMinor, d.Currency)
		if err != nil {
			return err
		}
		item := map[string]any{
			"is_default":     false,
			"start_date":     d.Date,
			"end_date":       d.Date,
			"price_per_day":  num,
			"min_stay":       maxInt(d.MinStay, 1),
			"currency_code":  strings.ToUpper(d.Currency),
			"room_type_id":   roomTypeID,
			"property_id":    propID,
			"price_per_week": nil,
		}
		if d.MaxStay != nil && *d.MaxStay > 0 {
			item["max_stay"] = *d.MaxStay
		}
		rates = append(rates, item)
	}
	payload := map[string]any{
		"property_id":  propID,
		"room_type_id": roomTypeID,
		"rates":        rates,
	}
	body, _ := json.Marshal(payload)
	resp, err := a.doAPIKey(ctx, creds, RequestSpec{
		Operation:  "push_rates",
		Method:     http.MethodPost,
		Path:       "/v1/rates/savewithoutavailability",
		Body:       body,
		Idempotent: true,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newProviderError(ErrInvalidResponse, "lodgify", "push_rates", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Lodgify v1 rate update failed")
	}
	return nil
}

func (a *LodgifyAdapter) ImportReservations(ctx context.Context, publicCfg, creds map[string]string, since time.Time) ([]json.RawMessage, error) {
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

func (a *LodgifyAdapter) PullReservations(ctx context.Context, _ map[string]string, creds map[string]string, since time.Time, cursor string) ([]ExternalReservation, string, error) {
	startPage := 1
	if p, err := strconv.Atoi(strings.TrimSpace(cursor)); err == nil && p > 0 {
		startPage = p
	}
	out := make([]ExternalReservation, 0, 32)
	for page := startPage; page < startPage+15; page++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("size", "50")
		q.Set("stayFilter", "All")
		if !since.IsZero() {
			q.Set("updatedSince", since.UTC().Format(time.RFC3339))
		}
		resp, err := a.doAPIKey(ctx, creds, RequestSpec{
			Operation:  "pull_reservations",
			Method:     http.MethodGet,
			Path:       "/v2/reservations/bookings",
			Query:      q,
			Idempotent: true,
		})
		if err != nil {
			return nil, "", err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, "", newProviderError(ErrInvalidResponse, "lodgify", "pull_reservations", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed pulling Lodgify bookings")
		}
		var payload struct {
			Count int                 `json:"count"`
			Items []lodgifyBookingDTO `json:"items"`
		}
		if err := decodeJSONStrict("lodgify", "pull_reservations", resp.Body, &payload); err != nil {
			return nil, "", err
		}
		for _, b := range payload.Items {
			if er, ok := b.toExternal(); ok {
				out = append(out, er)
			}
		}
		if len(payload.Items) < 50 || len(payload.Items) == 0 {
			break
		}
	}
	return out, "", nil
}

func (a *LodgifyAdapter) HydrateReservation(ctx context.Context, _ map[string]string, creds map[string]string, _ string, externalReservationID string) (ExternalReservation, error) {
	id := strings.TrimSpace(externalReservationID)
	if id == "" {
		return ExternalReservation{}, newProviderError(ErrInvalidInput, "lodgify", "hydrate_reservation", 0, "MISSING_ID", "booking ID is required")
	}
	resp, err := a.doAPIKey(ctx, creds, RequestSpec{
		Operation:  "hydrate_reservation",
		Method:     http.MethodGet,
		Path:       "/v2/reservations/bookings/" + url.PathEscape(id),
		Idempotent: true,
	})
	if err != nil {
		return ExternalReservation{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ExternalReservation{}, newProviderError(ErrInvalidResponse, "lodgify", "hydrate_reservation", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed fetching Lodgify booking detail")
	}
	var b lodgifyBookingDTO
	if err := decodeJSONStrict("lodgify", "hydrate_reservation", resp.Body, &b); err != nil {
		return ExternalReservation{}, err
	}
	er, ok := b.toExternal()
	if !ok {
		return ExternalReservation{}, newProviderError(ErrInvalidResponse, "lodgify", "hydrate_reservation", resp.StatusCode, "INVALID_BOOKING", "Lodgify booking omitted ID")
	}
	return er, nil
}

type lodgifyBookingDTO struct {
	ID           json.RawMessage `json:"id"`
	PropertyID   json.RawMessage `json:"property_id"`
	Status       string          `json:"status"`
	Source       string          `json:"source"`
	Arrival      string          `json:"arrival"`
	Departure    string          `json:"departure"`
	CurrencyCode string          `json:"currency_code"`
	TotalAmount  json.RawMessage `json:"total_amount"`
	UpdatedAt    string          `json:"updated_at"`
	CreatedAt    string          `json:"created_at"`
	Rooms        []struct {
		RoomTypeID json.RawMessage `json:"room_type_id"`
		People     int             `json:"people"`
	} `json:"rooms"`
	Guest struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Phone string `json:"phone"`
	} `json:"guest"`
}

func (b lodgifyBookingDTO) toExternal() (ExternalReservation, bool) {
	id := flexibleID(b.ID)
	if id == "" {
		return ExternalReservation{}, false
	}
	cur := strings.ToUpper(strings.TrimSpace(b.CurrencyCode))
	if cur == "" {
		cur = "GBP"
	}
	propID := flexibleID(b.PropertyID)
	listingID := propID
	adults := 1
	if len(b.Rooms) > 0 {
		if rtID := flexibleID(b.Rooms[0].RoomTypeID); rtID != "" {
			listingID = rtID
		}
		if b.Rooms[0].People > 0 {
			adults = b.Rooms[0].People
		}
	}
	guestName := strings.TrimSpace(b.Guest.Name)
	if guestName == "" {
		guestName = "Lodgify Guest"
	}
	modRaw := strings.TrimSpace(b.UpdatedAt)
	if modRaw == "" {
		modRaw = strings.TrimSpace(b.CreatedAt)
	}
	status := normalizeReservationStatus(b.Status)
	rev := modRaw
	if rev == "" {
		rev = status
	}
	er := ExternalReservation{
		ProviderCode:          "lodgify",
		ExternalReservationID: id,
		ExternalRevision:      rev,
		ExternalPropertyID:    propID,
		ExternalListingID:     listingID,
		Status:                status,
		SourceChannel:         normalizeSourceChannel(b.Source, "lodgify"),
		CheckIn:               parseDateOnlyString(b.Arrival),
		CheckOut:              parseDateOnlyString(b.Departure),
		Currency:              cur,
		TotalMinor:            flexibleMoneyMinor(b.TotalAmount, cur),
		Adults:                adults,
		GuestName:             guestName,
		GuestEmail:            strings.TrimSpace(b.Guest.Email),
		GuestPhone:            strings.TrimSpace(b.Guest.Phone),
		ProviderUpdatedAt:     parseFlexibleTimestamp(modRaw),
	}
	er.PayloadHash = computePayloadDigest(er)
	return er, true
}

func (a *LodgifyAdapter) AcknowledgeRevision(_ context.Context, _, _ map[string]string, _ string) error {
	return nil
}

func (a *LodgifyAdapter) HandleWebhook(_ context.Context, _ map[string]string, _ []byte) error {
	return newProviderError(ErrWebhookVerificationFailed, "lodgify", "webhook", 401, "CONNECTION_CONTEXT_REQUIRED", "connection credentials required to verify webhook")
}

func (a *LodgifyAdapter) VerifyWebhook(_ context.Context, publicCfg, creds map[string]string, headers map[string]string, body []byte, _ NonceChecker) (WebhookEvent, error) {
	if err := VerifySharedSecretWebhook("lodgify", headers, body, []string{creds["webhook_secret"]}); err != nil {
		return WebhookEvent{}, err
	}
	var payload struct {
		Event   string            `json:"event"`
		Booking lodgifyBookingDTO `json:"booking"`
		ID      json.RawMessage   `json:"booking_id"`
	}
	_ = json.Unmarshal(body, &payload)
	bookingID := flexibleID(payload.Booking.ID)
	if bookingID == "" {
		bookingID = flexibleID(payload.ID)
	}
	action := "reservation_sync"
	if strings.Contains(strings.ToLower(payload.Event), "cancel") || normalizeReservationStatus(payload.Booking.Status) == "CANCELLED" {
		action = "reservation_cancel"
	} else if strings.Contains(strings.ToLower(payload.Event), "availability") {
		action = "availability_ping"
	}
	evType := strings.TrimSpace(payload.Event)
	if evType == "" {
		evType = "booking_change"
	}
	return WebhookEvent{
		ProviderCode:          "lodgify",
		ExternalEventID:       computePayloadDigest(map[string]string{"ev": evType, "id": bookingID, "sha": SHA256Hex(body)})[:32],
		EventType:             evType,
		Action:                action,
		ExternalPropertyID:    flexibleID(payload.Booking.PropertyID),
		ExternalReservationID: bookingID,
		ExternalRevision:      strings.TrimSpace(payload.Booking.UpdatedAt),
		SignatureVerified:     true,
	}, nil
}

func (a *LodgifyAdapter) RegisterWebhook(ctx context.Context, _ map[string]string, creds map[string]string, callbackURL string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"target_url": callbackURL,
		"event":      "booking_change",
	})
	resp, err := a.doAPIKey(ctx, creds, RequestSpec{
		Operation: "register_webhook",
		Method:    http.MethodPost,
		Path:      "/webhooks/v1/subscribe",
		Body:      body,
	})
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", newProviderError(ErrInvalidResponse, "lodgify", "register_webhook", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Lodgify webhook subscription failed")
	}
	var out struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(resp.Body, &out)
	return flexibleID(out.ID), nil
}

func (a *LodgifyAdapter) DeleteWebhook(ctx context.Context, _ map[string]string, creds map[string]string, webhookID string) error {
	resp, err := a.doAPIKey(ctx, creds, RequestSpec{
		Operation:  "delete_webhook",
		Method:     http.MethodDelete,
		Path:       "/webhooks/v1/" + url.PathEscape(strings.TrimSpace(webhookID)),
		Idempotent: true,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newProviderError(ErrInvalidResponse, "lodgify", "delete_webhook", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Lodgify webhook deletion failed")
	}
	return nil
}
