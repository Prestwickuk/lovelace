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

type ChannexAdapter struct {
	http *SafeHTTPClient
}

func NewChannexAdapter(cfg AdapterConfig) *ChannexAdapter {
	return &ChannexAdapter{http: NewSafeHTTPClient(cfg)}
}

func (a *ChannexAdapter) ProviderCode() string        { return "channex" }
func (a *ChannexAdapter) HasLiveImplementation() bool { return true }

func (a *ChannexAdapter) AuthorizeURL(_ context.Context, _, _ string) (string, error) {
	return legacyAuthorizeError("channex")
}

func (a *ChannexAdapter) ExchangeCode(_ context.Context, _, _ string) (map[string]string, error) {
	return legacyExchangeError("channex")
}

func (a *ChannexAdapter) CapabilitiesMatrix(_ context.Context) []CapabilityItem {
	return []CapabilityItem{
		{Name: "listing_discovery", Status: CapabilitySupported, Detail: "Discovers properties, room_types, and rate_plans via GET /api/v1/{properties,room_types,rate_plans}"},
		{Name: "availability_sync", Status: CapabilitySupported, Detail: "Pushes room-type availability ranges via POST /api/v1/availability"},
		{Name: "rate_sync", Status: CapabilitySupported, Detail: "Pushes rate-plan daily rates & restrictions (min_stay, max_stay, stop_sell, CTA, CTD) via POST /api/v1/restrictions"},
		{Name: "reservation_import", Status: CapabilitySupported, Detail: "Pulls booking revision feed via GET /api/v1/booking_revisions/feed and acknowledges committed revisions via POST /api/v1/booking_revisions/{id}/ack"},
		{Name: "webhooks", Status: CapabilitySupported, Detail: "Verifies x-hmac-timestamp + x-hmac-signature-256 ({timestamp}.{raw_body}) with multi-key rotation support"},
		{Name: "token_revoke", Status: CapabilityNotImplemented, Detail: "Channex API keys are managed in Channex Profile/Organisation settings; registered webhooks can be deleted on disconnect"},
	}
}

func (a *ChannexAdapter) DiscoverCapabilities(ctx context.Context) ([]string, error) {
	return capabilitySupportedNames(a.CapabilitiesMatrix(ctx)), nil
}

func (a *ChannexAdapter) RefreshCredentials(_ context.Context, creds map[string]string) (map[string]string, error) {
	return cloneStringMap(creds), nil
}

func (a *ChannexAdapter) RefreshToken(_ context.Context, _ map[string]string, creds map[string]string) (map[string]string, *time.Time, error) {
	return cloneStringMap(creds), nil, nil
}

func (a *ChannexAdapter) endpointKey(publicCfg map[string]string) (string, string) {
	env := strings.ToLower(strings.TrimSpace(publicCfg["environment"]))
	if env == "staging" || env == "sandbox" {
		return "channex:staging", "staging"
	}
	return "channex", "production"
}

func (a *ChannexAdapter) doAPIKey(ctx context.Context, publicCfg, creds map[string]string, spec RequestSpec) (*httpResponseData, error) {
	apiKey := strings.TrimSpace(creds["user_api_key"])
	if apiKey == "" {
		return nil, newProviderError(ErrAuthFailed, "channex", spec.Operation, 401, "API_KEY_MISSING", "Channex user_api_key is required")
	}
	epKey, _ := a.endpointKey(publicCfg)
	spec.ProviderCode = "channex"
	spec.EndpointKey = epKey
	spec.BuildHeaders = func(_ int, _ *url.URL, _ string, _ []byte) (http.Header, error) {
		h := make(http.Header)
		h.Set("user-api-key", apiKey)
		return h, nil
	}
	return a.http.Do(ctx, spec)
}

func (a *ChannexAdapter) VerifyConnection(ctx context.Context, publicCfg, creds map[string]string) error {
	_, err := a.VerifyConnectionDetailed(ctx, publicCfg, creds)
	return err
}

