package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/domain"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
)

func TestPhase332FinalAcceptance_DirectDBAllowlistSecurity(t *testing.T) {
	srv, store, svc, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-accept-sec")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{
		UserID: owner.ID, PropertyID: &prop.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role,
	}

	staffUser, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "accept-staff@example.test", FullName: "AcceptStaff", Role: domain.RoleStaff, PropertyID: &prop.ID, Password: "accept-staff-pass-2026",
	})
	if err != nil {
		t.Fatalf("create staff user: %v", err)
	}
	inactiveOwner, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "accept-inactive@example.test", FullName: "AcceptInactive", Role: domain.RoleOwnerAdmin, PropertyID: &prop.ID, Password: "accept-inactive-pass-2026",
	})
	if err != nil {
		t.Fatalf("create inactive owner: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE admin_users SET is_active = FALSE WHERE id = $1`, inactiveOwner.ID); err != nil {
		t.Fatalf("deactivate owner: %v", err)
	}
	rotMgr, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "accept-rotmgr@example.test", FullName: "AcceptRotMgr", Role: domain.RoleManager, PropertyID: &prop.ID, Password: "accept-rotmgr-pass-2026",
	})
	if err != nil {
		t.Fatalf("create rotating manager: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE admin_users SET must_rotate_password = TRUE WHERE id = $1`, rotMgr.ID); err != nil {
		t.Fatalf("mark rotMgr must_rotate_password: %v", err)
	}
	otherProp, err := store.CreateProperty(ctx, nil, domain.Property{
		Slug: "accept-other-prop", Name: "Accept Other Prop", CountryCode: "GB", Timezone: "Europe/London", Currency: "GBP",
		ContactEmail: "other-accept@example.test", CheckInTime: "15:00", CheckOutTime: "10:00",
	})
	if err != nil {
		t.Fatalf("create other prop: %v", err)
	}
	otherOwner, err := svc.CreateUser(ctx, ownerActor, service.CreateUserInput{
		Email: "accept-other-owner@example.test", FullName: "OtherOwner", Role: domain.RoleOwnerAdmin, PropertyID: &otherProp.ID, Password: "accept-other-pass-2026",
	})
	if err != nil {
		t.Fatalf("create other owner: %v", err)
	}

	seedAuditRow := func(resourceType string) int64 {
		t.Helper()
		var id int64
		if qErr := store.Pool().QueryRow(ctx, `
			INSERT INTO audit_logs (property_id, actor_email, actor_role, action, resource_type, resource_id, request_id, before_state, after_state, metadata)
			VALUES ($1, 'legacy@raw.test', 'PUBLIC', $2, $3, $4, 'req-accept-sec',
			        '{"guest_name":"Legacy Secret","email":"legacy@raw.test"}'::jsonb,
			        '{"phone":"07700 900123"}'::jsonb,
			        '{"notes":"Private raw memo"}'::jsonb)
			RETURNING id
		`, prop.ID, resourceType+".legacy", resourceType, uuid.NewString()).Scan(&id); qErr != nil {
			t.Fatalf("seed audit row (%s): %v", resourceType, qErr)
		}
		return id
	}

	guestAuditID := seedAuditRow("guest")
	resAuditID := seedAuditRow("reservation")
	blockoutAuditID := seedAuditRow("blockout_group")
	maintAuditID := seedAuditRow("maintenance_request")
	auditLogAuditID := seedAuditRow("audit_log")
	unsupportedAuditID := seedAuditRow("unsupported_custom_widget")

	appURL := openSignoffAppRoleURL(t, store)
	appPool, err := pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatalf("connect lovelace_app: %v", err)
	}
	defer appPool.Close()

	// Build oversized payloads for boundary tests.
	hugeKeysMap := map[string]any{"redacted": true}
	for i := 0; i < 42; i++ {
		hugeKeysMap[fmt.Sprintf("k_%02d", i)] = true
	}
	hugeKeysJSON, _ := json.Marshal(hugeKeysMap)

	hugeArrayUUIDs := make([]string, 66)
	for i := range hugeArrayUUIDs {
		hugeArrayUUIDs[i] = uuid.NewString()
	}
	hugeArrayJSON, _ := json.Marshal(map[string]any{"unit_ids": hugeArrayUUIDs})

	longString := strings.Repeat("a", 129)
	longStrJSON, _ := json.Marshal(map[string]any{"reference": longString})

	deepObjJSON := `{"group":{"nested1":{"nested2":{"nested3":{"nested4":{"id":"` + uuid.NewString() + `"}}}}}}`

	// 1. Direct DB calls as lovelace_app must reject all disallowed JSON payloads across all 3 slots with SQLSTATE 22023.
	negativeCases := []struct {
		name      string
		auditID   int64
		payload   string
		leakToken string
	}{
		{"unknown key with name", guestAuditID, `{"custom_label":"Alice Smith"}`, "Alice Smith"},
		{"unknown key with private note", resAuditID, `{"private_memo":"VIP guest called front desk"}`, "private_memo"},
		{"unknown key with email", guestAuditID, `{"contact":"alice@example.com"}`, "alice@example.com"},
		{"unknown key with uk phone", guestAuditID, `{"callback":"07700 900123"}`, "07700 900123"},
		{"unknown key with intl phone", guestAuditID, `{"intl_callback":"+44 7700 900123"}`, "+44"},
		{"nested object email in moved_counts", guestAuditID, `{"moved_counts":{"email":"leak@example.test"}}`, "leak@example.test"},
		{"nested object phone in group", blockoutAuditID, `{"group":{"phone":"07700900123"}}`, "07700900123"},
		{"phone disguised as JSON number in moved_counts", guestAuditID, `{"moved_counts":{"reservations":447700900123}}`, "447700900123"},
		{"phone disguised as JSON number in total_pence", resAuditID, `{"total_pence":447700900123}`, "447700900123"},
		{"phone disguised as JSON number in version", guestAuditID, `{"version":7700900123}`, "7700900123"},
		{"phone disguised as JSON number in target_audit_log_id", auditLogAuditID, `{"target_audit_log_id":447700900123}`, "447700900123"},
		{"phone string disguised inside code/reference", resAuditID, `{"reference":"tel_07700900123"}`, "07700900123"},
		{"explicit PII key guest_email", resAuditID, `{"guest_email":"alice@example.com"}`, "guest_email"},
		{"explicit PII key notes", guestAuditID, `{"notes":"Private confidential note"}`, "Private confidential"},
		{"free-text string under allowed enum status", resAuditID, `{"status":"Alice Smith"}`, "Alice Smith"},
		{"free-text string under allowed code reference", resAuditID, `{"reference":"VIP guest called"}`, "VIP guest"},
		{"invalid UUID under guest_id", resAuditID, `{"guest_id":"not-a-uuid"}`, "not-a-uuid"},
		{"unsupported resource_type fails closed", unsupportedAuditID, `{"redacted":true}`, "unsupported_custom_widget"},
		{"unknown nested key in moved_counts", guestAuditID, `{"moved_counts":{"reservations":1,"secret_extra":2}}`, "secret_extra"},
		{"unknown nested key reason in blockout group", blockoutAuditID, fmt.Sprintf(`{"group":{"id":"%s","reason":"Private outage note"}}`, uuid.NewString()), "Private outage"},
		{"unknown nested key inspector_name in inspection", maintAuditID, fmt.Sprintf(`{"inspection":{"id":"%s","inspector_name":"Bob Jones"}}`, uuid.NewString()), "Bob Jones"},
		{"invalid element in unit_ids uuid array", resAuditID, `{"unit_ids":["07700900123"]}`, "07700900123"},
		{"invalid enum value in changed_fields", guestAuditID, `{"changed_fields":["secret_note"]}`, "secret_note"},
		{"wrong JSON type string for integer version", guestAuditID, `{"version":"1"}`, ""},
		{"wrong JSON type fractional float for integer version", guestAuditID, `{"version":1.5}`, ""},
		{"wrong JSON type number for enum status", resAuditID, `{"status":123}`, ""},
		{"wrong JSON type string for boolean has_email", guestAuditID, `{"has_email":"true"}`, ""},
		{"wrong JSON type false for redacted marker", guestAuditID, `{"redacted":false}`, ""},
		{"wrong JSON type top-level array", guestAuditID, `["id"]`, ""},
		{"wrong JSON type top-level JSON null literal", guestAuditID, `null`, ""},
		{"excessive nesting depth", blockoutAuditID, deepObjJSON, ""},
		{"excessive object key count", guestAuditID, string(hugeKeysJSON), ""},
		{"excessive array length", resAuditID, string(hugeArrayJSON), ""},
		{"excessive string length", resAuditID, string(longStrJSON), longString},
		{"control character newline in code", resAuditID, `{"reference":"TB-1\nINJECT"}`, "INJECT"},
	}

	assertNoPIIEcho := func(err error, label, leak string) {
		t.Helper()
		if leak == "" || err == nil {
			return
		}
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("%s: PG error message echoed sensitive input %q: %v", label, leak, err)
		}
	}

	for _, tc := range negativeCases {
		var rid uuid.UUID
		bErr := appPool.QueryRow(ctx, `SELECT public.redact_audit_log_entry($1, $2, $3::jsonb, NULL, '{"redacted":true}'::jsonb, 'Authorized compliance cleanup', $4)`,
			uuid.New(), tc.auditID, tc.payload, owner.ID).Scan(&rid)
		assertSignoffPGCode(t, bErr, "22023", "before_state "+tc.name)
		assertNoPIIEcho(bErr, "before_state "+tc.name, tc.leakToken)

		aErr := appPool.QueryRow(ctx, `SELECT public.redact_audit_log_entry($1, $2, NULL, $3::jsonb, '{"redacted":true}'::jsonb, 'Authorized compliance cleanup', $4)`,
			uuid.New(), tc.auditID, tc.payload, owner.ID).Scan(&rid)
		assertSignoffPGCode(t, aErr, "22023", "after_state "+tc.name)
		assertNoPIIEcho(aErr, "after_state "+tc.name, tc.leakToken)

		mErr := appPool.QueryRow(ctx, `SELECT public.redact_audit_log_entry($1, $2, NULL, NULL, $3::jsonb, 'Authorized compliance cleanup', $4)`,
			uuid.New(), tc.auditID, tc.payload, owner.ID).Scan(&rid)
		assertSignoffPGCode(t, mErr, "22023", "metadata "+tc.name)
		assertNoPIIEcho(mErr, "metadata "+tc.name, tc.leakToken)
	}

	// 2. Permission denials under lovelace_app MUST assert SQLSTATE 42501 cleanly.
	permCases := []struct {
		name    string
		actorID uuid.UUID
		auditID int64
	}{
		{"staff role", staffUser.ID, guestAuditID},
		{"inactive owner", inactiveOwner.ID, guestAuditID},
		{"must_rotate_password=true manager", rotMgr.ID, guestAuditID},
		{"foreign property owner", otherOwner.ID, guestAuditID},
	}
	for _, pc := range permCases {
		var rid uuid.UUID
		pErr := appPool.QueryRow(ctx, `
			SELECT public.redact_audit_log_entry($1, $2, NULL, NULL, '{"redacted":true}'::jsonb, 'Authorized compliance cleanup', $3)
		`, uuid.New(), pc.auditID, pc.actorID).Scan(&rid)
		assertSignoffPGCode(t, pErr, "42501", "permission denial "+pc.name)
	}

	// Direct table access by lovelace_app must also fail with SQLSTATE 42501.
	for _, dq := range []struct {
		name string
		sql  string
	}{
		{"direct INSERT audit_log_redactions", `INSERT INTO public.audit_log_redactions (id, audit_log_id, reason) VALUES ('` + uuid.NewString() + `', 1, 'bypass')`},
		{"direct SELECT audit_logs", `SELECT COUNT(*) FROM public.audit_logs`},
		{"direct UPDATE audit_logs", `UPDATE public.audit_logs SET action = 'tampered'`},
	} {
		_, err := appPool.Exec(ctx, dq.sql)
		assertSignoffPGCode(t, err, "42501", dq.name)
	}

	// 3. Defense-in-depth BEFORE INSERT trigger on audit_log_redactions blocks privileged direct inserts with SQLSTATE 22023.
	triggerNegativeCases := []struct {
		name    string
		auditID int64
		before  string
	}{
		{"trigger unknown key", guestAuditID, `{"unapproved":"secret"}`},
		{"trigger phone as JSON number", resAuditID, `{"total_pence":447700900123}`},
		{"trigger wrong type", resAuditID, `{"nights":"5"}`},
		{"trigger unsupported resource_type", unsupportedAuditID, `{"redacted":true}`},
	}
	for _, tnc := range triggerNegativeCases {
		_, trigErr := store.Pool().Exec(ctx, `
			INSERT INTO audit_log_redactions (id, audit_log_id, property_id, redacted_before_state, reason, reason_category, redacted_by)
			VALUES ($1, $2, $3, $4::jsonb, 'Clean reason for trigger check', 'HISTORICAL_PII_MASK', $5)
		`, uuid.New(), tnc.auditID, prop.ID, tnc.before, owner.ID)
		assertSignoffPGCode(t, trigErr, "22023", tnc.name)
	}
	_ = clk
}

