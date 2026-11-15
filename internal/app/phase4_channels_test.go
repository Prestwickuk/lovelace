package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tirbach/tir-bach/internal/app"
	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/channels"
	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func setupPhase4AppServer(t *testing.T, tag string, adapterCfg channels.AdapterConfig) (*httptest.Server, *postgres.Store, *service.Service, domain.Property, *testClock, string) {
	t.Helper()
	srv0, migratorStore, _, prop, _, _, clk := setupPhase2TestServer(t, tag)
	srv0.Close()

	ctx := context.Background()
	_, _ = migratorStore.Pool().Exec(ctx, `DELETE FROM channel_hmac_nonces`)

	appURL := openSignoffAppRoleURL(t, migratorStore)
	appStore, err := postgres.New(ctx, appURL)
	if err != nil {
		t.Fatalf("connect app role: %v", err)
	}
	t.Cleanup(appStore.Close)

	cfg := config.Config{
		AppEnv:          "test",
		DatabaseURL:     appURL,
		CookieSecure:    false,
		EncryptionKey:   testEncryptionKey,
		SessionTTL:      1 * time.Hour,
		SessionIdleTTL:  30 * time.Minute,
		LoginMaxFailIP:  5,
		LoginMaxFailAcc: 4,
		LoginWindow:     15 * time.Minute,
		EmailFrom:       "bookings@tirbach.wales",
	}
	appSvc := service.New(appStore, cfg)
	appSvc.SetClock(clk.Now)
	appSvc.SetChannelAdapterConfig(adapterCfg)

	router, err := app.NewRouter(cfg, appStore, appSvc)
	if err != nil {
		t.Fatalf("build app router: %v", err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// Ensure OTA channel codes exist cleanly for property mappings.
	for _, ch := range []struct {
		code string
		name string
	}{
		{"AIRBNB", "Airbnb"},
		{"BOOKING_COM", "Booking.com"},
		{"VRBO", "Vrbo"},
		{"BEDS24", "Beds24"},
		{"SMOOBU", "Smoobu"},
		{"LODGIFY", "Lodgify"},
		{"CHANNEX", "Channex"},
	} {
		if _, err := migratorStore.Pool().Exec(ctx, `
			INSERT INTO booking_channels (id, property_id, code, name, category, is_active, sort_order)
			VALUES ($1, $2, $3, $4, 'ota', TRUE, 50)
			ON CONFLICT (property_id, code) DO NOTHING
		`, uuid.New(), prop.ID, ch.code, ch.name); err != nil {
			t.Fatalf("seed booking channel %s: %v", ch.code, err)
		}
	}

	client := newClient(t)
	status, csrf := apiLogin(t, client, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")
	if status != http.StatusOK {
		t.Fatalf("owner login status=%d", status)
	}
	_ = client
	return srv, migratorStore, appSvc, prop, clk, csrf
}

func TestPhase4_ProviderLifecyclesSyncWebhooksConflictsAndAppRole(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	var (
		b24RefreshCount atomic.Int32
		b24AvailPushes  atomic.Int32
		b24RatePushes   atomic.Int32
		smRatePushes    atomic.Int32
		smNonces        sync.Map
		smResStatus     atomic.Value
		smResDeparture  atomic.Value
		smResUpdated    atomic.Value
		ldRatePushes    atomic.Int32
		chAckedRevs     sync.Map
		chAvailPushes   atomic.Int32
	)
	smResStatus.Store("reservation")
	smResDeparture.Store("2026-12-14")
	smResUpdated.Store("2026-11-15T10:05:00Z")

	// Beds24 mock server
	b24Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authentication/token":
			if r.Header.Get("refreshToken") != "b24_rt_live_secret_99" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			b24RefreshCount.Add(1)
			_, _ = w.Write([]byte(`{"token":"b24_tok_live_abc","expiresIn":86400}`))
		case "/authentication/details":
			if r.Header.Get("token") != "b24_tok_live_abc" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"validToken":true,"ownerId":1001}`))
		case "/properties":
			_, _ = w.Write([]byte(`{"success":true,"pages":{"nextPageExists":false},"data":[{"id":1001,"name":"Tir Bach Glamping","currency":"GBP","roomTypes":[{"id":201,"name":"Bell Tent 1","qty":2}]}]}`))
		case "/inventory/rooms/calendar":
			raw, _ := io.ReadAll(r.Body)
			if bytes.Contains(raw, []byte("numAvail")) {
				b24AvailPushes.Add(1)
			} else {
				b24RatePushes.Add(1)
			}
			_, _ = w.Write([]byte(`[{"success":true}]`))
		case "/bookings":
			_, _ = w.Write([]byte(`{"pages":{"nextPageExists":false},"data":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer b24Srv.Close()

	// Smoobu mock server enforcing HMAC-SHA256 on outbound calls
	smSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		hdrs := map[string]string{
			"X-API-Key":   r.Header.Get("X-API-Key"),
			"X-Timestamp": r.Header.Get("X-Timestamp"),
			"X-Nonce":     r.Header.Get("X-Nonce"),
			"X-Signature": r.Header.Get("X-Signature"),
		}
		err := channels.VerifySmoobuSignature(
			r.Context(), r.Method, r.URL.Path,
			channels.EncodeCanonicalQueryRFC3986(r.URL.Query()),
			hdrs, raw, "sm_key_secret_1", "sm_hmac_secret_2", now,
			func(_ context.Context, _ string, n string, _ time.Time) (bool, error) {
				_, existed := smNonces.LoadOrStore(n, true)
				return !existed, nil
			},
		)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/me":
			_, _ = w.Write([]byte(`{"id":808,"company":"Tir Bach Meadow"}`))
		case "/apartments":
			_, _ = w.Write([]byte(`{"page_count":1,"page":1,"apartments":[{"id":301,"name":"Owl Hut","currency":"GBP"}]}`))
		case "/rates":
			smRatePushes.Add(1)
			_, _ = w.Write([]byte(`{"success":true}`))
		case "/reservations/7701":
			st := smResStatus.Load().(string)
			dep := smResDeparture.Load().(string)
			upd := smResUpdated.Load().(string)
			_, _ = fmt.Fprintf(w, `{"id":7701,"type":%q,"arrival":"2026-12-10","departure":%q,"apartment":{"id":301,"name":"Owl Hut"},"channel":{"id":1,"name":"Airbnb"},"guest-name":"Megan Pritchard","email":"megan.ota@example.test","phone":"+447700900321","adults":2,"children":0,"price":320.00,"currency":"GBP","modified-at":%q}`, st, dep, upd)
		case "/reservations":
			_, _ = w.Write([]byte(`{"page_count":1,"bookings":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer smSrv.Close()

	// Lodgify mock server
	ldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-ApiKey") != "ldg_secret_key_3" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v2/properties":
			_, _ = w.Write([]byte(`{"count":1,"items":[{"id":70,"name":"Tir Bach","currency_code":"GBP","rooms":[{"id":701,"name":"Meadow Pod"}]}]}`))
		case "/v1/rates/savewithoutavailability":
			ldRatePushes.Add(1)
			_, _ = w.Write([]byte(`{"is_success":true}`))
		case "/v2/reservations/bookings":
			_, _ = w.Write([]byte(`{"count":0,"items":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ldSrv.Close()

	// Channex mock server
	chSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("user-api-key") != "ch_secret_key_4" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/properties":
			_, _ = w.Write([]byte(`{"data":[{"id":"ch-prop-1","attributes":{"title":"Tir Bach Staging","currency":"GBP"}}]}`))
		case "/room_types":
			_, _ = w.Write([]byte(`{"data":[{"id":"ch-rt-1","attributes":{"title":"Bell Tent"}}]}`))
		case "/rate_plans":
			_, _ = w.Write([]byte(`{"data":[{"id":"ch-rp-1","attributes":{"title":"Standard"},"relationships":{"room_type":{"data":{"id":"ch-rt-1"}}}}]}`))
		case "/availability":
			chAvailPushes.Add(1)
			_, _ = w.Write([]byte(`{"meta":{"message":"Success"}}`))
		case "/restrictions":
			_, _ = w.Write([]byte(`{"meta":{"message":"Success"}}`))
		case "/booking_revisions/rev-ok-1":
			_, _ = w.Write([]byte(`{"data":{"id":"rev-ok-1","attributes":{"id":"rev-ok-1","booking_id":"ch-bk-ok","property_id":"ch-prop-1","status":"new","ota_name":"BookingCom","arrival_date":"2026-12-20","departure_date":"2026-12-22","currency":"GBP","amount":"220.00","inserted_at":"2026-11-15T10:15:00Z","customer":{"name":"Huw","surname":"Thomas","mail":"huw@example.test","phone":"+447700900888"},"rooms":[{"room_type_id":"ch-rt-1","rate_plan_id":"ch-rp-1","occupancy":{"adults":2,"children":0,"infants":0}}]}}}`))
		case "/booking_revisions/rev-ok-1/ack":
			chAckedRevs.Store("rev-ok-1", true)
			_, _ = w.Write([]byte(`{"meta":{"message":"Success"}}`))
		case "/booking_revisions/rev-conflict-2":
			// Same unit dates as ch-bk-ok if only 1 unit or after all units of unit type are claimed
			_, _ = w.Write([]byte(`{"data":{"id":"rev-conflict-2","attributes":{"id":"rev-conflict-2","booking_id":"ch-bk-conflict","property_id":"ch-prop-1","status":"new","ota_name":"BookingCom","arrival_date":"2026-12-20","departure_date":"2026-12-22","currency":"GBP","amount":"220.00","inserted_at":"2026-11-15T10:20:00Z","customer":{"name":"Rhodri","surname":"Davies","mail":"rhodri@example.test","phone":"+447700900999"},"rooms":[{"room_type_id":"ch-rt-1","rate_plan_id":"ch-rp-1","occupancy":{"adults":2,"children":0,"infants":0}}]}}}`))
		case "/booking_revisions/rev-conflict-2/ack":
			chAckedRevs.Store("rev-conflict-2", true)
			_, _ = w.Write([]byte(`{"meta":{"message":"Success"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer chSrv.Close()

	adapterCfg := channels.AdapterConfig{
		AllowHTTPForTests: true,
		Clock:             func() time.Time { return now },
		ProviderBaseURLs: map[string]string{
			"beds24":          b24Srv.URL,
			"smoobu":          smSrv.URL,
			"lodgify":         ldSrv.URL,
			"channex:staging": chSrv.URL,
		},
	}

	srv, migratorStore, appSvc, prop, clk, _ := setupPhase4AppServer(t, "pub-p4-e2e", adapterCfg)
	clk.Advance(now.Sub(clk.Now()))

	client := newClient(t)
	status, csrf := apiLogin(t, client, srv.URL, "owner@tirbach.wales", "tirbach-owner-password-2026")
	if status != http.StatusOK {
		t.Fatalf("login failed: %d", status)
	}

	unitTypes, err := migratorStore.ListUnitTypes(context.Background(), prop.ID, false)
	if err != nil || len(unitTypes) == 0 {
		t.Fatalf("list unit types: %v", err)
	}
	ut := unitTypes[0]
	ratePlans, err := migratorStore.ListRatePlans(context.Background(), prop.ID)
	if err != nil || len(ratePlans) == 0 {
		t.Fatalf("list rate plans: %v", err)
	}
	rp := ratePlans[0]

	// 1. Beds24: save -> verify -> discover -> map -> preview -> require confirm_scope -> confirmed push -> idempotent skip -> disconnect
	rSaveB24 := doJSON(t, client, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/beds24", csrf, map[string]any{
		"inputs": map[string]string{
			"property_id":   "1001",
			"refresh_token": "b24_rt_live_secret_99",
		},
	})
	if rSaveB24.StatusCode != http.StatusOK {
		t.Fatalf("save beds24 status=%d", rSaveB24.StatusCode)
	}
	rSaveB24.Body.Close()

	rVerB24 := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/beds24/verify", csrf, nil)
	if rVerB24.StatusCode != http.StatusOK {
		t.Fatalf("verify beds24 status=%d", rVerB24.StatusCode)
	}
	rVerB24.Body.Close()

	rMapB24 := doJSON(t, client, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/beds24/mappings", csrf, map[string]any{
		"external_property_id": "1001",
		"external_listing_id":  "201",
		"external_name":        "Bell Tent 1",
		"unit_type_id":         ut.ID,
		"rate_plan_id":         rp.ID,
		"booking_channel_code": "AIRBNB",
		"currency":             prop.Currency,
		"timezone":             prop.Timezone,
	})
	if rMapB24.StatusCode != http.StatusOK {
		t.Fatalf("map beds24 status=%d", rMapB24.StatusCode)
	}
	rMapB24.Body.Close()

	// Preview >14d reports requires_confirmation=true
	rPrev := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/beds24/sync/preview", csrf, map[string]any{
		"start_date":        "2026-12-01",
		"end_date":          "2026-12-20",
		"sync_availability": true,
		"sync_rates":        true,
	})
	if rPrev.StatusCode != http.StatusOK {
		t.Fatalf("preview beds24 status=%d", rPrev.StatusCode)
	}
	var prevOut service.ChannelSyncPreview
	_ = json.NewDecoder(rPrev.Body).Decode(&prevOut)
	rPrev.Body.Close()
	if !prevOut.RequiresConfirmation || prevOut.TotalNights != 19 {
		t.Fatalf("unexpected sync preview output: %+v", prevOut)
	}

	// Missing confirm_scope must fail 422
	rUnconfirmed := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/beds24/sync", csrf, map[string]any{
		"start_date":        "2026-12-01",
		"end_date":          "2026-12-20",
		"sync_availability": true,
	})
	rUnconfirmed.Body.Close()
	if rUnconfirmed.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 confirmation_required, got %d", rUnconfirmed.StatusCode)
	}

	// Confirmed sync pushes availability + rates
	rSync := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/beds24/sync", csrf, map[string]any{
		"start_date":        "2026-12-01",
		"end_date":          "2026-12-20",
		"sync_availability": true,
		"sync_rates":        true,
		"confirm_scope":     true,
	})
	if rSync.StatusCode != http.StatusAccepted {
		t.Fatalf("confirmed sync status=%d", rSync.StatusCode)
	}
	rSync.Body.Close()

	if _, err := appSvc.ProcessPendingChannelSyncJobs(context.Background(), 20); err != nil {
		t.Fatalf("process beds24 jobs: %v", err)
	}
	if b24AvailPushes.Load() != 1 || b24RatePushes.Load() != 1 {
		t.Fatalf("expected 1 avail + 1 rate push, got avail=%d rate=%d", b24AvailPushes.Load(), b24RatePushes.Load())
	}

	// Second identical push is skipped via checkpoint digest
	rSync2 := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/beds24/sync", csrf, map[string]any{
		"start_date":        "2026-12-01",
		"end_date":          "2026-12-20",
		"sync_availability": true,
		"sync_rates":        true,
		"confirm_scope":     true,
	})
	rSync2.Body.Close()
	_, _ = appSvc.ProcessPendingChannelSyncJobs(context.Background(), 20)
	if b24AvailPushes.Load() != 1 || b24RatePushes.Load() != 1 {
		t.Fatalf("expected identical second push skipped via digest, got avail=%d rate=%d", b24AvailPushes.Load(), b24RatePushes.Load())
	}

	// Disconnect reports honest NOT_IMPLEMENTED for Beds24 and clears credentials
	rDisc := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/beds24/disconnect", csrf, nil)
	if rDisc.StatusCode != http.StatusOK {
		t.Fatalf("disconnect beds24 status=%d", rDisc.StatusCode)
	}
	var discOut service.DisconnectChannelResult
	_ = json.NewDecoder(rDisc.Body).Decode(&discOut)
	rDisc.Body.Close()
	if discOut.RemoteRevoked || discOut.CapabilityStatus != channels.CapabilityNotImplemented || discOut.Connection.HasCredentials {
		t.Fatalf("unexpected Beds24 disconnect result: %+v", discOut)
	}

	// 2. Smoobu: HMAC verification, signed webhook -> hydration -> booking -> stale out-of-order suppression -> mod -> cancel
	rSaveSm := doJSON(t, client, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/smoobu", csrf, map[string]any{
		"inputs": map[string]string{
			"api_key":        "sm_key_secret_1",
			"api_secret":     "sm_hmac_secret_2",
			"webhook_secret": "sm_wh_secret_9",
		},
	})
	var smConn channels.ChannelConnection
	_ = json.NewDecoder(rSaveSm.Body).Decode(&smConn)
	rSaveSm.Body.Close()
	rVerSm := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/smoobu/verify", csrf, nil)
	if rVerSm.StatusCode != http.StatusOK {
		t.Fatalf("verify smoobu status=%d", rVerSm.StatusCode)
	}
	rVerSm.Body.Close()

	// Pick single specific unit to test both booking & claim occupancy deterministically.
	units, err := migratorStore.ListUnits(context.Background(), prop.ID, &ut.ID, false)
	if err != nil || len(units) == 0 {
		t.Fatalf("list units: %v", err)
	}
	rMapSm := doJSON(t, client, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/smoobu/mappings", csrf, map[string]any{
		"external_listing_id":  "301",
		"unit_type_id":         ut.ID,
		"unit_id":              units[0].ID,
		"rate_plan_id":         rp.ID,
		"booking_channel_code": "SMOOBU",
		"currency":             prop.Currency,
		"timezone":             prop.Timezone,
	})
	if rMapSm.StatusCode != http.StatusOK {
		t.Fatalf("map smoobu status=%d", rMapSm.StatusCode)
	}
	rMapSm.Body.Close()

	whURL := srv.URL + "/api/v1/webhooks/channels/smoobu/" + smConn.ID.String()
	// Unsigned/bad secret rejected 401
	badReq, _ := http.NewRequest(http.MethodPost, whURL, strings.NewReader(`{"action":"newReservation","data":{"id":7701}}`))
	badReq.Header.Set("X-Lovelace-Webhook-Secret", "wrong-secret")
	badResp, _ := client.Do(badReq)
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 invalid smoobu webhook secret, got %d", badResp.StatusCode)
	}

	// Signed valid Smoobu webhook
	whPayload := []byte(`{"action":"newReservation","data":{"id":7701,"apartment":{"id":301},"modified-at":"2026-11-15T10:05:00Z"}}`)
	tsStr := now.Format(time.RFC3339)
	nonce1 := uuid.NewString()
	whPath := "/api/v1/webhooks/channels/smoobu/" + smConn.ID.String()
	sig1 := channels.SignSmoobuRequest(http.MethodPost, whPath, "", tsStr, nonce1, "sm_key_secret_1", "sm_hmac_secret_2", whPayload)
	okReq, _ := http.NewRequest(http.MethodPost, whURL, bytes.NewReader(whPayload))
	okReq.Header.Set("X-API-Key", "sm_key_secret_1")
	okReq.Header.Set("X-Timestamp", tsStr)
	okReq.Header.Set("X-Nonce", nonce1)
	okReq.Header.Set("X-Signature", sig1)
	okResp, err := client.Do(okReq)
	if err != nil {
		t.Fatalf("post smoobu webhook: %v", err)
	}
	okResp.Body.Close()
	if okResp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 for valid smoobu HMAC webhook, got %d", okResp.StatusCode)
	}
	// Exact nonce replay rejected 401
	replayReq, _ := http.NewRequest(http.MethodPost, whURL, bytes.NewReader(whPayload))
	replayReq.Header.Set("X-API-Key", "sm_key_secret_1")
	replayReq.Header.Set("X-Timestamp", tsStr)
	replayReq.Header.Set("X-Nonce", nonce1)
	replayReq.Header.Set("X-Signature", sig1)
	repResp, _ := client.Do(replayReq)
	repResp.Body.Close()
	if repResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 nonce replay rejection, got %d", repResp.StatusCode)
	}

	if _, err := appSvc.ProcessPendingChannelSyncJobs(context.Background(), 20); err != nil {
		t.Fatalf("process smoobu webhook job: %v", err)
	}
	extResSm, err := migratorStore.GetExternalReservationByExtID(context.Background(), nil, prop.ID, "smoobu", "7701")
	if err != nil || extResSm.ReservationID == nil || extResSm.ImportStatus != domain.ChannelImportImported {
		jobs, _ := migratorStore.ListChannelSyncJobs(context.Background(), nil, prop.ID, "smoobu", 10)
		t.Fatalf("expected imported Smoobu reservation, got %+v err=%v jobs=%+v", extResSm, err, jobs)
	}

	// Out-of-order stale webhook update ignored, then cancellation frees claim
	smResStatus.Store("cancellation")
	smResUpdated.Store("2026-11-15T10:12:00Z")
	cancelPayload := []byte(`{"action":"cancelReservation","data":{"id":7701,"apartment":{"id":301},"modified-at":"2026-11-15T10:12:00Z"}}`)
	cReq, _ := http.NewRequest(http.MethodPost, whURL, bytes.NewReader(cancelPayload))
	cReq.Header.Set("X-Lovelace-Webhook-Secret", "sm_wh_secret_9")
	cResp, _ := client.Do(cReq)
	cResp.Body.Close()
	if cResp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel webhook status=%d", cResp.StatusCode)
	}
	_, _ = appSvc.ProcessPendingChannelSyncJobs(context.Background(), 20)
	resAfterCancel, err := migratorStore.GetReservation(context.Background(), nil, prop.ID, *extResSm.ReservationID)
	if err != nil || resAfterCancel.Status != domain.ResStatusCancelled {
		jobs, _ := migratorStore.ListChannelSyncJobs(context.Background(), nil, prop.ID, "smoobu", 10)
		t.Fatalf("expected Smoobu reservation CANCELLED, got %+v err=%v jobs=%+v", resAfterCancel, err, jobs)
	}

	// 3. Lodgify: blocked outbound availability check (422 capability_blocked), rates push succeeds
	rSaveLd := doJSON(t, client, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/lodgify", csrf, map[string]any{
		"inputs": map[string]string{"account_id": "70", "api_key": "ldg_secret_key_3"},
	})
	rSaveLd.Body.Close()
	rVerLd := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/lodgify/verify", csrf, nil)
	if rVerLd.StatusCode != http.StatusOK {
		t.Fatalf("verify lodgify status=%d", rVerLd.StatusCode)
	}
	rVerLd.Body.Close()
	rMapLd := doJSON(t, client, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/lodgify/mappings", csrf, map[string]any{
		"external_property_id": "70",
		"external_listing_id":  "701",
		"unit_type_id":         ut.ID,
		"rate_plan_id":         rp.ID,
		"booking_channel_code": "LODGIFY",
		"currency":             prop.Currency,
		"timezone":             prop.Timezone,
	})
	rMapLd.Body.Close()

	rLdAvailBlocked := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/lodgify/sync", csrf, map[string]any{
		"start_date":        "2026-12-01",
		"end_date":          "2026-12-05",
		"sync_availability": true,
	})
	rLdAvailBlocked.Body.Close()
	if rLdAvailBlocked.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 capability_blocked on Lodgify availability push, got %d", rLdAvailBlocked.StatusCode)
	}
	rLdRates := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/lodgify/sync", csrf, map[string]any{
		"start_date": "2026-12-01",
		"end_date":   "2026-12-05",
		"sync_rates": true,
	})
	rLdRates.Body.Close()
	_, _ = appSvc.ProcessPendingChannelSyncJobs(context.Background(), 20)
	if ldRatePushes.Load() != 1 {
		t.Fatalf("expected Lodgify rate push count=1, got %d", ldRatePushes.Load())
	}

	// 4. Channex staging: rotated-key HMAC webhook + reservation commit & ack + overbooking conflict detection
	rSaveCh := doJSON(t, client, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/channex", csrf, map[string]any{
		"inputs": map[string]string{
			"property_id":             "ch-prop-1",
			"environment":             "staging",
			"user_api_key":            "ch_secret_key_4",
			"webhook_secret":          "ch_wh_new_key",
			"webhook_secret_previous": "ch_wh_old_key",
		},
	})
	var chConn channels.ChannelConnection
	_ = json.NewDecoder(rSaveCh.Body).Decode(&chConn)
	rSaveCh.Body.Close()
	rVerCh := doJSON(t, client, http.MethodPost, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/channex/verify", csrf, nil)
	if rVerCh.StatusCode != http.StatusOK {
		t.Fatalf("verify channex status=%d", rVerCh.StatusCode)
	}
	rVerCh.Body.Close()

	// Map Channex room type to unit[0] specifically so 2nd booking on exact same dates hits real inventory overlap!
	rMapCh := doJSON(t, client, http.MethodPut, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/channex/mappings", csrf, map[string]any{
		"external_property_id":  "ch-prop-1",
		"external_listing_id":   "ch-rt-1",
		"external_rate_plan_id": "ch-rp-1",
		"unit_type_id":          ut.ID,
		"unit_id":               units[0].ID,
		"rate_plan_id":          rp.ID,
		"booking_channel_code":  "BOOKING_COM",
		"currency":              prop.Currency,
		"timezone":              prop.Timezone,
	})
	rMapCh.Body.Close()

	chWhURL := srv.URL + "/api/v1/webhooks/channels/channex/" + chConn.ID.String()
	chBody1 := []byte(`{"event":"booking","property_id":"ch-prop-1","payload":{"revision_id":"rev-ok-1","booking_id":"ch-bk-ok","property_id":"ch-prop-1"}}`)
	tsUnix := fmt.Sprintf("%d", now.Unix())
	chSigOld := channels.SignChannexWebhook(tsUnix, chBody1, "ch_wh_old_key")
	chReq1, _ := http.NewRequest(http.MethodPost, chWhURL, bytes.NewReader(chBody1))
	chReq1.Header.Set("x-hmac-timestamp", tsUnix)
	chReq1.Header.Set("x-hmac-signature-256", chSigOld)
	chResp1, _ := client.Do(chReq1)
	chResp1.Body.Close()
	if chResp1.StatusCode != http.StatusAccepted {
		t.Fatalf("channex rotated key webhook status=%d", chResp1.StatusCode)
	}
	// Duplicate webhook is deduplicated with 200 OK
	chDupReq, _ := http.NewRequest(http.MethodPost, chWhURL, bytes.NewReader(chBody1))
	chDupReq.Header.Set("x-hmac-timestamp", tsUnix)
	chDupReq.Header.Set("x-hmac-signature-256", chSigOld)
	chDupResp, _ := client.Do(chDupReq)
	var dupBody service.WebhookProcessResult
	_ = json.NewDecoder(chDupResp.Body).Decode(&dupBody)
	chDupResp.Body.Close()
	if chDupResp.StatusCode != http.StatusOK || !dupBody.Deduplicated {
		t.Fatalf("expected deduplicated webhook 200 OK, got status=%d body=%+v", chDupResp.StatusCode, dupBody)
	}

	// Second Channex booking overlapping exact unit & dates must record CONFLICT_OVERBOOKED
	chBody2 := []byte(`{"event":"booking","property_id":"ch-prop-1","payload":{"revision_id":"rev-conflict-2","booking_id":"ch-bk-conflict","property_id":"ch-prop-1"}}`)
	chSigNew := channels.SignChannexWebhook(tsUnix, chBody2, "ch_wh_new_key")
	chReq2, _ := http.NewRequest(http.MethodPost, chWhURL, bytes.NewReader(chBody2))
	chReq2.Header.Set("x-hmac-timestamp", tsUnix)
	chReq2.Header.Set("x-hmac-signature-256", chSigNew)
	chResp2, _ := client.Do(chReq2)
	chResp2.Body.Close()

	_, _ = appSvc.ProcessPendingChannelSyncJobs(context.Background(), 20)

	if _, acked1 := chAckedRevs.Load("rev-ok-1"); !acked1 {
		t.Fatal("expected Channex rev-ok-1 to be acknowledged after durable commit")
	}
	conflictRec, err := migratorStore.GetExternalReservationByExtID(context.Background(), nil, prop.ID, "channex", "ch-bk-conflict")
	if err != nil || conflictRec.ImportStatus != domain.ChannelImportConflictOverbooked || conflictRec.ReservationID != nil {
		t.Fatalf("expected CONFLICT_OVERBOOKED without creating overlapping reservation, got %+v err=%v", conflictRec, err)
	}

	// 5. Verify Health endpoint + ZERO secrets or guest PII in audit_logs or channel_manager HTML UI
	rHealth := doJSON(t, client, http.MethodGet, srv.URL+"/api/v1/properties/"+prop.ID.String()+"/integrations/channel-manager/channex/health", csrf, nil)
	if rHealth.StatusCode != http.StatusOK {
		t.Fatalf("health endpoint status=%d", rHealth.StatusCode)
	}
	rHealth.Body.Close()

	rUI, err := client.Get(srv.URL + "/settings/integrations/channel-manager")
	if err != nil {
		t.Fatalf("GET channel-manager UI: %v", err)
	}
	uiHTMLBytes, _ := io.ReadAll(rUI.Body)
	rUI.Body.Close()
	if rUI.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on UI page, got %d", rUI.StatusCode)
	}
	uiHTML := string(uiHTMLBytes)
	if strings.Contains(uiHTML, "Render Error") || !strings.Contains(uiHTML, "Listing Mappings") {
		t.Fatalf("channel_manager UI failed to render completely: %s", uiHTML)
	}
	for _, secret := range []string{
		"b24_rt_live_secret_99", "b24_tok_live_abc",
		"sm_key_secret_1", "sm_hmac_secret_2", "sm_wh_secret_9",
		"ldg_secret_key_3", "ch_secret_key_4", "ch_wh_new_key", "ch_wh_old_key",
		"megan.ota@example.test", "huw@example.test", "rhodri@example.test",
	} {
		if strings.Contains(uiHTML, secret) {
			t.Fatalf("secret or PII %q leaked into channel_manager HTML UI", secret)
		}
	}

	var leakedAudit int
	_ = migratorStore.Pool().QueryRow(context.Background(), `
		SELECT COUNT(*) FROM audit_logs
		WHERE CAST(before_data AS TEXT) || CAST(after_data AS TEXT) ~* 'b24_rt_live|sm_hmac_secret|ldg_secret_key|ch_secret_key|megan\.ota|huw@example|rhodri@example|07700900'
	`).Scan(&leakedAudit)
	if leakedAudit != 0 {
		t.Fatalf("found %d audit_logs rows leaking credentials or guest PII", leakedAudit)
	}
}

func TestPhase4_WorkerLeaseFencingAndCrossPropertyIsolation(t *testing.T) {
	srv, migratorStore, _, prop, _, _ := setupPhase4AppServer(t, "pub-p4-fence", channels.AdapterConfig{})
	defer srv.Close()
	ctx := context.Background()

	// Seed connection row cleanly via repository.
	connID := uuid.New()
	tx, err := migratorStore.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	owner, _ := migratorStore.GetAdminUserByEmail(ctx, tx, "owner@tirbach.wales")
	rec, err := migratorStore.UpsertChannelConnection(
		ctx, tx, connID, prop.ID, "beds24", "Beds24",
		channels.AuthToken, channels.StatusConnected,
		map[string]string{"property_id": "1"}, []byte("cipher"), map[string]string{"refresh_token": "••••99"}, owner.ID,
	)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("upsert connection: %v", err)
	}

	// Enqueue job
	job, _, err := migratorStore.EnqueueChannelSyncJob(ctx, tx, domain.ChannelSyncJob{
		PropertyID:          prop.ID,
		ChannelConnectionID: rec.Connection.ID,
		ProviderCode:        "beds24",
		JobType:             domain.ChannelJobPullReservations,
		IdempotencyKey:      "fence-test-1",
	})
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("enqueue job: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Worker A claims job (fence_epoch = 1)
	claimedA, err := migratorStore.ClaimDueChannelSyncJobs(ctx, "worker-a", 5, 30*time.Second)
	if err != nil || len(claimedA) != 1 || claimedA[0].FenceEpoch != 1 {
		t.Fatalf("expected worker-a to claim fence_epoch=1, got %+v err=%v", claimedA, err)
	}

	// Expire lease in DB
	_, _ = migratorStore.Pool().Exec(ctx, `
		UPDATE channel_sync_jobs SET lease_expires_at = NOW() - INTERVAL '5 seconds' WHERE id = $1
	`, job.ID)

	// Worker B reclaims stale running job (fence_epoch = 2)
	claimedB, err := migratorStore.ClaimDueChannelSyncJobs(ctx, "worker-b", 5, 30*time.Second)
	if err != nil || len(claimedB) != 1 || claimedB[0].FenceEpoch != 2 {
		t.Fatalf("expected worker-b to reclaim fence_epoch=2, got %+v err=%v", claimedB, err)
	}

	// Stale Worker A renewal & completion must BOTH fail closed
	renewedA, _ := migratorStore.RenewChannelSyncJobLease(ctx, claimedA[0].ID, "worker-a", *claimedA[0].LeaseToken, claimedA[0].FenceEpoch, 30*time.Second)
	completedA, _ := migratorStore.CompleteChannelSyncJob(ctx, claimedA[0].ID, "worker-a", *claimedA[0].LeaseToken, claimedA[0].FenceEpoch, domain.ChannelJobSucceeded, nil, "", "")
	if renewedA || completedA {
		t.Fatalf("stale worker-a must not renew (%v) or complete (%v) fenced job", renewedA, completedA)
	}

	// Active Worker B succeeds cleanly
	completedB, err := migratorStore.CompleteChannelSyncJob(ctx, claimedB[0].ID, "worker-b", *claimedB[0].LeaseToken, claimedB[0].FenceEpoch, domain.ChannelJobSucceeded, nil, "", "")
	if err != nil || !completedB {
		t.Fatalf("worker-b completion failed: ok=%v err=%v", completedB, err)
	}

	// Cross-property trigger isolation test
	propBID := uuid.New()
	propB, err := migratorStore.CreateProperty(ctx, nil, domain.Property{
		ID:           propBID,
		Slug:         "other-meadow-" + propBID.String()[:8],
		Name:         "Other Meadow Campsite",
		CountryCode:  "GB",
		Timezone:     "Europe/London",
		Currency:     "GBP",
		CheckInTime:  "15:00",
		CheckOutTime: "10:30",
	})
	if err != nil {
		t.Fatalf("create second property: %v", err)
	}
	var utB domain.UnitType
	if err := migratorStore.WithTx(ctx, func(tx pgx.Tx) error {
		var txErr error
		utB, txErr = migratorStore.CreateUnitType(ctx, tx, domain.UnitType{
			ID:                   uuid.New(),
			PropertyID:           propB.ID,
			Code:                 "cross-ut",
			Name:                 "Cross Property Unit Type",
			Category:             domain.CategoryTentPitch,
			BaseCapacityAdults:   2,
			MaxAdults:            2,
			MaxGuests:            2,
			BaseNightlyRatePence: 3000,
			MinNights:            1,
			MaxNights:            14,
			IsActive:             true,
			SortOrder:            10,
		})
		return txErr
	}); err != nil {
		t.Fatalf("create second property unit type: %v", err)
	}
	errCross := migratorStore.WithTx(ctx, func(tx pgx.Tx) error {
		_, e := migratorStore.UpsertChannelListingMapping(ctx, tx, domain.ChannelListingMapping{
			PropertyID:          prop.ID,
			ChannelConnectionID: rec.Connection.ID,
			ProviderCode:        "beds24",
			ExternalListingID:   "cross-999",
			UnitTypeID:          utB.ID,
			BookingChannelCode:  "DIRECT",
			Currency:            "GBP",
			Timezone:            "Europe/London",
			OwnershipMode:       domain.ChannelOwnershipLovelaceARI,
			IsActive:            true,
		}, owner.ID)
		return e
	})
	if errCross == nil {
		t.Fatal("expected cross-property unit_type_id mapping to be rejected by DB trigger")
	}

	// Custom provider URL rejection (anti-SSRF public config guard)
	if err := service.ValidateChannelPublicURLSafe(map[string]string{"base_url": "http://169.254.169.254/latest"}); err == nil {
		t.Fatal("expected ValidateChannelPublicURLSafe to reject non-HTTPS custom base_url")
	}
	if err := service.ValidateChannelPublicURLSafe(map[string]string{"endpoint": "file:///etc/passwd"}); err == nil {
		t.Fatal("expected ValidateChannelPublicURLSafe to reject file:// endpoint")
	}

	// Property-scoped manager on prop cannot access propB channel endpoints over HTTP (403 Forbidden)
	mgrHash, err := auth.HashPassword("tirbach-manager-password-2026")
	if err != nil {
		t.Fatalf("hash manager password: %v", err)
	}
	if _, err := migratorStore.CreateAdminUser(ctx, nil, domain.AdminUser{
		ID:           uuid.New(),
		PropertyID:   &prop.ID,
		Email:        "mgr-prop-a@tirbach.wales",
		FullName:     "Gareth Davies",
		Role:         domain.RoleManager,
		PasswordHash: mgrHash,
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create manager user: %v", err)
	}
	mgrClient := newClient(t)
	mgrStatus, mgrCSRF := apiLogin(t, mgrClient, srv.URL, "mgr-prop-a@tirbach.wales", "tirbach-manager-password-2026")
	if mgrStatus != http.StatusOK {
		t.Fatalf("manager login status=%d", mgrStatus)
	}
	crossHTTPResp := doJSON(t, mgrClient, http.MethodPost, srv.URL+"/api/v1/properties/"+propB.ID.String()+"/integrations/channel-manager/beds24/verify", mgrCSRF, nil)
	crossHTTPResp.Body.Close()
	if crossHTTPResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for cross-property HTTP channel verify, got %d", crossHTTPResp.StatusCode)
	}
}
