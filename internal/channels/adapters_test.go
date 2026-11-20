package channels

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMoneyDecimalExactConversions(t *testing.T) {
	cases := []struct {
		minor    int64
		currency string
		want     string
	}{
		{12550, "GBP", "125.50"},
		{100, "GBP", "1.00"},
		{5, "EUR", "0.05"},
		{9850, "JPY", "9850"},
		{12345, "KWD", "12.345"},
	}
	for _, tc := range cases {
		got, err := FormatMinorToDecimal(tc.minor, tc.currency)
		if err != nil {
			t.Fatalf("FormatMinorToDecimal(%d, %s): %v", tc.minor, tc.currency, err)
		}
		if got != tc.want {
			t.Fatalf("FormatMinorToDecimal(%d, %s) = %q, want %q", tc.minor, tc.currency, got, tc.want)
		}
		parsed, err := ParseDecimalToMinor(got, tc.currency)
		if err != nil || parsed != tc.minor {
			t.Fatalf("ParseDecimalToMinor(%q, %s) = (%d, %v), want %d", got, tc.currency, parsed, err, tc.minor)
		}
	}
	if _, err := ParseDecimalToMinor("10.123", "GBP"); err == nil {
		t.Fatal("expected precision overflow error for 3 decimals in GBP")
	}
}

func TestSmoobuCanonicalQueryAndHMACSigningAndReplay(t *testing.T) {
	q := url.Values{}
	q.Set("pageSize", "100")
	q.Add("filter", "b value")
	q.Add("filter", "a~tilde")
	q.Set("a_first", "1")
	canon := EncodeCanonicalQueryRFC3986(q)
	if canon != "a_first=1&filter=a~tilde&filter=b%20value&pageSize=100" {
		t.Fatalf("unexpected canonical query: %q", canon)
	}

	if h := SHA256Hex(nil); h != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("unexpected empty SHA-256 hex: %s", h)
	}

	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	ts := now.Format(time.RFC3339)
	nonce := "7d1a5e18-4f6a-4a0d-b772-97523f6bb001"
	sig := SignSmoobuRequest("GET", "/api/me", canon, ts, nonce, "key-1", "sec-1", nil)
	headers := map[string]string{
		"X-API-Key":   "key-1",
		"X-Timestamp": ts,
		"X-Nonce":     nonce,
		"X-Signature": sig,
	}
	seen := map[string]bool{}
	checker := func(_ context.Context, _ string, n string, _ time.Time) (bool, error) {
		if seen[n] {
			return false, nil
		}
		seen[n] = true
		return true, nil
	}
	if err := VerifySmoobuSignature(context.Background(), "GET", "/api/me", canon, headers, nil, "key-1", "sec-1", now, checker); err != nil {
		t.Fatalf("valid Smoobu signature rejected: %v", err)
	}
	// Replay rejected
	if err := VerifySmoobuSignature(context.Background(), "GET", "/api/me", canon, headers, nil, "key-1", "sec-1", now, checker); !errors.Is(err, ErrWebhookVerificationFailed) {
		t.Fatalf("expected replay rejection, got %v", err)
	}
	// Skew > 5m rejected
	if err := VerifySmoobuSignature(context.Background(), "GET", "/api/me", canon, headers, nil, "key-1", "sec-1", now.Add(6*time.Minute), nil); !errors.Is(err, ErrWebhookVerificationFailed) {
		t.Fatalf("expected >5m skew rejection, got %v", err)
	}
}