func TestPhase332FinalAcceptance_AllResourceTypesRedactionAndImmutability(t *testing.T) {
	srv, store, _, prop, _, _, clk := setupPhase2TestServer(t, "pub-p332-accept-alltypes")
	defer srv.Close()

	ctx := context.Background()
	owner, err := store.GetAdminUserByEmail(ctx, nil, "owner@tirbach.wales")
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	ownerActor := service.Actor{
		UserID: owner.ID, PropertyID: &prop.ID, Email: owner.Email, FullName: owner.FullName, Role: owner.Role,
	}

	appURL := openSignoffAppRoleURL(t, store)
	appStore, err := postgres.New(ctx, appURL)
	if err != nil {
		t.Fatalf("postgres.New as lovelace_app: %v", err)
	}
	defer appStore.Close()

	appSvc := service.New(appStore, config.Config{
		AppEnv: "test", DatabaseURL: appURL, EncryptionKey: testEncryptionKey,
		SessionTTL: time.Hour, SessionIdleTTL: 30 * time.Minute, EmailFrom: "bookings@tirbach.wales",
	})
	appSvc.SetClock(clk.Now)

	nowStr := clk.Now().UTC().Format(time.RFC3339Nano)
	uid := uuid.NewString()
	uid2 := uuid.NewString()
	sumBytes := sha256.Sum256([]byte("acceptance-attachment"))
	validSHA := hex.EncodeToString(sumBytes[:])

	samplePayloads := map[string]map[string]any{
		"admin_session": {
			"session_id": uid, "user_id": owner.ID.String(), "property_id": prop.ID.String(),
			"created_at": nowStr, "expires_at": nowStr,
		},
		"admin_user": {
			"user_id": owner.ID.String(), "role": "owner_admin", "is_active": true,
			"must_rotate_password": false, "property_id": prop.ID.String(), "version": 1,
		},
		"audit_log": {
			"redaction_id": uid, "target_audit_log_id": 1, "target_resource_type": "guest",
			"target_resource_id": uid2, "redacted_at": nowStr,
		},
		"availability_block": {
			"id": uid, "property_id": prop.ID.String(), "unit_id": uid2, "unit_code": "POD-01",
			"start_date": "2026-06-01", "end_date": "2026-06-03", "block_type": "maintenance",
			"created_at": nowStr, "updated_at": nowStr,
		},
		"availability_release_rule": {
			"id": uid, "property_id": prop.ID.String(), "scope_type": "PROPERTY",
			"start_date": "2026-06-01", "end_date": "2026-06-10", "release_at": nowStr,
			"is_active": true, "version": 1, "created_at": nowStr, "updated_at": nowStr,
		},
		"blockout_group": {
			"group": map[string]any{
				"id": uid, "property_id": prop.ID.String(), "scope_type": "UNIT_TYPE",
				"target_unit_type_id": uid2, "category": "MAINTENANCE", "channel_scope": "ALL",
				"start_date": "2026-06-01", "end_date": "2026-06-02", "apply_to_future_units": false,
				"status": "ACTIVE", "active_claim_count": 1, "consumed_claim_count": 0,
				"released_claim_count": 0, "total_claim_count": 1, "version": 1,
				"created_at": nowStr, "updated_at": nowStr,
			},
			"unit_count": 1,
		},
		"booking_channel": {
			"id": uid, "property_id": prop.ID.String(), "code": "DIRECT", "category": "DIRECT",
			"is_active": true, "sort_order": 1, "created_at": nowStr, "updated_at": nowStr,
		},
		"booking_hold": {
			"hold_id": uid, "hold_reference": "HLD-2026-0001", "guest_id": uid2,
			"unit_type_id": uuid.NewString(), "rate_plan_id": uuid.NewString(),
			"channel_code": "DIRECT", "arrival_date": "2026-07-01", "departure_date": "2026-07-04",
			"adults": 2, "children": 1, "infants": 0, "terms_version": "v1",
			"status": "ACTIVE", "expires_at": nowStr, "version": 1,
		},
		"channel_connection": {
			"id": uid, "property_id": prop.ID.String(), "provider_code": "beds24",
			"auth_method": "API_KEY", "status": "CONFIGURED", "has_credentials": true,
			"has_live_adapter": true, "version": 1, "created_at": nowStr, "updated_at": nowStr,
		},
		"channel_external_reservation": {
			"id": uid, "property_id": prop.ID.String(), "channel_connection_id": uid2,
			"provider_code": "beds24", "external_reservation_id": "EXT-101",
			"import_status": "IMPORTED", "created_at": nowStr, "updated_at": nowStr,
		},
		"channel_listing_mapping": {
			"id": uid, "property_id": prop.ID.String(), "channel_connection_id": uid2,
			"provider_code": "beds24", "external_listing_id": "101",
			"unit_type_id": uid2, "booking_channel_code": "AIRBNB",
			"currency": "GBP", "timezone": "Europe/London",
			"sync_availability": true, "sync_rates": true, "sync_reservations": true,
			"ownership_mode": "LOVELACE_ARI_PROVIDER_BOOKINGS", "is_active": true,
			"version": 1, "created_at": nowStr, "updated_at": nowStr,
		},
		"channel_sync_job": {
			"id": uid, "property_id": prop.ID.String(), "channel_connection_id": uid2,
			"provider_code": "beds24", "job_type": "PUSH_RATES", "status": "SUCCEEDED",
			"attempts": 1, "max_attempts": 5, "fence_epoch": 1,
			"created_at": nowStr, "updated_at": nowStr,
		},
		"channel_webhook_event": {
			"id": uid, "property_id": prop.ID.String(), "channel_connection_id": uid2,
			"provider_code": "channex", "external_event_id": "evt-1", "event_type": "booking",
			"signature_verified": true, "status": "QUEUED", "received_at": nowStr,
		},
		"communication_automation_rule": {
			"id": uid, "property_id": prop.ID.String(), "trigger_event": "PRE_ARRIVAL",
			"offset_hours": -24, "send_hour_local": 9, "template_id": uid2,
			"applicable_statuses": []string{"CONFIRMED"}, "is_active": true,
			"created_at": nowStr, "updated_at": nowStr,
		},
		"communication_template": {
			"id": uid, "property_id": prop.ID.String(), "code": "welcome_email",
			"category": "PRE_ARRIVAL", "locale": "en-GB", "is_active": true,
			"version": 1, "created_at": nowStr, "updated_at": nowStr,
		},
		"confirmation_access_token": {
			"reference": "TB-2026-0001",
		},
		"email_outbox": {
			"actor_id": owner.ID.String(), "reconciled_at": nowStr, "resolution": "CONFIRMED_SENT",
			"reason_code": "MTA_PROOF", "evidence_type": "MTA_ACCEPTED", "evidence_ref": "mta-msg-1",
			"status": "SENT",
		},
		"extra": {
			"id": uid, "property_id": prop.ID.String(), "code": "FIREWOOD", "pricing_model": "per_stay",
			"price_pence": 1200, "tax_rate_bps": 2000, "max_quantity": 5, "is_active": true,
			"sort_order": 1, "version": 1, "created_at": nowStr, "updated_at": nowStr,
		},
		"guest": {
			"guest_id": uid, "status": "ACTIVE", "preferred_contact_method": "EMAIL",
			"preferred_language": "en", "has_email": true, "has_phone": true, "has_notes": true,
			"changed_fields": []string{"email", "name"}, "version": 2,
			"moved_counts": map[string]any{"reservations": 1, "booking_holds": 1, "consent_records": 1},
		},
		"guest_communication": {
			"communication_id": uid, "guest_id": uid2, "interaction_type": "EMAIL_OUTBOUND",
			"direction": "OUTBOUND", "delivery_status": "DELIVERED", "follow_up_required": false,
			"occurred_at": nowStr,
		},
		"guest_enquiry": {
			"enquiry_id": uid, "reference": "ENQ-2026-0001", "guest_id": uid2,
			"category": "COMPLAINT", "status": "IN_PROGRESS", "priority": "HIGH",
			"source": "EMAIL", "version": 1,
		},
		"guest_note": {
			"note_id": uid, "guest_id": uid2, "category": "OPERATIONAL", "is_pinned": true,
		},
		"guest_preference": {
			"preference_id": uid, "guest_id": uid2, "category": "PET", "has_notes": true,
		},
		"housekeeping_task": {
			"id": uid, "property_id": prop.ID.String(), "unit_id": uid2, "task_type": "CHANGEOVER_CLEAN",
			"stage": "READY", "priority": "NORMAL", "scheduled_date": "2026-06-05",
			"same_day_arrival": true, "has_active_block": false, "overdue": false,
			"inspected_at": nowStr, "inspected_by": owner.ID.String(),
			"version": 2, "created_at": nowStr, "updated_at": nowStr,
		},
		"maintenance_asset": {
			"id": uid, "property_id": prop.ID.String(), "asset_code": "A-BOIL-01",
			"status": "ACTIVE", "version": 1, "created_at": nowStr, "updated_at": nowStr,
		},
		"maintenance_attachment": {
			"id": uid, "property_id": prop.ID.String(), "request_id": uid2,
			"content_type": "image/png", "size_bytes": 4096, "sha256": validSHA, "created_at": nowStr,
		},
		"maintenance_category": {
			"id": uid, "property_id": prop.ID.String(), "code": "PLUMBING",
			"is_active": true, "sort_order": 1, "created_at": nowStr, "updated_at": nowStr,
		},
		"maintenance_contractor": {
			"id": uid, "property_id": prop.ID.String(), "is_active": true,
			"created_at": nowStr, "updated_at": nowStr,
		},
		"maintenance_location": {
			"id": uid, "property_id": prop.ID.String(), "code": "SITE-MAIN", "location_type": "FACILITY",
			"is_active": true, "sort_order": 1, "created_at": nowStr, "updated_at": nowStr,
		},
		"maintenance_request": {
			"request": map[string]any{
				"id": uid, "property_id": prop.ID.String(), "reference": "MNT-2026-0001",
				"category_id": uid2, "priority": "HIGH", "status": "COMPLETED",
				"work_type": "REACTIVE", "reported_at": nowStr, "guest_impact": false,
				"safety_impact": false, "availability_impact": "NONE", "inspection_required": true,
				"overdue": false, "has_active_restriction": false, "latest_inspection_result": "PASSED",
				"version": 2, "created_at": nowStr, "updated_at": nowStr,
			},
			"inspection": map[string]any{
				"id": uuid.NewString(), "property_id": prop.ID.String(), "request_id": uid,
				"inspector_id": owner.ID.String(), "result": "PASSED", "inspected_at": nowStr,
			},
		},
		"maintenance_schedule": {
			"id": uid, "property_id": prop.ID.String(), "category_id": uid2,
			"priority": "NORMAL", "frequency": "MONTHLY", "next_due_date": "2026-07-01",
			"anchor_day": 1, "inspection_required": true, "is_active": true,
			"version": 1, "created_at": nowStr, "updated_at": nowStr,
		},
		"payment_exception": {
			"id": uid, "property_id": prop.ID.String(), "exception_type": "HOLD_EXPIRED",
			"amount_pence": 12500, "currency": "GBP", "status": "OPEN", "created_at": nowStr,
		},
		"payment_webhook_event": {
			"event_id": "evt_test_livemode_1", "event_type": "checkout.session.completed",
			"event_livemode": false, "expected_livemode": true, "environment": "production",
			"payment_attempt": uid,
		},
		"property": {
			"id": prop.ID.String(), "slug": "tir-bach", "country_code": "GB",
			"timezone": "Europe/London", "currency": "GBP", "check_in_time": "15:00",
			"check_out_time": "10:00", "vat_registered": true, "version": 1,
			"created_at": nowStr, "updated_at": nowStr,
		},
		"property_booking_policy": {
			"property_id": prop.ID.String(), "hold_duration_minutes": 30, "deposit_mode": "PERCENTAGE",
			"deposit_percentage": 25, "deposit_fixed_pence": 0, "balance_due_days_before_arrival": 14,
			"extra_adult_nightly_pence": 1500, "child_nightly_pence": 500, "infant_nightly_pence": 0,
			"booking_terms_version": "v1", "stripe_enabled": false, "version": 1, "updated_at": nowStr,
		},
		"property_retention_policy": {
			"property_id": prop.ID.String(), "enquiry_retention_days": 365,
			"inactive_guest_retention_days": 1095, "comms_retention_days": 365,
			"auto_anonymize_enabled": false, "execution_mode": "MANUAL", "updated_at": nowStr,
		},
		"rate_plan": {
			"id": uid, "property_id": prop.ID.String(), "code": "STANDARD",
			"is_default": true, "is_active": true, "priority": 1, "version": 1,
			"created_at": nowStr, "updated_at": nowStr,
		},
		"rate_rule": {
			"id": uid, "property_id": prop.ID.String(), "rate_plan_id": uid2, "unit_type_id": uuid.NewString(),
			"start_date": "2026-07-01", "end_date": "2026-08-31", "days_of_week": 127,
			"nightly_rate_pence": 14500, "min_nights_override": 2, "priority": 10,
			"is_active": true, "version": 1, "created_at": nowStr, "updated_at": nowStr,
		},
		"report_export": {
			"id": uid, "property_id": prop.ID.String(), "export_type": "booking_line_items",
			"format": "csv", "start_date": "2026-07-01", "end_date": "2026-07-08",
			"currency": "GBP", "row_count": 4, "created_at": nowStr,
		},
		"reservation": {
			"reservation_id": uid, "reference": "TB-2026-0099", "guest_id": uid2,
			"channel_code": "DIRECT", "arrival_date": "2026-07-01", "departure_date": "2026-07-04",
			"nights": 3, "adults": 2, "children": 0, "infants": 0,
			"status": "CONFIRMED", "payment_status": "PAID", "total_pence": 36000,
			"deposit_pence": 12000, "amount_paid_pence": 36000, "balance_due_pence": 0,
			"currency": "GBP", "unit_ids": []string{uuid.NewString()}, "version": 2,
		},
		"unit": {
			"id": uid, "property_id": prop.ID.String(), "unit_type_id": uid2,
			"code": "POD-01", "status": "active", "sort_order": 1, "version": 1,
			"created_at": nowStr, "updated_at": nowStr,
		},
		"unit_type": {
			"id": uid, "property_id": prop.ID.String(), "code": "POD", "category": "glamping",
			"base_capacity_adults": 2, "max_adults": 4, "max_children": 2, "max_infants": 1,
			"max_guests": 4, "pets_allowed": true, "max_pets": 2,
			"base_nightly_rate_pence": 12000, "tax_rate_bps": 2000, "min_nights": 1,
			"max_nights": 14, "is_active": true, "is_public_bookable": true, "sort_order": 1,
			"version": 1, "created_at": nowStr, "updated_at": nowStr,
		},
	}

	supportedTypes := service.SupportedAuditResourceTypes()
	if len(supportedTypes) != 42 {
		t.Fatalf("expected 42 supported audit resource types, got %d (%v)", len(supportedTypes), supportedTypes)
	}

	for _, rt := range supportedTypes {
		rt := rt
		t.Run("resource_"+rt, func(t *testing.T) {
			sample, ok := samplePayloads[rt]
			if !ok {
				t.Fatalf("missing test sample payload for resource_type %q", rt)
			}
			// Inject dirty legacy PII and unallowlisted free-text alongside valid structural fields.
			dirtyBefore := make(map[string]any, len(sample)+5)
			for k, v := range sample {
				dirtyBefore[k] = v
			}
			dirtyBefore["guest_name"] = "Legacy Raw Name"
			dirtyBefore["guest_email"] = "legacy@example.test"
			dirtyBefore["phone"] = "07700 900123"
			dirtyBefore["notes"] = "Sensitive historical complaint note"
			dirtyBefore["unknown_legacy_col"] = "free text leak"

			beforeBytes, _ := json.Marshal(dirtyBefore)
			afterBytes, _ := json.Marshal(sample)
			metaBytes := []byte(`{"ip_address":"192.0.2.55","user_agent":"SecretUA","custom_note":"Alice called 07700 900123"}`)

			var auditID int64
			if err := store.Pool().QueryRow(ctx, `
				INSERT INTO audit_logs (property_id, actor_user_id, actor_email, actor_role, action, resource_type, resource_id, request_id, ip_address, user_agent, before_state, after_state, metadata)
				VALUES ($1, $2, 'legacy.actor@example.test', 'owner_admin', $3, $4, $5, 'req-alltypes', '192.0.2.55', 'SecretUA', $6::jsonb, $7::jsonb, $8::jsonb)
				RETURNING id
			`, prop.ID, owner.ID, rt+".legacy_event", rt, uid, string(beforeBytes), string(afterBytes), string(metaBytes)).Scan(&auditID); err != nil {
				t.Fatalf("seed historical audit_log for %s: %v", rt, err)
			}

			redactedEntry, err := appSvc.RedactHistoricalAuditLog(ctx, ownerActor, prop.ID, auditID, "Phase 3.2 acceptance redaction for "+rt)
			if err != nil {
				t.Fatalf("RedactHistoricalAuditLog(%s) failed: %v", rt, err)
			}
			if !redactedEntry.IsRedacted || redactedEntry.ActorEmail != "redacted@redacted.lovelace" || redactedEntry.IPAddress != "" || redactedEntry.UserAgent != "" {
				t.Fatalf("unexpected effective masked fields for %s: %+v", rt, redactedEntry)
			}

			combined := string(redactedEntry.BeforeState) + " " + string(redactedEntry.AfterState) + " " + string(redactedEntry.Metadata)
			for _, forbidden := range []string{"Legacy Raw Name", "legacy@example.test", "07700 900123", "unknown_legacy_col", "SecretUA", "192.0.2.55", "custom_note"} {
				if strings.Contains(combined, forbidden) {
					t.Fatalf("%s effective redacted view retained forbidden token %q: %s", rt, forbidden, combined)
				}
			}
			var metaMap, beforeMap, afterMap map[string]any
			if err := json.Unmarshal(redactedEntry.Metadata, &metaMap); err != nil || metaMap["redacted"] != true || len(metaMap) != 1 {
				t.Fatalf("%s expected sanitized redacted metadata marker only, got %s (err=%v)", rt, string(redactedEntry.Metadata), err)
			}
			if err := json.Unmarshal(redactedEntry.BeforeState, &beforeMap); err != nil || beforeMap["redacted"] != true || len(beforeMap) < 2 {
				t.Fatalf("%s expected sanitized before_state preserving structural keys + redacted marker, got %s (err=%v)", rt, string(redactedEntry.BeforeState), err)
			}
			if err := json.Unmarshal(redactedEntry.AfterState, &afterMap); err != nil || len(afterMap) == 0 {
				t.Fatalf("%s expected sanitized after_state preserving structural keys, got %s (err=%v)", rt, string(redactedEntry.AfterState), err)
			}

			// Verify raw audit_logs row stays intact and immutable (overlay only).
			var rawBefore string
			if err := store.Pool().QueryRow(ctx, `SELECT before_state::text FROM audit_logs WHERE id = $1`, auditID).Scan(&rawBefore); err != nil {
				t.Fatalf("query raw audit_logs %s: %v", rt, err)
			}
			if !strings.Contains(rawBefore, "Legacy Raw Name") {
				t.Fatalf("%s raw audit_logs row was unexpectedly altered: %s", rt, rawBefore)
			}
			_, updErr := store.Pool().Exec(ctx, `UPDATE audit_logs SET metadata = '{}' WHERE id = $1`, auditID)
			if updErr == nil {
				t.Fatalf("%s expected UPDATE on raw audit_logs to fail immutability trigger", rt)
			}
		})
	}

	// Unsupported resource_type via service layer must fail closed with ErrValidation.
	var badTypeID int64
	if err := store.Pool().QueryRow(ctx, `
		INSERT INTO audit_logs (property_id, actor_user_id, actor_email, actor_role, action, resource_type, resource_id, before_state)
		VALUES ($1, $2, 'legacy@example.test', 'owner_admin', 'weird.action', 'totally_unsupported_type', $3, '{"x":1}'::jsonb)
		RETURNING id
	`, prop.ID, owner.ID, uuid.NewString()).Scan(&badTypeID); err != nil {
		t.Fatalf("seed unsupported audit row: %v", err)
	}
	if _, err := appSvc.RedactHistoricalAuditLog(ctx, ownerActor, prop.ID, badTypeID, "Should fail closed"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected ErrValidation for unsupported resource_type, got %v", err)
	}
}