func (a *ChannexAdapter) VerifyConnectionDetailed(ctx context.Context, publicCfg, creds map[string]string) (ConnectionVerification, error) {
	_, envName := a.endpointKey(publicCfg)
	q := url.Values{}
	q.Set("pagination[page]", "1")
	q.Set("pagination[limit]", "10")
	resp, err := a.doAPIKey(ctx, publicCfg, creds, RequestSpec{
		Operation:  "verify_connection",
		Method:     http.MethodGet,
		Path:       "/properties",
		Query:      q,
		Idempotent: true,
	})
	if err != nil {
		return ConnectionVerification{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ConnectionVerification{}, newProviderError(ErrInvalidResponse, "channex", "verify_connection", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Channex property query failed")
	}
	var out struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Title string `json:"title"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := decodeJSONStrict("channex", "verify_connection", resp.Body, &out); err != nil {
		return ConnectionVerification{}, err
	}
	accID := strings.TrimSpace(publicCfg["property_id"])
	accName := "Channex (" + envName + ")"
	if accID == "" && len(out.Data) > 0 {
		accID = out.Data[0].ID
		if out.Data[0].Attributes.Title != "" {
			accName = out.Data[0].Attributes.Title
		}
	}
	return ConnectionVerification{
		VerifiedAt:   a.http.Now(),
		AccountID:    accID,
		AccountName:  accName,
		Environment:  envName,
		Capabilities: a.CapabilitiesMatrix(ctx),
	}, nil
}

func (a *ChannexAdapter) Disconnect(ctx context.Context, publicCfg, creds map[string]string) error {
	if whID := strings.TrimSpace(publicCfg["webhook_id"]); whID != "" {
		if err := a.DeleteWebhook(ctx, publicCfg, creds, whID); err == nil {
			return nil
		}
	}
	return newProviderError(ErrNotImplemented, "channex", "disconnect", 0, "NO_REMOTE_REVOKE", "Channex user API keys are revoked in Channex Dashboard")
}

func (a *ChannexAdapter) ListListings(ctx context.Context, publicCfg, creds map[string]string) ([]ExternalListing, error) {
	return a.DiscoverListingsDetailed(ctx, publicCfg, creds)
}

func (a *ChannexAdapter) DiscoverListingsDetailed(ctx context.Context, publicCfg, creds map[string]string) ([]ExternalListing, error) {
	propIDFilter := strings.TrimSpace(publicCfg["property_id"])

	type propMeta struct {
		ID       string
		Title    string
		Currency string
	}
	var props []propMeta
	for page := 1; page <= 10; page++ {
		q := url.Values{}
		q.Set("pagination[page]", strconv.Itoa(page))
		q.Set("pagination[limit]", "50")
		resp, err := a.doAPIKey(ctx, publicCfg, creds, RequestSpec{
			Operation:  "list_properties",
			Method:     http.MethodGet,
			Path:       "/properties",
			Query:      q,
			Idempotent: true,
		})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, newProviderError(ErrInvalidResponse, "channex", "list_properties", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed listing Channex properties")
		}
		var out struct {
			Data []struct {
				ID         string `json:"id"`
				Attributes struct {
					Title    string `json:"title"`
					Currency string `json:"currency"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := decodeJSONStrict("channex", "list_properties", resp.Body, &out); err != nil {
			return nil, err
		}
		for _, p := range out.Data {
			if propIDFilter != "" && p.ID != propIDFilter {
				continue
			}
			props = append(props, propMeta{ID: p.ID, Title: p.Attributes.Title, Currency: strings.ToUpper(p.Attributes.Currency)})
		}
		if len(out.Data) < 50 {
			break
		}
	}

	out := make([]ExternalListing, 0, 16)
	for _, prop := range props {
		q := url.Values{}
		q.Set("filter[property_id]", prop.ID)
		q.Set("pagination[page]", "1")
		q.Set("pagination[limit]", "100")

		rtResp, err := a.doAPIKey(ctx, publicCfg, creds, RequestSpec{
			Operation:  "list_room_types",
			Method:     http.MethodGet,
			Path:       "/room_types",
			Query:      q,
			Idempotent: true,
		})
		if err != nil {
			return nil, err
		}
		if rtResp.StatusCode < 200 || rtResp.StatusCode >= 300 {
			return nil, newProviderError(ErrInvalidResponse, "channex", "list_room_types", rtResp.StatusCode, fmt.Sprintf("HTTP_%d", rtResp.StatusCode), "failed listing Channex room types")
		}
		var rts struct {
			Data []struct {
				ID         string `json:"id"`
				Attributes struct {
					Title string `json:"title"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if err := decodeJSONStrict("channex", "list_room_types", rtResp.Body, &rts); err != nil {
			return nil, err
		}

		rpResp, err := a.doAPIKey(ctx, publicCfg, creds, RequestSpec{
			Operation:  "list_rate_plans",
			Method:     http.MethodGet,
			Path:       "/rate_plans",
			Query:      q,
			Idempotent: true,
		})
		ratePlansByRoom := make(map[string]string)
		if err == nil && rpResp.StatusCode >= 200 && rpResp.StatusCode < 300 {
			var rps struct {
				Data []struct {
					ID         string `json:"id"`
					Attributes struct {
						Title string `json:"title"`
					} `json:"attributes"`
					Relationships struct {
						RoomType struct {
							Data struct {
								ID string `json:"id"`
							} `json:"data"`
						} `json:"room_type"`
					} `json:"relationships"`
				} `json:"data"`
			}
			if json.Unmarshal(rpResp.Body, &rps) == nil {
				for _, rp := range rps.Data {
					rtID := rp.Relationships.RoomType.Data.ID
					if rtID != "" && ratePlansByRoom[rtID] == "" {
						ratePlansByRoom[rtID] = rp.ID
					}
				}
			}
		}

		for _, rt := range rts.Data {
			name := strings.TrimSpace(rt.Attributes.Title)
			if prop.Title != "" && name != "" {
				name = prop.Title + " — " + name
			}
			rpID := ratePlansByRoom[rt.ID]
			if rpID != "" {
				name += " [rp:" + rpID + "]"
			}
			out = append(out, ExternalListing{
				ExternalID:         rt.ID,
				ExternalPropertyID: prop.ID,
				ExternalRatePlanID: rpID,
				Name:               name,
				Channel:            "channex",
				Currency:           prop.Currency,
				SupportsPush:       true,
			})
		}
	}
	return out, nil
}

func (a *ChannexAdapter) SyncAvailability(ctx context.Context, publicCfg, creds map[string]string, raw json.RawMessage) error {
	var req AvailabilityPushRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return newProviderError(ErrInvalidInput, "channex", "push_availability", 0, "INVALID_PAYLOAD", "invalid availability payload")
	}
	return a.PushAvailability(ctx, publicCfg, creds, req)
}

func (a *ChannexAdapter) PushAvailability(ctx context.Context, publicCfg, creds map[string]string, req AvailabilityPushRequest) error {
	propID := strings.TrimSpace(req.ExternalPropertyID)
	if propID == "" {
		propID = strings.TrimSpace(publicCfg["property_id"])
	}
	rtID := strings.TrimSpace(req.ExternalListingID)
	if propID == "" || rtID == "" || len(req.Days) == 0 {
		return newProviderError(ErrInvalidInput, "channex", "push_availability", 0, "EMPTY_REQUEST", "property_id, room_type_id, and days are required")
	}
	values := make([]map[string]any, 0, len(req.Days))
	for _, d := range req.Days {
		avail := d.AvailableUnits
		if d.StopSell != nil && *d.StopSell {
			avail = 0
		}
		values = append(values, map[string]any{
			"property_id":  propID,
			"room_type_id": rtID,
			"date_from":    d.Date,
			"date_to":      d.Date,
			"availability": avail,
		})
	}
	body, _ := json.Marshal(map[string]any{"values": values})
	resp, err := a.doAPIKey(ctx, publicCfg, creds, RequestSpec{
		Operation:  "push_availability",
		Method:     http.MethodPost,
		Path:       "/availability",
		Body:       body,
		Idempotent: true,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newProviderError(ErrInvalidResponse, "channex", "push_availability", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Channex /availability update failed")
	}
	return nil
}

func (a *ChannexAdapter) SyncRates(ctx context.Context, publicCfg, creds map[string]string, raw json.RawMessage) error {
	var req RatePushRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return newProviderError(ErrInvalidInput, "channex", "push_rates", 0, "INVALID_PAYLOAD", "invalid rates payload")
	}
	return a.PushRates(ctx, publicCfg, creds, req)
}

func (a *ChannexAdapter) PushRates(ctx context.Context, publicCfg, creds map[string]string, req RatePushRequest) error {
	propID := strings.TrimSpace(req.ExternalPropertyID)
	if propID == "" {
		propID = strings.TrimSpace(publicCfg["property_id"])
	}
	rpID := strings.TrimSpace(req.ExternalRatePlanID)
	if rpID == "" {
		rpID = strings.TrimSpace(req.ExternalListingID)
	}
	if propID == "" || rpID == "" || len(req.Days) == 0 {
		return newProviderError(ErrInvalidInput, "channex", "push_rates", 0, "EMPTY_REQUEST", "property_id, rate_plan_id, and days are required")
	}
	values := make([]map[string]any, 0, len(req.Days))
	for _, d := range req.Days {
		decRate, err := FormatMinorToDecimal(d.PriceMinor, d.Currency)
		if err != nil {
			return err
		}
		item := map[string]any{
			"property_id":  propID,
			"rate_plan_id": rpID,
			"date_from":    d.Date,
			"date_to":      d.Date,
			"rate":         decRate,
		}
		if d.MinStay > 0 {
			item["min_stay_arrival"] = d.MinStay
		}
		if d.MaxStay != nil && *d.MaxStay > 0 {
			item["max_stay"] = *d.MaxStay
		}
		if d.StopSell != nil {
			item["stop_sell"] = *d.StopSell
		}
		if d.ClosedToArrival != nil {
			item["closed_to_arrival"] = *d.ClosedToArrival
		}
		if d.ClosedToDeparture != nil {
			item["closed_to_departure"] = *d.ClosedToDeparture
		}
		values = append(values, item)
	}
	body, _ := json.Marshal(map[string]any{"values": values})
	resp, err := a.doAPIKey(ctx, publicCfg, creds, RequestSpec{
		Operation:  "push_rates",
		Method:     http.MethodPost,
		Path:       "/restrictions",
		Body:       body,
		Idempotent: true,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newProviderError(ErrInvalidResponse, "channex", "push_rates", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Channex /restrictions update failed")
	}
	return nil
}

func (a *ChannexAdapter) ImportReservations(ctx context.Context, publicCfg, creds map[string]string, since time.Time) ([]json.RawMessage, error) {
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

func (a *ChannexAdapter) PullReservations(ctx context.Context, publicCfg, creds map[string]string, _ time.Time, cursor string) ([]ExternalReservation, string, error) {
	startPage := 1
	if p, err := strconv.Atoi(strings.TrimSpace(cursor)); err == nil && p > 0 {
		startPage = p
	}
	out := make([]ExternalReservation, 0, 32)
	propFilter := strings.TrimSpace(publicCfg["property_id"])
	for page := startPage; page < startPage+10; page++ {
		q := url.Values{}
		q.Set("pagination[page]", strconv.Itoa(page))
		q.Set("pagination[limit]", "50")
		if propFilter != "" {
			q.Set("filter[property_id]", propFilter)
		}
		resp, err := a.doAPIKey(ctx, publicCfg, creds, RequestSpec{
			Operation:  "pull_reservations",
			Method:     http.MethodGet,
			Path:       "/booking_revisions/feed",
			Query:      q,
			Idempotent: true,
		})
		if err != nil {
			return nil, "", err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, "", newProviderError(ErrInvalidResponse, "channex", "pull_reservations", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed polling Channex booking revision feed")
		}
		var payload struct {
			Data []channexRevisionItem `json:"data"`
		}
		if err := decodeJSONStrict("channex", "pull_reservations", resp.Body, &payload); err != nil {
			return nil, "", err
		}
		for _, item := range payload.Data {
			if er, ok := item.toExternal(); ok {
				out = append(out, er)
			}
		}
		if len(payload.Data) < 50 {
			break
		}
	}
	return out, "", nil
}

func (a *ChannexAdapter) HydrateReservation(ctx context.Context, publicCfg, creds map[string]string, _ string, externalReservationID string) (ExternalReservation, error) {
	id := strings.TrimSpace(externalReservationID)
	if id == "" {
		return ExternalReservation{}, newProviderError(ErrInvalidInput, "channex", "hydrate_reservation", 0, "MISSING_ID", "revision or booking ID is required")
	}
	resp, err := a.doAPIKey(ctx, publicCfg, creds, RequestSpec{
		Operation:  "hydrate_reservation",
		Method:     http.MethodGet,
		Path:       "/booking_revisions/" + url.PathEscape(id),
		Idempotent: true,
	})
	if err != nil {
		return ExternalReservation{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ExternalReservation{}, newProviderError(ErrInvalidResponse, "channex", "hydrate_reservation", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "failed fetching Channex booking revision")
	}
	var payload struct {
		Data channexRevisionItem `json:"data"`
	}
	if err := decodeJSONStrict("channex", "hydrate_reservation", resp.Body, &payload); err != nil {
		return ExternalReservation{}, err
	}
	er, ok := payload.Data.toExternal()
	if !ok {
		return ExternalReservation{}, newProviderError(ErrInvalidResponse, "channex", "hydrate_reservation", resp.StatusCode, "INVALID_REVISION", "Channex revision payload invalid")
	}
	return er, nil
}

func (a *ChannexAdapter) AcknowledgeRevision(ctx context.Context, publicCfg, creds map[string]string, revisionAckID string) error {
	revID := strings.TrimSpace(revisionAckID)
	if revID == "" {
		return nil
	}
	resp, err := a.doAPIKey(ctx, publicCfg, creds, RequestSpec{
		Operation:  "ack_revision",
		Method:     http.MethodPost,
		Path:       "/booking_revisions/" + url.PathEscape(revID) + "/ack",
		Idempotent: true,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newProviderError(ErrInvalidResponse, "channex", "ack_revision", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Channex booking revision acknowledgement failed")
	}
	return nil
}

type channexRevisionItem struct {
	ID         string `json:"id"`
	Attributes struct {
		ID          string          `json:"id"`
		RevisionID  string          `json:"revision_id"`
		BookingID   string          `json:"booking_id"`
		PropertyID  string          `json:"property_id"`
		Status      string          `json:"status"`
		OTACode     string          `json:"ota_name"`
		ArrivalDate string          `json:"arrival_date"`
		Departure   string          `json:"departure_date"`
		Currency    string          `json:"currency"`
		Amount      json.RawMessage `json:"amount"`
		UpdatedAt   string          `json:"inserted_at"`
		Customer    struct {
			Name    string `json:"name"`
			Surname string `json:"surname"`
			Mail    string `json:"mail"`
			Phone   string `json:"phone"`
		} `json:"customer"`
		Rooms []struct {
			RoomTypeID string `json:"room_type_id"`
			RatePlanID string `json:"rate_plan_id"`
			Occupancy  struct {
				Adults   int `json:"adults"`
				Children int `json:"children"`
				Infants  int `json:"infants"`
			} `json:"occupancy"`
		} `json:"rooms"`
	} `json:"attributes"`
}

func (r channexRevisionItem) toExternal() (ExternalReservation, bool) {
	revID := strings.TrimSpace(r.ID)
	if revID == "" {
		revID = strings.TrimSpace(r.Attributes.RevisionID)
	}
	bookingID := strings.TrimSpace(r.Attributes.BookingID)
	if bookingID == "" {
		bookingID = strings.TrimSpace(r.Attributes.ID)
	}
	if bookingID == "" {
		return ExternalReservation{}, false
	}
	cur := strings.ToUpper(strings.TrimSpace(r.Attributes.Currency))
	if cur == "" {
		cur = "GBP"
	}
	var rtID, rpID string
	adults, children, infants := 1, 0, 0
	if len(r.Attributes.Rooms) > 0 {
		rtID = strings.TrimSpace(r.Attributes.Rooms[0].RoomTypeID)
		rpID = strings.TrimSpace(r.Attributes.Rooms[0].RatePlanID)
		if r.Attributes.Rooms[0].Occupancy.Adults > 0 {
			adults = r.Attributes.Rooms[0].Occupancy.Adults
		}
		children = maxInt(r.Attributes.Rooms[0].Occupancy.Children, 0)
		infants = maxInt(r.Attributes.Rooms[0].Occupancy.Infants, 0)
	}
	guestName := strings.TrimSpace(strings.TrimSpace(r.Attributes.Customer.Name) + " " + strings.TrimSpace(r.Attributes.Customer.Surname))
	if guestName == "" {
		guestName = "Channex Guest"
	}
	status := normalizeReservationStatus(r.Attributes.Status)
	rev := strings.TrimSpace(r.Attributes.UpdatedAt)
	if rev == "" {
		rev = revID
	}
	er := ExternalReservation{
		ProviderCode:          "channex",
		ExternalReservationID: bookingID,
		ExternalRevision:      rev,
		RevisionAckID:         revID,
		ExternalPropertyID:    strings.TrimSpace(r.Attributes.PropertyID),
		ExternalListingID:     rtID,
		ExternalRatePlanID:    rpID,
		Status:                status,
		SourceChannel:         normalizeSourceChannel(r.Attributes.OTACode, "channex"),
		CheckIn:               parseDateOnlyString(r.Attributes.ArrivalDate),
		CheckOut:              parseDateOnlyString(r.Attributes.Departure),
		Currency:              cur,
		TotalMinor:            flexibleMoneyMinor(r.Attributes.Amount, cur),
		Adults:                adults,
		Children:              children,
		Infants:               infants,
		GuestName:             guestName,
		GuestEmail:            strings.TrimSpace(r.Attributes.Customer.Mail),
		GuestPhone:            strings.TrimSpace(r.Attributes.Customer.Phone),
		ProviderUpdatedAt:     parseFlexibleTimestamp(r.Attributes.UpdatedAt),
	}
	er.PayloadHash = computePayloadDigest(er)
	return er, true
}

func (a *ChannexAdapter) HandleWebhook(_ context.Context, _ map[string]string, _ []byte) error {
	return newProviderError(ErrWebhookVerificationFailed, "channex", "webhook", 401, "CONNECTION_CONTEXT_REQUIRED", "connection credentials required to verify webhook")
}

func (a *ChannexAdapter) VerifyWebhook(_ context.Context, _ map[string]string, creds map[string]string, headers map[string]string, body []byte, _ NonceChecker) (WebhookEvent, error) {
	secrets := []string{creds["webhook_secret"], creds["webhook_secret_previous"]}
	if err := VerifyChannexWebhook(headers, body, secrets, a.http.Now()); err != nil {
		return WebhookEvent{}, err
	}
	var envelope struct {
		Event      string `json:"event"`
		PropertyID string `json:"property_id"`
		Payload    struct {
			ID         string `json:"id"`
			RevisionID string `json:"revision_id"`
			BookingID  string `json:"booking_id"`
			PropertyID string `json:"property_id"`
		} `json:"payload"`
	}
	if err := decodeJSONStrict("channex", "webhook", body, &envelope); err != nil {
		return WebhookEvent{}, err
	}
	ev := strings.ToLower(strings.TrimSpace(envelope.Event))
	action := "reservation_sync"
	if strings.Contains(ev, "cancel") {
		action = "reservation_cancel"
	} else if !strings.Contains(ev, "booking") && ev != "" {
		action = "availability_ping"
	}
	revID := strings.TrimSpace(envelope.Payload.RevisionID)
	if revID == "" {
		revID = strings.TrimSpace(envelope.Payload.ID)
	}
	bookingID := strings.TrimSpace(envelope.Payload.BookingID)
	propID := strings.TrimSpace(envelope.PropertyID)
	if propID == "" {
		propID = strings.TrimSpace(envelope.Payload.PropertyID)
	}
	extID := revID
	if extID == "" {
		extID = SHA256Hex(body)[:32]
	}
	return WebhookEvent{
		ProviderCode:          "channex",
		ExternalEventID:       extID,
		EventType:             ev,
		Action:                action,
		ExternalPropertyID:    propID,
		ExternalReservationID: bookingID,
		ExternalRevision:      revID,
		RevisionAckID:         revID,
		SignatureVerified:     true,
	}, nil
}

func (a *ChannexAdapter) RegisterWebhook(ctx context.Context, publicCfg, creds map[string]string, callbackURL string) (string, error) {
	propID := strings.TrimSpace(publicCfg["property_id"])
	if propID == "" {
		return "", newProviderError(ErrInvalidInput, "channex", "register_webhook", 0, "MISSING_PROPERTY_ID", "Channex property_id required for webhook registration")
	}
	whPayload := map[string]any{
		"webhook": map[string]any{
			"property_id":  propID,
			"callback_url": callbackURL,
			"event_mask":   "booking",
			"is_active":    true,
			"send_data":    true,
			"is_signed":    true,
		},
	}
	body, _ := json.Marshal(whPayload)
	resp, err := a.doAPIKey(ctx, publicCfg, creds, RequestSpec{
		Operation: "register_webhook",
		Method:    http.MethodPost,
		Path:      "/webhooks",
		Body:      body,
	})
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", newProviderError(ErrInvalidResponse, "channex", "register_webhook", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Channex webhook registration failed")
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(resp.Body, &out)
	return strings.TrimSpace(out.Data.ID), nil
}

func (a *ChannexAdapter) DeleteWebhook(ctx context.Context, publicCfg, creds map[string]string, webhookID string) error {
	resp, err := a.doAPIKey(ctx, publicCfg, creds, RequestSpec{
		Operation:  "delete_webhook",
		Method:     http.MethodDelete,
		Path:       "/webhooks/" + url.PathEscape(strings.TrimSpace(webhookID)),
		Idempotent: true,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newProviderError(ErrInvalidResponse, "channex", "delete_webhook", resp.StatusCode, fmt.Sprintf("HTTP_%d", resp.StatusCode), "Channex webhook deletion failed")
	}
	return nil
}