func TestSafeHTTPClientSecurityGuardsAndRateLimitRetry(t *testing.T) {
	// Non-allowlisted HTTPS rejected without AllowHTTPForTests
	client := NewSafeHTTPClient(AdapterConfig{
		ProviderBaseURLs: map[string]string{"beds24": "https://evil.example.com/v2"},
	})
	if _, err := client.ResolveEndpoint("beds24"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for non-allowlisted host, got %v", err)
	}

	// Redirect blocked
	redirTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer redirTarget.Close()
	redirSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirTarget.URL, http.StatusFound)
	}))
	defer redirSrv.Close()

	var attempts int32
	rateSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"validToken":true,"ownerId":9}`))
	}))
	defer rateSrv.Close()

	oversizeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 2048)))
	}))
	defer oversizeSrv.Close()

	cfgRedir := AdapterConfig{
		AllowHTTPForTests: true,
		ProviderBaseURLs:  map[string]string{"lodgify": redirSrv.URL},
	}
	if err := NewLodgifyAdapter(cfgRedir).VerifyConnection(context.Background(), nil, map[string]string{"api_key": "k"}); !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("expected ErrRedirectBlocked, got %v", err)
	}

	var slept time.Duration
	cfgRate := AdapterConfig{
		AllowHTTPForTests: true,
		ProviderBaseURLs:  map[string]string{"beds24": rateSrv.URL},
		MaxRetries:        3,
		Sleep: func(_ context.Context, d time.Duration) error {
			slept += d
			return nil
		},
	}
	res, err := NewBeds24Adapter(cfgRate).VerifyConnectionDetailed(context.Background(), map[string]string{"property_id": "42"}, map[string]string{"refresh_token": "rt", "access_token": "tok"})
	if err != nil {
		t.Fatalf("expected 429 retry recovery, got %v", err)
	}
	if slept != time.Second || res.AccountID != "42" {
		t.Fatalf("unexpected retry behavior: slept=%v res=%+v", slept, res)
	}

	cfgSize := AdapterConfig{
		AllowHTTPForTests: true,
		ProviderBaseURLs:  map[string]string{"lodgify": oversizeSrv.URL},
		MaxResponseBytes:  256,
		MaxRetries:        1,
	}
	if err := NewLodgifyAdapter(cfgSize).VerifyConnection(context.Background(), nil, map[string]string{"api_key": "k"}); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("expected ErrResponseTooLarge, got %v", err)
	}
}

func TestAllFourAdaptersOfflineFlows(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

	// 1. Beds24 mock server (setup, token refresh, details, properties paginated, calendar, bookings, disconnect ErrNotImplemented)
	var b24Page int32
	b24Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authentication/token":
			if r.Header.Get("refreshToken") != "b24_rt_1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"token":"b24_tok_fresh","expiresIn":86400}`))
		case "/authentication/details":
			if r.Header.Get("token") != "b24_tok_fresh" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"validToken":true,"ownerId":77}`))
		case "/properties":
			p := atomic.AddInt32(&b24Page, 1)
			if p == 1 {
				_, _ = w.Write([]byte(`{"success":true,"pages":{"nextPageExists":true},"data":[{"id":100,"name":"Meadowbrook","currency":"GBP","roomTypes":[{"id":101,"name":"Bell Tent 1","qty":1}]}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"pages":{"nextPageExists":false},"data":[{"id":100,"name":"Meadowbrook","currency":"GBP","roomTypes":[{"id":102,"name":"Shepherd Hut","qty":1}]}]}`))
		case "/inventory/rooms/calendar":
			_, _ = w.Write([]byte(`[{"success":true}]`))
		case "/bookings":
			_, _ = w.Write([]byte(`{"pages":{"nextPageExists":false},"data":[{"id":5001,"propertyId":100,"roomId":101,"status":"confirmed","channel":"airbnb","arrival":"2026-12-01","departure":"2026-12-04","currency":"GBP","price":"285.00","numAdult":2,"numChild":1,"firstName":"Alice","lastName":"Evans","email":"alice@example.test","modifiedTime":"2026-11-15T11:30:00Z"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer b24Srv.Close()

	b24 := NewBeds24Adapter(AdapterConfig{
		AllowHTTPForTests: true,
		Clock:             func() time.Time { return now },
		ProviderBaseURLs:  map[string]string{"beds24": b24Srv.URL},
	})
	ver, err := b24.VerifyConnectionDetailed(context.Background(), map[string]string{"property_id": "100"}, map[string]string{"refresh_token": "b24_rt_1"})
	if err != nil || ver.UpdatedCreds["access_token"] != "b24_tok_fresh" {
		t.Fatalf("Beds24 verify failed: ver=%+v err=%v", ver, err)
	}
	b24Listings, err := b24.DiscoverListingsDetailed(context.Background(), map[string]string{"property_id": "100"}, ver.UpdatedCreds)
	if err != nil || len(b24Listings) != 2 {
		t.Fatalf("Beds24 discovery failed: listings=%+v err=%v", b24Listings, err)
	}
	if err := b24.PushRates(context.Background(), nil, ver.UpdatedCreds, RatePushRequest{
		ExternalListingID: "101", Currency: "GBP",
		Days: []RateDay{{Date: "2026-12-01", PriceMinor: 9500, Currency: "GBP", MinStay: 2}},
	}); err != nil {
		t.Fatalf("Beds24 PushRates failed: %v", err)
	}
	b24Bookings, _, err := b24.PullReservations(context.Background(), map[string]string{"property_id": "100"}, ver.UpdatedCreds, time.Time{}, "")
	if err != nil || len(b24Bookings) != 1 || b24Bookings[0].TotalMinor != 28500 || b24Bookings[0].SourceChannel != "airbnb" {
		t.Fatalf("Beds24 PullReservations failed: %+v err=%v", b24Bookings, err)
	}
	if err := b24.Disconnect(context.Background(), nil, ver.UpdatedCreds); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented on Beds24 Disconnect, got %v", err)
	}

	// 2. Smoobu mock server verifying HMAC on every request
	smNonces := map[string]bool{}
	smSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := EncodeCanonicalQueryRFC3986(r.URL.Query())
		var bodyBuf []byte
		if r.Body != nil {
			dec := json.NewDecoder(r.Body)
			var raw json.RawMessage
			_ = dec.Decode(&raw)
			if len(raw) > 0 {
				bodyBuf = raw
			}
		}
		hdrs := map[string]string{
			"X-API-Key":   r.Header.Get("X-API-Key"),
			"X-Timestamp": r.Header.Get("X-Timestamp"),
			"X-Nonce":     r.Header.Get("X-Nonce"),
			"X-Signature": r.Header.Get("X-Signature"),
		}
		err := VerifySmoobuSignature(r.Context(), r.Method, r.URL.Path, q, hdrs, bodyBuf, "sm_key", "sm_secret", now, func(_ context.Context, _ string, n string, _ time.Time) (bool, error) {
			if smNonces[n] {
				return false, nil
			}
			smNonces[n] = true
			return true, nil
		})
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/me":
			_, _ = w.Write([]byte(`{"id":901,"company":"Meadowbrook Meadow"}`))
		case "/apartments":
			_, _ = w.Write([]byte(`{"page_count":1,"page":1,"apartments":[{"id":301,"name":"Owl Hut","currency":"GBP"}]}`))
		case "/rates":
			_, _ = w.Write([]byte(`{"success":true}`))
		case "/reservations":
			_, _ = w.Write([]byte(`{"page_count":1,"bookings":[{"id":8801,"type":"reservation","arrival":"2026-12-10","departure":"2026-12-12","apartment":{"id":301,"name":"Owl Hut"},"channel":{"id":1,"name":"Booking.com"},"guest-name":"Owain Griffiths","email":"owain@example.test","phone":"+447700900123","adults":2,"children":0,"price":180.5,"currency":"GBP","modified-at":"2026-11-15 11:00:00"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer smSrv.Close()

	sm := NewSmoobuAdapter(AdapterConfig{
		AllowHTTPForTests: true,
		Clock:             func() time.Time { return now },
		ProviderBaseURLs:  map[string]string{"smoobu": smSrv.URL},
	})
	smCreds := map[string]string{"api_key": "sm_key", "api_secret": "sm_secret", "webhook_secret": "wh_sm_secret"}
	smVer, err := sm.VerifyConnectionDetailed(context.Background(), nil, smCreds)
	if err != nil || smVer.AccountName != "Meadowbrook Meadow" {
		t.Fatalf("Smoobu verify failed: %+v err=%v", smVer, err)
	}
	if err := sm.PushAvailability(context.Background(), nil, smCreds, AvailabilityPushRequest{
		ExternalListingID: "301",
		Days:              []AvailabilityDay{{Date: "2026-12-15", AvailableUnits: 0}},
	}); err != nil {
		t.Fatalf("Smoobu PushAvailability failed: %v", err)
	}
	smRes, _, err := sm.PullReservations(context.Background(), nil, smCreds, time.Time{}, "")
	if err != nil || len(smRes) != 1 || smRes[0].TotalMinor != 18050 || smRes[0].SourceChannel != "booking_com" {
		t.Fatalf("Smoobu PullReservations failed: %+v err=%v", smRes, err)
	}

	// 3. Lodgify mock server verifying BLOCKED availability and supported rate_sync + reservations
	ldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-ApiKey") != "ldg_key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v2/properties":
			_, _ = w.Write([]byte(`{"count":1,"items":[{"id":70,"name":"Countryside Glamping","currency_code":"GBP","rooms":[{"id":701,"name":"Wren Tent"}]}]}`))
		case "/v1/rates/savewithoutavailability":
			_, _ = w.Write([]byte(`{"is_success":true}`))
		case "/v2/reservations/bookings":
			_, _ = w.Write([]byte(`{"count":1,"items":[{"id":9901,"property_id":70,"status":"Booked","source":"Vrbo","arrival":"2026-12-20","departure":"2026-12-23","currency_code":"GBP","total_amount":"330.00","updated_at":"2026-11-15T10:00:00Z","rooms":[{"room_type_id":701,"people":3}],"guest":{"name":"Bethan Jones","email":"bethan@example.test","phone":"+447700900456"}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ldSrv.Close()

	ld := NewLodgifyAdapter(AdapterConfig{
		AllowHTTPForTests: true,
		Clock:             func() time.Time { return now },
		ProviderBaseURLs:  map[string]string{"lodgify": ldSrv.URL},
	})
	ldCreds := map[string]string{"api_key": "ldg_key"}
	if err := ld.PushAvailability(context.Background(), nil, ldCreds, AvailabilityPushRequest{
		ExternalListingID: "701", Days: []AvailabilityDay{{Date: "2026-12-01", AvailableUnits: 1}},
	}); !errors.Is(err, ErrOperationBlocked) {
		t.Fatalf("expected ErrOperationBlocked on Lodgify PushAvailability, got %v", err)
	}
	if err := ld.PushRates(context.Background(), nil, ldCreds, RatePushRequest{
		ExternalPropertyID: "70", ExternalListingID: "701", Currency: "GBP",
		Days: []RateDay{{Date: "2026-12-20", PriceMinor: 11000, Currency: "GBP", MinStay: 2}},
	}); err != nil {
		t.Fatalf("Lodgify PushRates failed: %v", err)
	}
	ldRes, _, err := ld.PullReservations(context.Background(), nil, ldCreds, time.Time{}, "")
	if err != nil || len(ldRes) != 1 || ldRes[0].TotalMinor != 33000 || ldRes[0].SourceChannel != "vrbo" {
		t.Fatalf("Lodgify PullReservations failed: %+v err=%v", ldRes, err)
	}

	// 4. Channex mock server (staging host switch, availability, restrictions, revisions + ack, webhook key rotation)
	var acked bool
	chStagingSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("user-api-key") != "ch_key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/properties":
			_, _ = w.Write([]byte(`{"data":[{"id":"prop-ch-1","attributes":{"title":"Meadowbrook Staging","currency":"GBP"}}]}`))
		case "/room_types":
			_, _ = w.Write([]byte(`{"data":[{"id":"rt-ch-1","attributes":{"title":"Bell Tent"}}]}`))
		case "/rate_plans":
			_, _ = w.Write([]byte(`{"data":[{"id":"rp-ch-1","attributes":{"title":"Standard"},"relationships":{"room_type":{"data":{"id":"rt-ch-1"}}}}]}`))
		case "/availability", "/restrictions":
			_, _ = w.Write([]byte(`{"meta":{"message":"Success"}}`))
		case "/booking_revisions/feed":
			_, _ = w.Write([]byte(`{"data":[{"id":"rev-1","attributes":{"id":"rev-1","booking_id":"bk-ch-99","property_id":"prop-ch-1","status":"new","ota_name":"BookingCom","arrival_date":"2027-01-05","departure_date":"2027-01-08","currency":"GBP","amount":"420.00","inserted_at":"2026-11-15T11:45:00Z","customer":{"name":"Dafydd","surname":"Williams","mail":"dafydd@example.test","phone":"+447700900789"},"rooms":[{"room_type_id":"rt-ch-1","rate_plan_id":"rp-ch-1","occupancy":{"adults":2,"children":1,"infants":0}}]}}]}`))
		case "/booking_revisions/rev-1/ack":
			acked = true
			_, _ = w.Write([]byte(`{"meta":{"message":"Success"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer chStagingSrv.Close()

	ch := NewChannexAdapter(AdapterConfig{
		AllowHTTPForTests: true,
		Clock:             func() time.Time { return now },
		ProviderBaseURLs:  map[string]string{"channex:staging": chStagingSrv.URL},
	})
	chPub := map[string]string{"environment": "staging", "property_id": "prop-ch-1"}
	chCreds := map[string]string{
		"user_api_key":            "ch_key",
		"webhook_secret":          "new-rot-secret",
		"webhook_secret_previous": "old-rot-secret",
	}
	chVer, err := ch.VerifyConnectionDetailed(context.Background(), chPub, chCreds)
	if err != nil || chVer.Environment != "staging" {
		t.Fatalf("Channex staging verify failed: %+v err=%v", chVer, err)
	}
	chList, err := ch.DiscoverListingsDetailed(context.Background(), chPub, chCreds)
	if err != nil || len(chList) != 1 || !strings.Contains(chList[0].Name, "rp-ch-1") {
		t.Fatalf("Channex discovery failed: %+v err=%v", chList, err)
	}
	if err := ch.PushAvailability(context.Background(), chPub, chCreds, AvailabilityPushRequest{
		ExternalPropertyID: "prop-ch-1", ExternalListingID: "rt-ch-1",
		Days: []AvailabilityDay{{Date: "2027-01-05", AvailableUnits: 2}},
	}); err != nil {
		t.Fatalf("Channex PushAvailability failed: %v", err)
	}
	if err := ch.PushRates(context.Background(), chPub, chCreds, RatePushRequest{
		ExternalPropertyID: "prop-ch-1", ExternalRatePlanID: "rp-ch-1", Currency: "GBP",
		Days: []RateDay{{Date: "2027-01-05", PriceMinor: 14000, Currency: "GBP", MinStay: 2}},
	}); err != nil {
		t.Fatalf("Channex PushRates failed: %v", err)
	}
	chRevs, _, err := ch.PullReservations(context.Background(), chPub, chCreds, time.Time{}, "")
	if err != nil || len(chRevs) != 1 || chRevs[0].RevisionAckID != "rev-1" || chRevs[0].TotalMinor != 42000 {
		t.Fatalf("Channex PullReservations failed: %+v err=%v", chRevs, err)
	}
	if err := ch.AcknowledgeRevision(context.Background(), chPub, chCreds, chRevs[0].RevisionAckID); err != nil || !acked {
		t.Fatalf("Channex AcknowledgeRevision failed: acked=%v err=%v", acked, err)
	}

	// Channex webhook rotation signature verification
	whBody := []byte(`{"event":"booking","property_id":"prop-ch-1","payload":{"revision_id":"rev-1","booking_id":"bk-ch-99","property_id":"prop-ch-1"}}`)
	tsUnix := strconv.FormatInt(now.Unix(), 10)
	sigOld := SignChannexWebhook(tsUnix, whBody, "old-rot-secret")
	ev, err := ch.VerifyWebhook(context.Background(), chPub, chCreds, map[string]string{
		"x-hmac-timestamp":     tsUnix,
		"x-hmac-signature-256": sigOld,
	}, whBody, nil)
	if err != nil || !ev.SignatureVerified || ev.RevisionAckID != "rev-1" {
		t.Fatalf("Channex rotated-key webhook verify failed: %+v err=%v", ev, err)
	}
}

func TestAdapterDisconnect_WebhookDeleteFailureAndTokenRevokeSeparation(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		makeFn    func(baseURL string) LiveAdapter
		pubCfg    map[string]string
		creds     map[string]string
		delPrefix string
	}{
		{
			name: "smoobu",
			makeFn: func(baseURL string) LiveAdapter {
				return NewSmoobuAdapter(AdapterConfig{
					AllowHTTPForTests: true,
					MaxRetries:        0,
					Clock:             func() time.Time { return now },
					NonceFunc:         func() string { return "n1" },
					ProviderBaseURLs:  map[string]string{"smoobu": baseURL},
				})
			},
			pubCfg:    map[string]string{"webhook_id": "wh-sm-1"},
			creds:     map[string]string{"api_key": "k", "api_secret": "s"},
			delPrefix: "/users/webhooks/wh-sm-1",
		},
		{
			name: "lodgify",
			makeFn: func(baseURL string) LiveAdapter {
				return NewLodgifyAdapter(AdapterConfig{
					AllowHTTPForTests: true,
					MaxRetries:        0,
					Clock:             func() time.Time { return now },
					ProviderBaseURLs:  map[string]string{"lodgify": baseURL},
				})
			},
			pubCfg:    map[string]string{"webhook_id": "wh-lg-1"},
			creds:     map[string]string{"api_key": "lg_k"},
			delPrefix: "/webhooks/v1/wh-lg-1",
		},
		{
			name: "channex",
			makeFn: func(baseURL string) LiveAdapter {
				return NewChannexAdapter(AdapterConfig{
					AllowHTTPForTests: true,
					MaxRetries:        0,
					Clock:             func() time.Time { return now },
					ProviderBaseURLs:  map[string]string{"channex": baseURL},
				})
			},
			pubCfg:    map[string]string{"webhook_id": "wh-ch-1"},
			creds:     map[string]string{"user_api_key": "ch_k"},
			delPrefix: "/webhooks/wh-ch-1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name+"_webhook_delete_ok_token_unsupported", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete && r.URL.Path == tc.delPrefix {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()

			ad := tc.makeFn(srv.URL)
			if err := ad.Disconnect(context.Background(), tc.pubCfg, tc.creds); !errors.Is(err, ErrNotImplemented) {
				t.Fatalf("%s Disconnect expected ErrNotImplemented for unrevocable API keys after webhook delete, got %v", tc.name, err)
			}
			out, err := ad.DisconnectDetailed(context.Background(), tc.pubCfg, tc.creds)
			if err != nil {
				t.Fatalf("%s DisconnectDetailed unexpected err: %v", tc.name, err)
			}
			if !out.WebhookDeleteAttempted || out.WebhookDeleteStatus != CapabilitySupported || !out.ClearedWebhookConfig {
				t.Fatalf("%s expected webhook deleted & config clearable, got %+v", tc.name, out)
			}
			if out.TokenRevocationStatus != CapabilityNotImplemented || out.TokenRevocationErrorCode != "NO_REMOTE_REVOKE" {
				t.Fatalf("%s expected honest NO_REMOTE_REVOKE token status, got %+v", tc.name, out)
			}
		})

		t.Run(tc.name+"_webhook_delete_failure_surfaces_blocked", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()

			ad := tc.makeFn(srv.URL)
			dErr := ad.Disconnect(context.Background(), tc.pubCfg, tc.creds)
			if dErr == nil || errors.Is(dErr, ErrNotImplemented) {
				t.Fatalf("%s Disconnect must surface webhook delete failure instead of masking under ErrNotImplemented, got %v", tc.name, dErr)
			}
			out, outErr := ad.DisconnectDetailed(context.Background(), tc.pubCfg, tc.creds)
			if outErr == nil {
				t.Fatalf("%s DisconnectDetailed must return error on webhook delete HTTP 500", tc.name)
			}
			if !out.WebhookDeleteAttempted || out.WebhookDeleteStatus != CapabilityBlocked || out.WebhookDeleteErrorCode != "HTTP_500" || out.ClearedWebhookConfig {
				t.Fatalf("%s expected BLOCKED/HTTP_500 webhook cleanup and ClearedWebhookConfig=false, got %+v", tc.name, out)
			}
			if out.TokenRevocationStatus != CapabilityNotImplemented {
				t.Fatalf("%s expected TokenRevocationStatus=NOT_IMPLEMENTED, got %+v", tc.name, out)
			}
		})

		t.Run(tc.name+"_no_webhook_id_not_applicable", func(t *testing.T) {
			ad := tc.makeFn("https://example.invalid")
			out, err := ad.DisconnectDetailed(context.Background(), map[string]string{}, tc.creds)
			if err != nil {
				t.Fatalf("%s DisconnectDetailed with no webhook failed: %v", tc.name, err)
			}
			if out.WebhookDeleteAttempted || out.WebhookDeleteStatus != CapabilityNotApplicable {
				t.Fatalf("%s expected NOT_APPLICABLE when webhook_id absent, got %+v", tc.name, out)
			}
		})
	}
}
