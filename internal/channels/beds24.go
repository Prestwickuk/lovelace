package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const beds24RefreshBuffer = 5 * time.Minute

type Beds24Adapter struct {
	http *SafeHTTPClient
}

func NewBeds24Adapter(cfg AdapterConfig) *Beds24Adapter {
	return &Beds24Adapter{http: NewSafeHTTPClient(cfg)}
}

func (a *Beds24Adapter) ProviderCode() string        { return "beds24" }
func (a *Beds24Adapter) HasLiveImplementation() bool { return true }

func (a *Beds24Adapter) AuthorizeURL(ctx context.Context, state string, redirectURI string) (string, error) {
	return legacyAuthorizeError("beds24")
}

func (a *Beds24Adapter) ExchangeCode(ctx context.Context, code string, redirectURI string) (map[string]string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, newProviderError(ErrInvalidInput, "beds24", "exchange_code", 0, "INVITE_CODE_REQUIRED", "Beds24 invite code is required")
	}
	resp, err := a.http.Do(ctx, RequestSpec{
		ProviderCode: "beds24",
		Operation:    "exchange_invite_code",
		Method:       http.MethodGet,
		Path:         "/authentication/setup",
		Idempotent:   true,
		BuildHeaders: func(_ int, _ *url.URL, _ string, _ []byte) (http.Header, error) {
			h := make(http.Header)
			h.Set("code", code)
			return h, nil
		},
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newProviderError(ErrAuthFailed, "beds24", "exchange_invite_code", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Beds24 invite code setup failed")
	}
	var out struct {
		Token        string `json:"token"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
	}
	if err := decodeJSONStrict("beds24", "exchange_invite_code", resp.Body, &out); err != nil {
		return nil, err
	}
	if out.RefreshToken == "" || out.Token == "" {
		return nil, newProviderError(ErrInvalidResponse, "beds24", "exchange_invite_code", resp.StatusCode, "MISSING_TOKEN", "Beds24 setup response omitted tokens")
	}
	exp := a.http.Now().Add(time.Duration(maxInt(int(out.ExpiresIn), 3600)) * time.Second)
	return map[string]string{
		"refresh_token":    out.RefreshToken,
		"access_token":     out.Token,
		"token_expires_at": exp.UTC().Format(time.RFC3339),
	}, nil
}

func (a *Beds24Adapter) CapabilitiesMatrix(_ context.Context) []CapabilityItem {
	return []CapabilityItem{
		{Name: "token_refresh", Status: CapabilitySupported, Detail: "Exchanges long-lived refreshToken via GET /authentication/token for 24h access tokens"},
		{Name: "listing_discovery", Status: CapabilitySupported, Detail: "Discovers properties & rooms via GET /properties?includeAllRooms=true"},
		{Name: "availability_sync", Status: CapabilitySupported, Detail: "Pushes calendar availability and stop-sell/CTA/CTD via POST /inventory/rooms/calendar"},
		{Name: "rate_sync", Status: CapabilitySupported, Detail: "Pushes nightly price and stay restrictions via POST /inventory/rooms/calendar"},
		{Name: "reservation_import", Status: CapabilitySupported, Detail: "Pulls and hydrates bookings via GET /bookings"},
		{Name: "webhooks", Status: CapabilitySupported, Detail: "Receives configured-secret webhook triggers and re-fetches authoritative booking state"},
		{Name: "token_revoke", Status: CapabilityNotImplemented, Detail: "Beds24 API v2 does not expose remote refresh-token revocation; local disconnect clears encrypted secrets"},
	}
}

func (a *Beds24Adapter) DiscoverCapabilities(ctx context.Context) ([]string, error) {
	return capabilitySupportedNames(a.CapabilitiesMatrix(ctx)), nil
}

func (a *Beds24Adapter) RefreshCredentials(ctx context.Context, creds map[string]string) (map[string]string, error) {
	updated, _, err := a.RefreshToken(ctx, nil, creds)
	return updated, err
}

func (a *Beds24Adapter) RefreshToken(ctx context.Context, _ map[string]string, creds map[string]string) (map[string]string, *time.Time, error) {
	rt := strings.TrimSpace(creds["refresh_token"])
	invite := strings.TrimSpace(creds["invite_code"])
	if rt == "" && invite != "" {
		exchanged, err := a.ExchangeCode(ctx, invite, "")
		if err != nil {
			return nil, nil, err
		}
		merged := cloneStringMap(creds)
		for k, v := range exchanged {
			merged[k] = v
		}
		delete(merged, "invite_code")
		exp, _ := time.Parse(time.RFC3339, merged["token_expires_at"])
		return merged, &exp, nil
	}
	if rt == "" {
		return nil, nil, newProviderError(ErrAuthFailed, "beds24", "refresh_token", 401, "MISSING_REFRESH_TOKEN", "Beds24 refresh_token is required")
	}

	resp, err := a.http.Do(ctx, RequestSpec{
		ProviderCode: "beds24",
		Operation:    "refresh_token",
		Method:       http.MethodGet,
		Path:         "/authentication/token",
		Idempotent:   true,
		BuildHeaders: func(_ int, _ *url.URL, _ string, _ []byte) (http.Header, error) {
			h := make(http.Header)
			h.Set("refreshToken", rt)
			return h, nil
		},
	})
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, newProviderError(ErrAuthFailed, "beds24", "refresh_token", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Beds24 rejected refresh token")
	}
	var out struct {
		Token        string `json:"token"`
		ExpiresIn    int64  `json:"expiresIn"`
		RefreshToken string `json:"refreshToken,omitempty"`
	}
	if err := decodeJSONStrict("beds24", "refresh_token", resp.Body, &out); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(out.Token) == "" {
		return nil, nil, newProviderError(ErrInvalidResponse, "beds24", "refresh_token", resp.StatusCode, "EMPTY_TOKEN", "Beds24 returned empty access token")
	}
	ttl := time.Duration(out.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	exp := a.http.Now().Add(ttl).UTC()
	merged := cloneStringMap(creds)
	merged["access_token"] = out.Token
	merged["token_expires_at"] = exp.Format(time.RFC3339)
	if strings.TrimSpace(out.RefreshToken) != "" {
		merged["refresh_token"] = strings.TrimSpace(out.RefreshToken)
	}
	return merged, &exp, nil
}

func (a *Beds24Adapter) ensureAccessToken(ctx context.Context, publicCfg, creds map[string]string, forceRefresh bool) (string, map[string]string, *time.Time, error) {
	token := strings.TrimSpace(creds["access_token"])
	expStr := strings.TrimSpace(creds["token_expires_at"])
	var expPtr *time.Time
	valid := false
	if token != "" && !forceRefresh {
		if expStr != "" {
			if parsed, err := time.Parse(time.RFC3339, expStr); err == nil {
				u := parsed.UTC()
				expPtr = &u
				if u.After(a.http.Now().Add(beds24RefreshBuffer)) {
					valid = true
				}
			}
		} else {
			valid = true
		}
	}
	if valid {
		return token, creds, expPtr, nil
	}
	updated, exp, err := a.RefreshToken(ctx, publicCfg, creds)
	if err != nil {
		return "", nil, nil, err
	}
	return updated["access_token"], updated, exp, nil
}

func (a *Beds24Adapter) doAuthenticated(ctx context.Context, publicCfg, creds map[string]string, spec RequestSpec) (*httpResponseData, map[string]string, *time.Time, error) {
	token, workingCreds, expPtr, err := a.ensureAccessToken(ctx, publicCfg, creds, false)
	if err != nil {
		return nil, nil, nil, err
	}
	spec.ProviderCode = "beds24"
	origBuilder := spec.BuildHeaders
	spec.BuildHeaders = func(attempt int, reqURL *url.URL, canonicalQuery string, body []byte) (http.Header, error) {
		h := make(http.Header)
		if origBuilder != nil {
			extra, err := origBuilder(attempt, reqURL, canonicalQuery, body)
			if err != nil {
				return nil, err
			}
			for k, vals := range extra {
				h[k] = vals
			}
		}
		h.Set("token", token)
		return h, nil
	}
	resp, err := a.http.Do(ctx, spec)
	if err != nil && errors.Is(err, ErrAuthFailed) {
		updated2, exp2, rErr := a.RefreshToken(ctx, publicCfg, workingCreds)
		if rErr != nil {
			return nil, nil, nil, err
		}
		token = updated2["access_token"]
		workingCreds = updated2
		expPtr = exp2
		resp2, err2 := a.http.Do(ctx, spec)
		return resp2, workingCreds, expPtr, err2
	}
	return resp, workingCreds, expPtr, err
}

func (a *Beds24Adapter) VerifyConnection(ctx context.Context, publicCfg, creds map[string]string) error {
	_, err := a.VerifyConnectionDetailed(ctx, publicCfg, creds)
	return err
}

func (a *Beds24Adapter) VerifyConnectionDetailed(ctx context.Context, publicCfg, creds map[string]string) (ConnectionVerification, error) {
	resp, updatedCreds, expPtr, err := a.doAuthenticated(ctx, publicCfg, creds, RequestSpec{
		Operation:  "verify_connection",
		Method:     http.MethodGet,
		Path:       "/authentication/details",
		Idempotent: true,
	})
	if err != nil {
		return ConnectionVerification{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ConnectionVerification{}, newProviderError(ErrInvalidResponse, "beds24", "verify_connection", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "unexpected status verifying Beds24 token")
	}

	var details struct {
		ValidToken  bool `json:"validToken"`
		TokenOwner  any  `json:"token"`
		Permissions any  `json:"permissions"`
		OwnerID     any  `json:"ownerId"`
		PropertyID  any  `json:"propertyId"`
	}
	if err := decodeJSONStrict("beds24", "verify_connection", resp.Body, &details); err != nil {
		return ConnectionVerification{}, err
	}
	if !details.ValidToken {
		return ConnectionVerification{}, newProviderError(ErrAuthFailed, "beds24", "verify_connection", resp.StatusCode, "TOKEN_INVALID", "Beds24 reports token is invalid")
	}

	accountID := strings.TrimSpace(publicCfg["property_id"])
	if accountID == "" && details.OwnerID != nil {
		accountID = fmt.Sprintf("%v", details.OwnerID)
	}
	return ConnectionVerification{
		VerifiedAt:     a.http.Now(),
		AccountID:      accountID,
		AccountName:    "Beds24 Property " + accountID,
		Environment:    "production",
		UpdatedCreds:   updatedCreds,
		TokenExpiresAt: expPtr,
		Capabilities:   a.CapabilitiesMatrix(ctx),
	}, nil
}

func (a *Beds24Adapter) Disconnect(ctx context.Context, publicCfg, creds map[string]string) error {
	// Beds24 API v2 does not expose a token-revocation endpoint.
	return newProviderError(ErrNotImplemented, "beds24", "disconnect", 0, "NO_REMOTE_REVOKE", "Beds24 API v2 does not expose remote token revocation")
}

func (a *Beds24Adapter) DisconnectDetailed(ctx context.Context, publicCfg, creds map[string]string) (DisconnectOutcome, error) {
	return DisconnectOutcome{
		WebhookDeleteAttempted:   false,
		WebhookDeleteStatus:      CapabilityNotImplemented,
		WebhookDeleteDetail:      "Beds24 webhooks are managed manually in Beds24 UI; no remote webhook deletion endpoint.",
		WebhookDeleteErrorCode:   "MANUAL_WEBHOOK_CONFIG",
		TokenRevocationStatus:    CapabilityNotImplemented,
		TokenRevocationDetail:    "Beds24 API v2 does not expose remote token revocation.",
		TokenRevocationErrorCode: "NO_REMOTE_REVOKE",
	}, nil
}

func (a *Beds24Adapter) ListListings(ctx context.Context, publicCfg, creds map[string]string) ([]ExternalListing, error) {
	return a.DiscoverListingsDetailed(ctx, publicCfg, creds)
}

func (a *Beds24Adapter) DiscoverListingsDetailed(ctx context.Context, publicCfg, creds map[string]string) ([]ExternalListing, error) {
	targetPropID := strings.TrimSpace(publicCfg["property_id"])
	out := make([]ExternalListing, 0, 8)
	for page := 1; page <= 25; page++ {
		q := url.Values{}
		q.Set("includeAllRooms", "true")
		q.Set("page", strconv.Itoa(page))
		if targetPropID != "" {
			q.Set("id", targetPropID)
		}

		resp, _, _, err := a.doAuthenticated(ctx, publicCfg, creds, RequestSpec{
			Operation:  "list_listings",
			Method:     http.MethodGet,
			Path:       "/properties",
			Query:      q,
			Idempotent: true,
		})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, newProviderError(ErrInvalidResponse, "beds24", "list_listings", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed listing Beds24 properties")
		}

		var payload struct {
			Success bool `json:"success"`
			Pages   struct {
				NextPageExists bool `json:"nextPageExists"`
			} `json:"pages"`
			Data []struct {
				ID        json.RawMessage `json:"id"`
				Name      string          `json:"name"`
				Currency  string          `json:"currency"`
				RoomTypes []struct {
					ID   json.RawMessage `json:"id"`
					Name string          `json:"name"`
					Qty  int             `json:"qty"`
				} `json:"roomTypes"`
			} `json:"data"`
		}
		if err := decodeJSONStrict("beds24", "list_listings", resp.Body, &payload); err != nil {
			return nil, err
		}
		for _, prop := range payload.Data {
			pid := flexibleID(prop.ID)
			for _, rt := range prop.RoomTypes {
				rid := flexibleID(rt.ID)
				if rid == "" {
					continue
				}
				name := strings.TrimSpace(rt.Name)
				if prop.Name != "" && name != "" {
					name = prop.Name + " — " + name
				} else if name == "" {
					name = "Beds24 Room " + rid
				}
				out = append(out, ExternalListing{
					ExternalID:         rid,
					ExternalPropertyID: pid,
					Name:               name,
					Channel:            "beds24",
					Currency:           strings.ToUpper(strings.TrimSpace(prop.Currency)),
					SupportsPush:       true,
				})
			}
		}
		if !payload.Pages.NextPageExists || len(payload.Data) == 0 {
			break
		}
	}
	return out, nil
}

func (a *Beds24Adapter) SyncAvailability(ctx context.Context, publicCfg, creds map[string]string, raw json.RawMessage) error {
	var req AvailabilityPushRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return newProviderError(ErrInvalidInput, "beds24", "push_availability", 0, "INVALID_PAYLOAD", "invalid availability payload")
	}
	return a.PushAvailability(ctx, publicCfg, creds, req)
}

func (a *Beds24Adapter) PushAvailability(ctx context.Context, publicCfg, creds map[string]string, req AvailabilityPushRequest) error {
	if strings.TrimSpace(req.ExternalListingID) == "" || len(req.Days) == 0 {
		return newProviderError(ErrInvalidInput, "beds24", "push_availability", 0, "EMPTY_REQUEST", "room ID and days are required")
	}
	calendar := make([]map[string]any, 0, len(req.Days))
	for _, d := range req.Days {
		entry := map[string]any{
			"from":     d.Date,
			"to":       d.Date,
			"numAvail": d.AvailableUnits,
		}
		if d.StopSell != nil && *d.StopSell {
			entry["override"] = "blackout"
		}
		if d.ClosedToArrival != nil {
			entry["closedArrival"] = *d.ClosedToArrival
		}
		if d.ClosedToDeparture != nil {
			entry["closedDeparture"] = *d.ClosedToDeparture
		}
		calendar = append(calendar, entry)
	}
	return a.postCalendarBatch(ctx, publicCfg, creds, "push_availability", req.ExternalListingID, calendar)
}

func (a *Beds24Adapter) SyncRates(ctx context.Context, publicCfg, creds map[string]string, raw json.RawMessage) error {
	var req RatePushRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return newProviderError(ErrInvalidInput, "beds24", "push_rates", 0, "INVALID_PAYLOAD", "invalid rates payload")
	}
	return a.PushRates(ctx, publicCfg, creds, req)
}

func (a *Beds24Adapter) PushRates(ctx context.Context, publicCfg, creds map[string]string, req RatePushRequest) error {
	if strings.TrimSpace(req.ExternalListingID) == "" || len(req.Days) == 0 {
		return newProviderError(ErrInvalidInput, "beds24", "push_rates", 0, "EMPTY_REQUEST", "room ID and days are required")
	}
	calendar := make([]map[string]any, 0, len(req.Days))
	for _, d := range req.Days {
		num, err := FormatMinorToNumber(d.PriceMinor, d.Currency)
		if err != nil {
			return err
		}
		entry := map[string]any{
			"from":   d.Date,
			"to":     d.Date,
			"price1": num,
		}
		if d.MinStay > 0 {
			entry["minStay"] = d.MinStay
		}
		if d.MaxStay != nil && *d.MaxStay > 0 {
			entry["maxStay"] = *d.MaxStay
		}
		if d.StopSell != nil && *d.StopSell {
			entry["override"] = "blackout"
		}
		if d.ClosedToArrival != nil {
			entry["closedArrival"] = *d.ClosedToArrival
		}
		if d.ClosedToDeparture != nil {
			entry["closedDeparture"] = *d.ClosedToDeparture
		}
		calendar = append(calendar, entry)
	}
	return a.postCalendarBatch(ctx, publicCfg, creds, "push_rates", req.ExternalListingID, calendar)
}

func (a *Beds24Adapter) postCalendarBatch(ctx context.Context, publicCfg, creds map[string]string, op, roomID string, calendar []map[string]any) error {
	var roomIDVal any = roomID
	if n, err := strconv.ParseInt(strings.TrimSpace(roomID), 10, 64); err == nil {
		roomIDVal = n
	}
	payload := []map[string]any{
		{
			"roomId":   roomIDVal,
			"calendar": calendar,
		},
	}
	body, _ := json.Marshal(payload)
	resp, _, _, err := a.doAuthenticated(ctx, publicCfg, creds, RequestSpec{
		Operation:  op,
		Method:     http.MethodPost,
		Path:       "/inventory/rooms/calendar",
		Body:       body,
		Idempotent: true,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newProviderError(ErrInvalidResponse, "beds24", op, resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Beds24 calendar update failed")
	}
	return nil
}

func (a *Beds24Adapter) ImportReservations(ctx context.Context, publicCfg, creds map[string]string, since time.Time) ([]json.RawMessage, error) {
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

func (a *Beds24Adapter) PullReservations(ctx context.Context, publicCfg, creds map[string]string, since time.Time, cursor string) ([]ExternalReservation, string, error) {
	startPage := 1
	if p, err := strconv.Atoi(strings.TrimSpace(cursor)); err == nil && p > 0 {
		startPage = p
	}
	out := make([]ExternalReservation, 0, 32)
	propID := strings.TrimSpace(publicCfg["property_id"])
	for page := startPage; page < startPage+15; page++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("includeInvoiceItems", "true")
		if propID != "" {
			q.Set("propertyId", propID)
		}
		if !since.IsZero() {
			q.Set("modifiedFrom", since.UTC().Format(time.RFC3339))
		}
		resp, _, _, err := a.doAuthenticated(ctx, publicCfg, creds, RequestSpec{
			Operation:  "pull_reservations",
			Method:     http.MethodGet,
			Path:       "/bookings",
			Query:      q,
			Idempotent: true,
		})
		if err != nil {
			return nil, "", err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, "", newProviderError(ErrInvalidResponse, "beds24", "pull_reservations", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed pulling Beds24 bookings")
		}
		bookings, nextExists, err := a.parseBookingsPage(resp.Body, propID)
		if err != nil {
			return nil, "", err
		}
		out = append(out, bookings...)
		if !nextExists {
			break
		}
	}
	return out, "", nil
}

func (a *Beds24Adapter) HydrateReservation(ctx context.Context, publicCfg, creds map[string]string, externalPropertyID, externalReservationID string) (ExternalReservation, error) {
	q := url.Values{}
	q.Set("id", strings.TrimSpace(externalReservationID))
	if pid := strings.TrimSpace(externalPropertyID); pid != "" {
		q.Set("propertyId", pid)
	}
	resp, _, _, err := a.doAuthenticated(ctx, publicCfg, creds, RequestSpec{
		Operation:  "hydrate_reservation",
		Method:     http.MethodGet,
		Path:       "/bookings",
		Query:      q,
		Idempotent: true,
	})
	if err != nil {
		return ExternalReservation{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ExternalReservation{}, newProviderError(ErrInvalidResponse, "beds24", "hydrate_reservation", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed fetching Beds24 booking detail")
	}
	items, _, err := a.parseBookingsPage(resp.Body, externalPropertyID)
	if err != nil {
		return ExternalReservation{}, err
	}
	if len(items) == 0 {
		return ExternalReservation{}, newProviderError(ErrInvalidResponse, "beds24", "hydrate_reservation", resp.StatusCode, "BOOKING_NOT_FOUND", "Beds24 booking not returned")
	}
	return items[0], nil
}

func (a *Beds24Adapter) parseBookingsPage(body []byte, defaultPropID string) ([]ExternalReservation, bool, error) {
	var payload struct {
		Pages struct {
			NextPageExists bool `json:"nextPageExists"`
		} `json:"pages"`
		Data []struct {
			ID           json.RawMessage `json:"id"`
			PropertyID   json.RawMessage `json:"propertyId"`
			RoomID       json.RawMessage `json:"roomId"`
			Status       string          `json:"status"`
			Channel      string          `json:"channel"`
			Referer      string          `json:"referer"`
			Arrival      string          `json:"arrival"`
			Departure    string          `json:"departure"`
			Currency     string          `json:"currency"`
			Price        json.RawMessage `json:"price"`
			NumAdult     int             `json:"numAdult"`
			NumChild     int             `json:"numChild"`
			FirstName    string          `json:"firstName"`
			LastName     string          `json:"lastName"`
			Email        string          `json:"email"`
			Mobile       string          `json:"mobile"`
			Phone        string          `json:"phone"`
			ModifiedTime string          `json:"modifiedTime"`
		} `json:"data"`
	}
	if err := decodeJSONStrict("beds24", "parse_bookings", body, &payload); err != nil {
		return nil, false, err
	}
	out := make([]ExternalReservation, 0, len(payload.Data))
	for _, b := range payload.Data {
		bid := flexibleID(b.ID)
		if bid == "" {
			continue
		}
		pid := flexibleID(b.PropertyID)
		if pid == "" {
			pid = defaultPropID
		}
		cur := strings.ToUpper(strings.TrimSpace(b.Currency))
		if cur == "" {
			cur = "GBP"
		}
		phone := strings.TrimSpace(b.Mobile)
		if phone == "" {
			phone = strings.TrimSpace(b.Phone)
		}
		guestName := strings.TrimSpace(strings.TrimSpace(b.FirstName) + " " + strings.TrimSpace(b.LastName))
		if guestName == "" {
			guestName = "Beds24 Guest"
		}
		src := b.Channel
		if src == "" {
			src = b.Referer
		}
		modAt := parseFlexibleTimestamp(b.ModifiedTime)
		rev := strings.TrimSpace(b.ModifiedTime)
		if rev == "" {
			rev = normalizeReservationStatus(b.Status)
		}
		er := ExternalReservation{
			ProviderCode:          "beds24",
			ExternalReservationID: bid,
			ExternalRevision:      rev,
			ExternalPropertyID:    pid,
			ExternalListingID:     flexibleID(b.RoomID),
			Status:                normalizeReservationStatus(b.Status),
			SourceChannel:         normalizeSourceChannel(src, "beds24"),
			CheckIn:               parseDateOnlyString(b.Arrival),
			CheckOut:              parseDateOnlyString(b.Departure),
			Currency:              cur,
			TotalMinor:            flexibleMoneyMinor(b.Price, cur),
			Adults:                maxInt(b.NumAdult, 1),
			Children:              maxInt(b.NumChild, 0),
			GuestName:             guestName,
			GuestEmail:            strings.TrimSpace(b.Email),
			GuestPhone:            phone,
			ProviderUpdatedAt:     modAt,
		}
		er.PayloadHash = computePayloadDigest(er)
		out = append(out, er)
	}
	return out, payload.Pages.NextPageExists, nil
}

func (a *Beds24Adapter) AcknowledgeRevision(_ context.Context, _, _ map[string]string, _ string) error {
	return nil
}

func (a *Beds24Adapter) HandleWebhook(ctx context.Context, headers map[string]string, body []byte) error {
	return newProviderError(ErrWebhookVerificationFailed, "beds24", "webhook", 401, "CONNECTION_CONTEXT_REQUIRED", "connection credentials required to verify webhook")
}

func (a *Beds24Adapter) VerifyWebhook(_ context.Context, publicCfg, creds map[string]string, headers map[string]string, body []byte, _ NonceChecker) (WebhookEvent, error) {
	if err := VerifySharedSecretWebhook("beds24", headers, body, []string{creds["webhook_secret"]}); err != nil {
		return WebhookEvent{}, err
	}
	var payload struct {
		Action     string `json:"action"`
		BookingID  any    `json:"bookId"`
		ID         any    `json:"id"`
		PropertyID any    `json:"propertyId"`
		Booking    *struct {
			ID         any    `json:"id"`
			PropertyID any    `json:"propertyId"`
			RoomID     any    `json:"roomId"`
			Status     string `json:"status"`
			Modified   string `json:"modifiedTime"`
		} `json:"booking"`
	}
	_ = json.Unmarshal(body, &payload)

	bookingID := ""
	propID := strings.TrimSpace(publicCfg["property_id"])
	roomID := ""
	action := "reservation_sync"
	rev := ""
	if payload.Booking != nil {
		if payload.Booking.ID != nil {
			bookingID = fmt.Sprintf("%v", payload.Booking.ID)
		}
		if payload.Booking.PropertyID != nil {
			propID = fmt.Sprintf("%v", payload.Booking.PropertyID)
		}
		if payload.Booking.RoomID != nil {
			roomID = fmt.Sprintf("%v", payload.Booking.RoomID)
		}
		if normalizeReservationStatus(payload.Booking.Status) == "CANCELLED" {
			action = "reservation_cancel"
		}
		rev = strings.TrimSpace(payload.Booking.Modified)
	}
	if bookingID == "" {
		if payload.BookingID != nil {
			bookingID = fmt.Sprintf("%v", payload.BookingID)
		} else if payload.ID != nil {
			bookingID = fmt.Sprintf("%v", payload.ID)
		}
	}
	if bookingID == "<nil>" {
		bookingID = ""
	}
	if propID == "<nil>" {
		propID = ""
	}
	if roomID == "<nil>" {
		roomID = ""
	}
	eventType := strings.TrimSpace(payload.Action)
	if eventType == "" {
		eventType = "booking_update"
	}
	extEventID := computePayloadDigest(map[string]string{
		"booking_id": bookingID,
		"rev":        rev,
		"body_sha":   SHA256Hex(body),
	})[:32]

	return WebhookEvent{
		ProviderCode:          "beds24",
		ExternalEventID:       extEventID,
		EventType:             eventType,
		Action:                action,
		ExternalPropertyID:    propID,
		ExternalListingID:     roomID,
		ExternalReservationID: bookingID,
		ExternalRevision:      rev,
		SignatureVerified:     true,
	}, nil
}

func (a *Beds24Adapter) RegisterWebhook(_ context.Context, _, _ map[string]string, _ string) (string, error) {
	return "", newProviderError(ErrNotImplemented, "beds24", "register_webhook", 0, "MANUAL_WEBHOOK_CONFIG", "Beds24 webhooks are configured in Beds24 Property → Booking Webhook UI")
}

func (a *Beds24Adapter) DeleteWebhook(_ context.Context, _, _ map[string]string, _ string) error {
	return newProviderError(ErrNotImplemented, "beds24", "delete_webhook", 0, "MANUAL_WEBHOOK_CONFIG", "Beds24 webhooks are managed in Beds24 UI")
}
