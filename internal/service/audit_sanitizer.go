package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tirbach/tir-bach/internal/domain"
)

type auditFieldKind int

const (
	ruleBoolTrue auditFieldKind = iota + 1
	ruleBool
	ruleUUID
	ruleUUIDOrNull
	ruleUUIDArray
	ruleTimestamp
	ruleTimestampOrNull
	ruleDateOrTimestamp
	ruleDateOrTimestampOrNull
	ruleTimeHHMM
	ruleCurrency
	ruleCountryCode
	ruleLocaleOrEmpty
	ruleTimezone
	ruleSHA256
	ruleCode
	ruleCodeOrEmpty
	ruleOpaqueID
	ruleOpaqueIDOrEmpty
	ruleIntRange
	ruleIntOrNullRange
	ruleIntAuditLogID
	ruleEnum
	ruleEnumArray
	ruleObjMovedCounts
	ruleObjBlockoutGroupInner
	ruleObjMaintRequestInner
	ruleObjMaintInspection
)

type auditFieldRule struct {
	kind     auditFieldKind
	min      int64
	max      int64
	enumName string
}

var (
	reAuditKey          = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	reAuditUUID         = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	reAuditTimestamp    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$`)
	reAuditDate         = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reAuditTimeHHMM     = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d(:[0-5]\d)?$`)
	reAuditCurrency     = regexp.MustCompile(`^[A-Z]{3}$`)
	reAuditCountryCode  = regexp.MustCompile(`^[A-Z]{2}$`)
	reAuditLocale       = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)
	reAuditTimezone     = regexp.MustCompile(`^[A-Za-z_]+(/[A-Za-z0-9_+-]+){0,2}$`)
	reAuditSHA256       = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	reAuditCode         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,63}$`)
	reAuditOpaqueID     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/#-]{0,127}$`)
	reAuditHexDigest    = regexp.MustCompile(`^[0-9a-fA-F]{16,64}$`)
	reAuditHasHexAlpha  = regexp.MustCompile(`[a-fA-F]`)
	reAuditInt          = regexp.MustCompile(`^-?[0-9]{1,12}$`)
	reAuditDigits7Plus  = regexp.MustCompile(`^[0-9]{7,}$`)
	reAuditUKPhoneRun   = regexp.MustCompile(`0[1-35-9][0-9._:/-]{7,}`)
	reAuditDigitRun9    = regexp.MustCompile(`[0-9]{9,}`)
	reAuditCodeStripper = strings.NewReplacer(".", "", "_", "", ":", "", "/", "", "+", "", "-", "")
)

var auditEnumSets = map[string]map[string]bool{
	"role": {
		"owner_admin": true, "manager": true, "staff": true,
		"maintenance_staff": true, "housekeeping_staff": true, "viewer": true,
	},
	"resource_type": {}, // Populated in init() from auditResourceAllowlists keys.
	"block_type": {
		"maintenance": true, "owner_hold": true, "seasonal_closure": true, "weather_closure": true,
	},
	"release_scope": {
		"UNIT": true, "UNIT_TYPE": true, "PROPERTY": true,
	},
	"block_scope": {
		"UNIT": true, "UNITS": true, "UNIT_TYPE": true, "PROPERTY": true,
	},
	"claim_type": {
		"RESERVATION": true, "PENDING_RESERVATION": true, "EXTERNAL_RESERVATION": true,
		"OWNER_USE": true, "MAINTENANCE": true, "SEASONAL_CLOSURE": true,
		"WEATHER_CLOSURE": true, "CHANNEL_ALLOCATION": true, "CHANNEL_RESTRICTION": true,
		"OPERATIONAL_BUFFER": true, "ADMINISTRATIVE_BLOCK": true, "HOLD": true,
	},
	"blockout_status": {
		"ACTIVE": true, "PARTIALLY_CONSUMED": true, "CONSUMED": true,
		"PARTIALLY_RELEASED": true, "RELEASED": true,
	},
	"hold_status": {
		"ACTIVE": true, "CONFIRMED": true, "EXPIRED": true, "CANCELLED": true, "FAILED": true,
	},
	"trigger_event": {
		"BOOKING_CONFIRMATION": true, "PRE_ARRIVAL": true, "ARRIVAL_INFO": true,
		"DEPARTURE_INSTRUCTIONS": true, "POST_STAY_THANK_YOU": true, "PAYMENT_REMINDER": true,
	},
	"trigger_event_or_empty": {
		"": true, "BOOKING_CONFIRMATION": true, "PRE_ARRIVAL": true, "ARRIVAL_INFO": true,
		"DEPARTURE_INSTRUCTIONS": true, "POST_STAY_THANK_YOU": true, "PAYMENT_REMINDER": true,
	},
	"reservation_status": {
		"INQUIRY": true, "PENDING": true, "CONFIRMED": true,
		"CHECKED_IN": true, "CHECKED_OUT": true, "CANCELLED": true, "NO_SHOW": true,
	},
	"payment_status": {
		"": true, "MANUAL": true, "UNPAID": true, "PARTIALLY_PAID": true,
		"PAID": true, "REFUNDED": true, "PARTIALLY_REFUNDED": true, "FAILED": true,
	},
	"reconcile_decision": {
		"": true, "CONFIRMED_SENT": true, "CONFIRMED_FAILED": true, "CANCEL": true,
	},
	"reconcile_evidence_type": {
		"": true, "MTA_ACCEPTED": true, "PROVIDER_WEBHOOK_ACCEPTED": true, "CONSERVATIVE_ASSUME_SENT": true,
		"MTA_REJECTED": true, "PROVIDER_WEBHOOK_FAILED": true, "PROVIDER_NOT_SUBMITTED": true,
	},
	"pricing_model": {
		"per_stay": true, "per_night": true, "per_person_per_stay": true, "per_person_per_night": true,
	},
	"guest_status": {
		"ACTIVE": true, "VIP": true, "DO_NOT_CONTACT": true, "MERGED": true, "ANONYMIZED": true,
	},
	"contact_method_or_empty": {
		"": true, "EMAIL": true, "PHONE": true, "SMS": true, "POST": true,
	},
	"guest_changed_field": {
		"name": true, "email": true, "phone": true, "preferred_contact_method": true,
		"preferred_language": true, "status": true, "address": true, "preferences": true, "notes": true,
	},
	"reservation_changed_field": {
		"guest_name": true, "guest_email": true, "guest_phone": true, "occupant_details": true,
		"stay_dates": true, "party_size": true, "channel_code": true, "status": true,
		"financials": true, "internal_notes": true, "arrival_details": true,
	},
	"dismissal_reason_category": {
		"NOT_DUPLICATE": true, "DISTINCT_PEOPLE": true, "SHARED_CONTACT": true,
		"HOUSEHOLD": true, "CORPORATE": true, "ANONYMISED": true,
		"DIFFERENT_PEOPLE": true, "FAMILY_SAME_ADDRESS": true, "CORPORATE_SHARED": true,
		"TEST_OR_STAFF": true, "OTHER": true,
	},
	"consent_status": {
		"GRANTED": true, "WITHDRAWN": true,
	},
	"interaction_type": {
		"EMAIL_OUTBOUND": true, "EMAIL_INBOUND": true, "PHONE_CALL": true, "IN_PERSON": true,
		"WEBSITE_ENQUIRY": true, "BOOKING_NOTIFICATION": true, "SYSTEM_NOTIFICATION": true, "COMPLAINT": true,
	},
	"comm_direction": {
		"INBOUND": true, "OUTBOUND": true, "INTERNAL": true,
	},
	"delivery_status": {
		"": true, "DRAFT": true, "QUEUED": true, "SENDING": true, "DISPATCH_UNKNOWN": true,
		"ACCEPTED": true, "DELIVERED": true, "BOUNCED": true, "FAILED": true, "CANCELLED": true, "LOGGED": true,
	},
	"enquiry_category": {
		"GENERAL": true, "BOOKING": true, "AVAILABILITY": true, "ACCOMMODATION": true,
		"FACILITIES": true, "ARRIVAL_DEPARTURE": true, "SPECIAL_REQUEST": true, "GROUP_BOOKING": true,
		"CANCELLATION": true, "COMPLAINT": true, "LOST_PROPERTY": true, "OTHER": true,
	},
	"enquiry_status": {
		"NEW": true, "TRIAGED": true, "IN_PROGRESS": true,
		"WAITING_FOR_GUEST": true, "WAITING_FOR_PROPERTY": true, "RESOLVED": true, "CLOSED": true,
	},
	"priority_4": {
		"LOW": true, "NORMAL": true, "HIGH": true, "URGENT": true,
	},
	"enquiry_source": {
		"PHONE": true, "IN_PERSON": true, "EMAIL": true, "WEB": true, "ADMIN": true, "PORTAL": true,
	},
	"portal_identity_status": {
		"ACTIVE": true, "SUSPENDED": true,
	},
	"portal_challenge_purpose": {
		"LOGIN": true, "VERIFY_EMAIL": true, "STEP_UP": true,
	},
	"portal_session_scope": {
		"BOOKING": true, "ACCOUNT": true,
	},
	"note_category": {
		"GENERAL": true, "BOOKING": true, "PREFERENCE": true, "CUSTOMER_SERVICE": true, "OPERATIONAL": true,
	},
	"pref_category": {
		"ACCOMMODATION": true, "PET": true, "ARRIVAL": true, "EQUIPMENT": true,
		"COMMUNICATION": true, "ACCESSIBILITY": true, "OTHER": true,
	},
	"task_type": {
		"CHANGEOVER_CLEAN": true, "INSPECTION": true, "DEEP_CLEAN": true, "MID_STAY_REFRESH": true, "GENERAL": true,
	},
	"hk_stage": {
		"GUEST_DEPARTING": true, "CLEANING_REQUIRED": true, "CLEANING_IN_PROGRESS": true,
		"INSPECTION_REQUIRED": true, "READY": true,
	},
	"unit_status": {
		"active": true, "maintenance": true, "inactive": true, "retired": true,
	},
	"unit_status_or_empty": {
		"": true, "active": true, "maintenance": true, "inactive": true, "retired": true,
	},
	"unit_category": {
		"glamping": true, "tent_pitch": true, "touring_pitch": true, "cabin": true, "other": true,
	},
	"asset_status": {
		"ACTIVE": true, "RETIRED": true,
	},
	"maint_status": {
		"REPORTED": true, "TRIAGED": true, "SCHEDULED": true, "IN_PROGRESS": true,
		"AWAITING_PARTS": true, "ON_HOLD": true, "COMPLETED": true, "CANCELLED": true, "CLOSED": true,
	},
	"maint_work_type": {
		"REACTIVE": true, "PREVENTATIVE": true,
	},
	"maint_impact": {
		"NONE": true, "PARTIAL": true, "UNAVAILABLE": true,
	},
	"maint_inspection_result": {
		"PASSED": true, "FAILED": true,
	},
	"maint_inspection_result_or_empty": {
		"": true, "PASSED": true, "FAILED": true,
	},
	"maint_entry_type": {
		"WORK_STARTED": true, "DIAGNOSIS": true, "WORK_PERFORMED": true, "PERFORMED": true, "PARTS_USED": true,
		"ADDITIONAL_WORK": true, "DELAY_REASON": true, "CONTRACTOR_NOTE": true,
		"COMPLETION_NOTE": true, "GENERAL_NOTE": true, "REOPENED": true,
	},
	"maint_frequency": {
		"ONCE": true, "WEEKLY": true, "MONTHLY": true, "QUARTERLY": true, "SIX_MONTHLY": true, "ANNUALLY": true,
	},
	"deposit_mode": {
		"FULL": true, "PERCENTAGE": true, "FIXED": true,
	},
	"branding_action": {
		"updated": true, "logo_uploaded": true, "logo_deleted": true,
	},
	"branding_logo_mime": {
		"image/png": true, "image/jpeg": true, "image/webp": true,
	},
	"outbox_status": {
		"PENDING": true, "PROCESSING": true, "DISPATCHING": true, "DISPATCH_UNKNOWN": true,
		"CANCEL_REQUESTED": true, "CANCELLED": true, "SENT": true, "FAILED": true,
	},
	"channel_ownership_mode": {
		"LOVELACE_ARI_PROVIDER_BOOKINGS": true, "READ_ONLY_IMPORT": true,
	},
	"channel_job_type": {
		"VERIFY_CONNECTION": true, "DISCOVER_LISTINGS": true, "PUSH_AVAILABILITY": true,
		"PUSH_RATES": true, "PULL_RESERVATIONS": true, "PROCESS_WEBHOOK_EVENT": true,
	},
	"channel_job_status": {
		"PENDING": true, "RUNNING": true, "SUCCEEDED": true,
		"FAILED_RETRYABLE": true, "FAILED_TERMINAL": true, "CANCELLED": true,
	},
	"channel_import_status": {
		"IMPORTED": true, "UPDATED": true, "CANCELLED": true,
		"CONFLICT_OVERBOOKED": true, "UNMAPPED": true, "FAILED": true,
	},
	"channel_webhook_status": {
		"RECEIVED": true, "QUEUED": true, "PROCESSED": true,
		"CONFLICT": true, "IGNORED": true, "FAILED": true,
	},
	"report_export_type": {
		"booking_line_items": true, "payment_ledger": true, "refund_ledger": true,
	},
	"report_export_format": {
		"csv": true, "json": true,
	},
	"backup_provider": {
		"LOCAL_ZIP": true, "AWS_S3": true, "S3_COMPATIBLE": true, "GCS": true, "AZURE_BLOB": true,
	},
	"backup_target_status": {
		"NEVER_RUN": true, "VERIFIED": true, "SUCCEEDED": true, "FAILED": true,
	},
	"backup_run_kind": {
		"LOCAL_DOWNLOAD": true, "CLOUD_MANUAL": true, "CLOUD_SCHEDULED": true, "VERIFY_ARCHIVE": true, "RESTORE_EMPTY_DB": true,
	},
	"backup_run_status": {
		"RUNNING": true, "SUCCEEDED": true, "FAILED": true,
	},
}

var auditMovedCountsAllowedKeys = map[string]bool{
	"reservations":       true,
	"booking_holds":      true,
	"preferences":        true,
	"notes":              true,
	"enquiries":          true,
	"communications":     true,
	"email_outbox":       true,
	"communication_jobs": true,
	"consent_records":    true,
	"dismissals":         true,
	"delivery_events":    true,
	"jobs":               true,
}

var (
	rRedacted  = auditFieldRule{kind: ruleBoolTrue}
	rBool      = auditFieldRule{kind: ruleBool}
	rUUID      = auditFieldRule{kind: ruleUUID}
	rUUIDNull  = auditFieldRule{kind: ruleUUIDOrNull}
	rUUIDArr   = auditFieldRule{kind: ruleUUIDArray}
	rTS        = auditFieldRule{kind: ruleTimestamp}
	rTSNull    = auditFieldRule{kind: ruleTimestampOrNull}
	rDateTS    = auditFieldRule{kind: ruleDateOrTimestamp}
	rDateTSN   = auditFieldRule{kind: ruleDateOrTimestampOrNull}
	rTimeHHMM  = auditFieldRule{kind: ruleTimeHHMM}
	rCurrency  = auditFieldRule{kind: ruleCurrency}
	rCountry   = auditFieldRule{kind: ruleCountryCode}
	rLocale    = auditFieldRule{kind: ruleLocaleOrEmpty}
	rTZ        = auditFieldRule{kind: ruleTimezone}
	rSHA256    = auditFieldRule{kind: ruleSHA256}
	rCode      = auditFieldRule{kind: ruleCode}
	rCodeOpt   = auditFieldRule{kind: ruleCodeOrEmpty}
	rOpaqueID  = auditFieldRule{kind: ruleOpaqueID}
	rOpaqueOpt = auditFieldRule{kind: ruleOpaqueIDOrEmpty}
	rVersion   = auditFieldRule{kind: ruleIntRange, min: 0, max: 100000}
	rCount     = auditFieldRule{kind: ruleIntRange, min: 0, max: 100000}
	rParty     = auditFieldRule{kind: ruleIntRange, min: 0, max: 100}
	rDays365   = auditFieldRule{kind: ruleIntRange, min: 0, max: 365}
	rDays1_36  = auditFieldRule{kind: ruleIntRange, min: 1, max: 365}
	rDays1_3N  = auditFieldRule{kind: ruleIntOrNullRange, min: 1, max: 365}
	rNights    = auditFieldRule{kind: ruleIntRange, min: 0, max: 3650}
	rRetDays   = auditFieldRule{kind: ruleIntRange, min: 1, max: 36500}
	rPence     = auditFieldRule{kind: ruleIntRange, min: 0, max: 50000000}
	rBPS       = auditFieldRule{kind: ruleIntRange, min: 0, max: 10000}
	rPct       = auditFieldRule{kind: ruleIntRange, min: 0, max: 100}
	rSort      = auditFieldRule{kind: ruleIntRange, min: 0, max: 10000}
	rHoldMin   = auditFieldRule{kind: ruleIntRange, min: 1, max: 10080}
	rOffHours  = auditFieldRule{kind: ruleIntRange, min: -720, max: 720}
	rHourNull  = auditFieldRule{kind: ruleIntOrNullRange, min: 0, max: 23}
	rAnchor    = auditFieldRule{kind: ruleIntRange, min: 0, max: 31}
	rDOWMask   = auditFieldRule{kind: ruleIntRange, min: 0, max: 127}
	rSizeByte  = auditFieldRule{kind: ruleIntRange, min: 0, max: 52428800}
	rAuditID   = auditFieldRule{kind: ruleIntAuditLogID, min: 1, max: 100000000}
)

func rEnum(name string) auditFieldRule    { return auditFieldRule{kind: ruleEnum, enumName: name} }
func rEnumArr(name string) auditFieldRule { return auditFieldRule{kind: ruleEnumArray, enumName: name} }

var maintInspectionAllowlist = map[string]auditFieldRule{
	"id":           rUUID,
	"property_id":  rUUID,
	"request_id":   rUUID,
	"inspector_id": rUUID,
	"result":       rEnum("maint_inspection_result"),
	"inspected_at": rTS,
}

// auditResourceAllowlists mirrors public.audit_resource_key_rule in PostgreSQL 0018_phase3_3_2_final_acceptance.sql 1:1.
var auditResourceAllowlists = map[string]map[string]auditFieldRule{
	"admin_session": {
		"redacted": rRedacted, "session_id": rUUID, "user_id": rUUID,
		"property_id": rUUIDNull, "created_at": rTS, "expires_at": rTS,
	},
	"admin_user": {
		"redacted": rRedacted, "id": rUUID, "user_id": rUUID, "property_id": rUUIDNull,
		"role": rEnum("role"), "is_active": rBool, "must_rotate_password": rBool,
		"sessions_revoked": rBool, "session_id": rUUID, "new_session_id": rUUID,
		"last_login_at": rTSNull, "version": rVersion, "created_at": rTS, "updated_at": rTS,
	},
	"audit_log": {
		"redacted": rRedacted, "redaction_id": rUUID, "target_audit_log_id": rAuditID,
		"target_resource_type": rEnum("resource_type"), "target_resource_id": rOpaqueID,
		"redacted_at": rTS,
	},
	"availability_block": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "unit_id": rUUID,
		"unit_code": rCodeOpt, "start_date": rDateTS, "end_date": rDateTS,
		"block_type": rEnum("block_type"), "created_by": rUUIDNull, "released_by": rUUIDNull,
		"released_at": rTSNull, "created_at": rTS, "updated_at": rTS,
	},
	"availability_release_rule": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID,
		"scope_type": rEnum("release_scope"), "unit_id": rUUIDNull, "unit_type_id": rUUIDNull,
		"unit_code": rCodeOpt, "channel_code": rCodeOpt, "start_date": rDateTS, "end_date": rDateTS,
		"release_at": rTS, "released_manually_at": rTSNull, "released_manually_by": rUUIDNull,
		"is_active": rBool, "created_by": rUUIDNull, "updated_by": rUUIDNull,
		"cancelled_at": rTSNull, "cancelled_by": rUUIDNull,
		"version": rVersion, "created_at": rTS, "updated_at": rTS,
	},
	"backup_run": {
		"redacted": rRedacted, "id": rUUID, "target_id": rUUIDNull,
		"run_kind": rEnum("backup_run_kind"), "provider": rEnum("backup_provider"),
		"status": rEnum("backup_run_status"), "encrypted": rBool,
		"archive_sha256": rSHA256, "size_bytes": rSizeByte,
		"table_count": rCount, "row_count": rCount, "attachment_count": rCount,
		"started_at": rTS, "completed_at": rTSNull,
	},
	"backup_target": {
		"redacted": rRedacted, "id": rUUID, "provider": rEnum("backup_provider"),
		"encrypt_archive": rBool, "schedule_enabled": rBool,
		"schedule_interval_hours": rCount, "retention_count": rDays1_36,
		"is_active": rBool, "last_status": rEnum("backup_target_status"),
		"last_verified_at": rTSNull, "last_backup_at": rTSNull,
		"version": rVersion, "created_at": rTS, "updated_at": rTS,
	},
	"blockout_group": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID,
		"scope_type": rEnum("block_scope"), "target_unit_type_id": rUUIDNull,
		"category": rEnum("claim_type"), "channel_scope": rCodeOpt,
		"start_date": rDateTS, "end_date": rDateTS, "apply_to_future_units": rBool,
		"status": rEnum("blockout_status"), "active_claim_count": rCount,
		"consumed_claim_count": rCount, "released_claim_count": rCount, "total_claim_count": rCount,
		"created_by": rUUIDNull, "released_by": rUUIDNull, "released_at": rTSNull,
		"version": rVersion, "created_at": rTS, "updated_at": rTS,
		"unit_count": rCount, "released_unit_ids": rUUIDArr, "reinstated_claim_ids": rUUIDArr,
		"unit_ids": rUUIDArr, "group": {kind: ruleObjBlockoutGroupInner},
	},
	"booking_channel": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "code": rCode,
		"category": rCode, "is_active": rBool, "sort_order": rSort,
		"created_at": rTS, "updated_at": rTS,
	},
	"booking_hold": {
		"redacted": rRedacted, "id": rUUID, "hold_id": rUUID, "property_id": rUUID,
		"hold_reference": rCode, "guest_id": rUUIDNull, "unit_type_id": rUUID,
		"rate_plan_id": rUUID, "channel_code": rCodeOpt, "arrival_date": rDateTS,
		"departure_date": rDateTS, "adults": rParty, "children": rParty, "infants": rParty,
		"terms_version": rCodeOpt, "terms_accepted_at": rTSNull, "status": rEnum("hold_status"),
		"expires_at": rTS, "confirmed_reservation_id": rUUIDNull,
		"version": rVersion, "created_at": rTS, "updated_at": rTS,
	},
	"channel_connection": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "provider_code": rCode,
		"auth_method": rCode, "status": rCode, "has_credentials": rBool, "has_live_adapter": rBool,
		"credentials_updated_at": rTSNull, "token_expires_at": rTSNull,
		"last_verified_at": rTSNull, "last_synced_at": rTSNull, "last_sync_error_code": rCodeOpt,
		"version": rVersion, "created_at": rTS, "updated_at": rTS,
	},
	"channel_external_reservation": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "channel_connection_id": rUUID,
		"listing_mapping_id": rUUIDNull, "provider_code": rCode,
		"external_reservation_id": rOpaqueID, "external_revision": rOpaqueOpt,
		"reservation_id": rUUIDNull, "import_status": rEnum("channel_import_status"),
		"created_at": rTS, "updated_at": rTS,
	},
	"channel_listing_mapping": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "channel_connection_id": rUUID,
		"provider_code": rCode, "external_property_id": rOpaqueOpt, "external_listing_id": rOpaqueID,
		"external_rate_plan_id": rOpaqueOpt, "unit_type_id": rUUID, "unit_id": rUUIDNull,
		"rate_plan_id": rUUIDNull, "booking_channel_code": rCode, "currency": rCurrency,
		"timezone": rTZ, "sync_availability": rBool, "sync_rates": rBool,
		"sync_reservations": rBool, "ownership_mode": rEnum("channel_ownership_mode"),
		"is_active": rBool, "version": rVersion, "created_at": rTS, "updated_at": rTS,
	},
	"channel_sync_job": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "channel_connection_id": rUUID,
		"listing_mapping_id": rUUIDNull, "provider_code": rCode,
		"job_type": rEnum("channel_job_type"), "status": rEnum("channel_job_status"),
		"attempts": rCount, "max_attempts": rCount, "fence_epoch": rCount,
		"last_error_code": rCodeOpt, "started_at": rTSNull, "completed_at": rTSNull,
		"created_at": rTS, "updated_at": rTS,
	},
	"channel_webhook_event": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "channel_connection_id": rUUID,
		"provider_code": rCode, "external_event_id": rOpaqueID, "event_type": rCode,
		"signature_verified": rBool, "status": rEnum("channel_webhook_status"),
		"received_at": rTS, "processed_at": rTSNull,
	},
	"communication_automation_rule": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID,
		"trigger_event": rEnum("trigger_event"), "offset_hours": rOffHours,
		"send_hour_local": rHourNull, "template_id": rUUID,
		"applicable_statuses": rEnumArr("reservation_status"), "is_active": rBool,
		"created_by": rUUIDNull, "updated_by": rUUIDNull, "created_at": rTS, "updated_at": rTS,
	},
	"communication_template": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "code": rCode,
		"category": rCode, "locale": rLocale, "is_active": rBool, "version": rVersion,
		"created_by": rUUIDNull, "updated_by": rUUIDNull, "created_at": rTS, "updated_at": rTS,
	},
	"confirmation_access_token": {
		"redacted": rRedacted, "reference": rCode,
	},
	"email_outbox": {
		"redacted": rRedacted, "id": rUUID, "outbox_id": rUUID, "property_id": rUUID,
		"reservation_id": rUUIDNull, "guest_id": rUUIDNull, "communication_id": rUUIDNull,
		"actor_id": rUUID, "template": rCodeOpt, "status": rEnum("outbox_status"),
		"attempts": rCount, "dispatched_at": rTSNull, "dispatch_worker_id": rCodeOpt,
		"dispatch_lease_expires_at": rTSNull, "dispatch_heartbeat_at": rTSNull,
		"dispatch_fence_epoch": rCount, "dispatch_fenced_at": rTSNull, "dispatch_outcome": rCodeOpt,
		"reconciled_by": rUUIDNull, "reconciled_at": rTSNull,
		"resolution": rEnum("reconcile_decision"), "reconciled_decision": rEnum("reconcile_decision"),
		"evidence_type": rEnum("reconcile_evidence_type"), "reconciled_evidence_type": rEnum("reconcile_evidence_type"),
		"evidence_ref": rCodeOpt, "reconciled_evidence_ref": rCodeOpt,
		"reason_code": rCodeOpt, "reconciled_reason_code": rCodeOpt,
		"cancelled_at": rTSNull, "next_attempt_at": rTSNull, "sent_at": rTSNull, "created_at": rTS,
	},
	"extra": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "code": rCode,
		"pricing_model": rEnum("pricing_model"), "price_pence": rPence, "tax_rate_bps": rBPS,
		"max_quantity": rCount, "is_active": rBool, "sort_order": rSort,
		"version": rVersion, "archived_at": rTSNull, "created_at": rTS, "updated_at": rTS,
	},
	"guest": {
		"redacted": rRedacted, "id": rUUID, "guest_id": rUUID, "property_id": rUUID,
		"status": rEnum("guest_status"), "preferred_contact_method": rEnum("contact_method_or_empty"),
		"preferred_language": rLocale, "merged_into_guest_id": rUUIDNull,
		"merged_at": rTSNull, "anonymized_at": rTSNull, "erasure_requested_at": rTSNull,
		"has_email": rBool, "has_phone": rBool, "has_notes": rBool, "has_reason": rBool,
		"changed_fields": rEnumArr("guest_changed_field"), "version": rVersion,
		"created_by": rUUIDNull, "updated_by": rUUIDNull, "created_at": rTS, "updated_at": rTS,
		"guest_a_id": rUUID, "guest_b_id": rUUID, "reason_category": rEnum("dismissal_reason_category"),
		"source_guest_id": rUUID, "target_guest_id": rUUID, "merge_event_id": rUUID,
		"moved_counts": {kind: ruleObjMovedCounts}, "consent_record_id": rUUID,
		"consent_type": rCodeOpt, "consent_status": rEnum("consent_status"), "method": rCodeOpt, "source": rCodeOpt,
		"policy_version": rCodeOpt, "occurred_at": rTS, "pending_dispatches": rCount,
		"requested_guest_id": rUUID, "enquiry_id": rUUIDNull,
	},
	"guest_portal_identity": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "guest_id": rUUID,
		"status": rEnum("portal_identity_status"), "purpose": rEnum("portal_challenge_purpose"),
		"challenge_id": rUUIDNull, "sessions_revoked": rCount, "version": rVersion,
		"email_verified_at": rTSNull, "created_at": rTS, "updated_at": rTS,
	},
	"guest_portal_session": {
		"redacted": rRedacted, "id": rUUID, "session_id": rUUID, "property_id": rUUID,
		"portal_identity_id": rUUIDNull, "scoped_reservation_id": rUUIDNull,
		"scope_type": rEnum("portal_session_scope"), "revoked_count": rCount,
		"idle_expires_at": rTS, "absolute_expires_at": rTS, "revoked_at": rTSNull, "created_at": rTS,
	},
	"guest_communication": {
		"redacted": rRedacted, "id": rUUID, "communication_id": rUUID, "property_id": rUUID,
		"guest_id": rUUIDNull, "reservation_id": rUUIDNull, "enquiry_id": rUUIDNull,
		"interaction_type": rEnum("interaction_type"), "direction": rEnum("comm_direction"),
		"template_id": rUUIDNull, "template_version_id": rUUIDNull, "rule_id": rUUIDNull,
		"trigger_event": rEnum("trigger_event_or_empty"), "email_outbox_id": rUUIDNull, "outbox_id": rUUIDNull,
		"delivery_status": rEnum("delivery_status"), "follow_up_required": rBool,
		"follow_up_assigned_to": rUUIDNull, "follow_up_at": rTSNull, "follow_up_completed_at": rTSNull,
		"staff_user_id": rUUIDNull, "occurred_at": rTS, "created_at": rTS, "updated_at": rTS,
	},
	"guest_enquiry": {
		"redacted": rRedacted, "id": rUUID, "enquiry_id": rUUID, "message_id": rUUID,
		"property_id": rUUID, "reference": rCode, "guest_id": rUUIDNull, "reservation_id": rUUIDNull,
		"maintenance_request_id": rUUIDNull, "category": rEnum("enquiry_category"),
		"status": rEnum("enquiry_status"), "priority": rEnum("priority_4"), "source": rEnum("enquiry_source"),
		"direction": rEnum("comm_direction"), "is_internal": rBool, "assigned_user_id": rUUIDNull,
		"staff_user_id": rUUIDNull, "resolved_by": rUUIDNull, "closed_by": rUUIDNull,
		"target_response_at": rTSNull, "follow_up_at": rTSNull, "resolved_at": rTSNull,
		"closed_at": rTSNull, "version": rVersion, "created_at": rTS, "updated_at": rTS,
	},
	"guest_note": {
		"redacted": rRedacted, "id": rUUID, "note_id": rUUID, "property_id": rUUID,
		"guest_id": rUUID, "reservation_id": rUUIDNull, "category": rEnum("note_category"),
		"is_pinned": rBool, "created_by": rUUIDNull, "updated_by": rUUIDNull,
		"created_at": rTS, "updated_at": rTS,
	},
	"guest_preference": {
		"redacted": rRedacted, "id": rUUID, "preference_id": rUUID, "property_id": rUUID,
		"guest_id": rUUID, "category": rEnum("pref_category"), "has_notes": rBool,
		"created_by": rUUIDNull, "updated_by": rUUIDNull, "created_at": rTS, "updated_at": rTS,
	},
	"housekeeping_task": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "unit_id": rUUID,
		"reservation_id": rUUIDNull, "task_type": rEnum("task_type"), "stage": rEnum("hk_stage"),
		"priority": rEnum("priority_4"), "unit_status": rEnum("unit_status_or_empty"),
		"scheduled_date": rDateTS, "due_at": rTSNull, "assigned_to": rUUIDNull,
		"same_day_arrival": rBool, "has_active_block": rBool, "overdue": rBool,
		"started_at": rTSNull, "started_by": rUUIDNull, "inspected_at": rTSNull,
		"inspected_by": rUUIDNull, "completed_at": rTSNull, "completed_by": rUUIDNull,
		"version": rVersion, "created_at": rTS, "updated_at": rTS,
	},
	"maintenance_asset": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "asset_code": rCode,
		"location_id": rUUIDNull, "unit_id": rUUIDNull, "status": rEnum("asset_status"),
		"installed_on": rDateTSN, "warranty_expires_on": rDateTSN,
		"version": rVersion, "created_at": rTS, "updated_at": rTS,
	},
	"maintenance_attachment": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "request_id": rUUID,
		"work_entry_id": rUUIDNull, "content_type": rCode, "size_bytes": rSizeByte,
		"sha256": rSHA256, "uploaded_by": rUUIDNull, "created_at": rTS,
	},
	"maintenance_category": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "code": rCode,
		"is_active": rBool, "sort_order": rSort, "created_at": rTS, "updated_at": rTS,
	},
	"maintenance_contractor": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "is_active": rBool,
		"created_at": rTS, "updated_at": rTS,
	},
	"maintenance_location": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "code": rCode,
		"location_type": rCode, "unit_id": rUUIDNull, "is_active": rBool,
		"sort_order": rSort, "created_at": rTS, "updated_at": rTS,
	},
	"maintenance_request": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "reference": rCode,
		"category_id": rUUID, "priority": rEnum("priority_4"), "status": rEnum("maint_status"),
		"work_type": rEnum("maint_work_type"), "location_id": rUUIDNull, "unit_id": rUUIDNull,
		"asset_id": rUUIDNull, "schedule_id": rUUIDNull, "scheduled_occurrence_date": rDateTSN,
		"reported_by": rUUIDNull, "assigned_user_id": rUUIDNull, "assigned_contractor_id": rUUIDNull,
		"reported_at": rTS, "target_date": rDateTSN, "scheduled_for": rTSNull,
		"started_at": rTSNull, "completed_at": rTSNull, "closed_at": rTSNull,
		"guest_impact": rBool, "safety_impact": rBool, "availability_impact": rEnum("maint_impact"),
		"inspection_required": rBool, "overdue": rBool, "has_active_restriction": rBool,
		"latest_inspection_result": rEnum("maint_inspection_result_or_empty"),
		"version":                  rVersion, "created_at": rTS, "updated_at": rTS,
		"request_id": rUUID, "entry_type": rEnum("maint_entry_type"), "author_id": rUUID,
		"blockout_group_id": rUUID, "group_status": rEnum("blockout_status"),
		"start_date": rDateTS, "end_date": rDateTS, "group_version": rVersion,
		"created_by": rUUIDNull, "released_by": rUUIDNull, "released_at": rTSNull,
		"request": {kind: ruleObjMaintRequestInner}, "inspection": {kind: ruleObjMaintInspection},
	},
	"maintenance_schedule": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "category_id": rUUID,
		"location_id": rUUIDNull, "unit_id": rUUIDNull, "asset_id": rUUIDNull,
		"priority": rEnum("priority_4"), "frequency": rEnum("maint_frequency"),
		"next_due_date": rDateTS, "anchor_day": rAnchor, "last_generated_for_date": rDateTSN,
		"assigned_user_id": rUUIDNull, "assigned_contractor_id": rUUIDNull,
		"inspection_required": rBool, "is_active": rBool,
		"version": rVersion, "created_at": rTS, "updated_at": rTS,
	},
	"payment_exception": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "hold_id": rUUIDNull,
		"payment_attempt_id": rUUIDNull, "reservation_id": rUUIDNull, "exception_type": rCode,
		"amount_pence": rPence, "currency": rCurrency, "status": rCode,
		"resolved_by": rUUIDNull, "resolved_at": rTSNull, "created_at": rTS,
	},
	"payment_webhook_event": {
		"redacted": rRedacted, "event_id": rOpaqueID, "event_type": rCode,
		"event_livemode": rBool, "expected_livemode": rBool, "environment": rCode, "payment_attempt": rCodeOpt,
	},
	"property_branding": {
		"redacted": rRedacted, "property_id": rUUID, "action": rEnum("branding_action"),
		"schema_version": rCount, "version": rVersion,
		"has_custom_palette": rBool, "has_terminology_overrides": rBool, "has_logo": rBool,
		"logo_content_type": rEnum("branding_logo_mime"), "logo_size_bytes": rSizeByte,
		"logo_sha256": rSHA256, "logo_version": rCount,
		"created_at": rTS, "updated_at": rTS,
	},
	"property": {
		"redacted": rRedacted, "id": rUUID, "slug": rCode, "country_code": rCountry,
		"timezone": rTZ, "currency": rCurrency, "check_in_time": rTimeHHMM, "check_out_time": rTimeHHMM,
		"vat_registered": rBool, "version": rVersion, "created_at": rTS, "updated_at": rTS,
	},
	"property_booking_policy": {
		"redacted": rRedacted, "property_id": rUUID, "hold_duration_minutes": rHoldMin,
		"deposit_mode": rEnum("deposit_mode"), "deposit_percentage": rPct,
		"deposit_fixed_pence": rPence, "balance_due_days_before_arrival": rDays365,
		"extra_adult_nightly_pence": rPence, "child_nightly_pence": rPence,
		"infant_nightly_pence": rPence, "booking_terms_version": rCodeOpt,
		"stripe_enabled": rBool, "guest_portal_enabled": rBool, "version": rVersion, "updated_at": rTS,
	},
	"property_retention_policy": {
		"redacted": rRedacted, "property_id": rUUID, "enquiry_retention_days": rRetDays,
		"inactive_guest_retention_days": rRetDays, "comms_retention_days": rRetDays,
		"auto_anonymize_enabled": rBool, "execution_mode": rCodeOpt,
		"updated_by": rUUIDNull, "updated_at": rTS,
	},
	"rate_plan": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "code": rCode,
		"is_default": rBool, "is_active": rBool, "priority": rSort,
		"version": rVersion, "archived_at": rTSNull, "created_at": rTS, "updated_at": rTS,
	},
	"rate_rule": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "rate_plan_id": rUUID,
		"unit_type_id": rUUID, "start_date": rDateTS, "end_date": rDateTS,
		"days_of_week": rDOWMask, "nightly_rate_pence": rPence, "min_nights_override": rDays1_3N,
		"priority": rSort, "is_active": rBool, "version": rVersion, "archived_at": rTSNull,
		"created_at": rTS, "updated_at": rTS,
	},
	"report_export": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID,
		"export_type": rEnum("report_export_type"), "format": rEnum("report_export_format"),
		"start_date": rDateTS, "end_date": rDateTS, "currency": rCodeOpt,
		"row_count": rCount, "created_at": rTS,
	},
	"reservation": {
		"redacted": rRedacted, "id": rUUID, "reservation_id": rUUID, "property_id": rUUID,
		"reference": rCode, "guest_id": rUUIDNull, "channel_code": rCodeOpt,
		"arrival_date": rDateTS, "departure_date": rDateTS, "nights": rNights,
		"adults": rParty, "children": rParty, "infants": rParty,
		"status": rEnum("reservation_status"), "payment_status": rEnum("payment_status"),
		"total_pence": rPence, "deposit_pence": rPence, "amount_paid_pence": rPence,
		"balance_due_pence": rPence, "currency": rCurrency, "unit_ids": rUUIDArr,
		"changed_fields": rEnumArr("reservation_changed_field"), "version": rVersion,
		"cancelled_at": rTSNull, "cancellation_requested_at": rTSNull,
		"created_by": rUUIDNull, "updated_by": rUUIDNull, "created_at": rTS, "updated_at": rTS,
	},
	"unit": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "unit_type_id": rUUID,
		"code": rCode, "status": rEnum("unit_status"), "sort_order": rSort,
		"version": rVersion, "archived_at": rTSNull, "created_at": rTS, "updated_at": rTS,
	},
	"unit_type": {
		"redacted": rRedacted, "id": rUUID, "property_id": rUUID, "code": rCode,
		"category": rEnum("unit_category"), "base_capacity_adults": rParty,
		"max_adults": rParty, "max_children": rParty, "max_infants": rParty,
		"max_guests": rParty, "pets_allowed": rBool, "max_pets": rParty,
		"base_nightly_rate_pence": rPence, "tax_rate_bps": rBPS,
		"min_nights": rDays1_36, "max_nights": rDays1_3N, "is_active": rBool,
		"is_public_bookable": rBool, "sort_order": rSort, "version": rVersion,
		"archived_at": rTSNull, "created_at": rTS, "updated_at": rTS,
	},
}

func init() {
	resSet := make(map[string]bool, len(auditResourceAllowlists))
	for k := range auditResourceAllowlists {
		resSet[k] = true
	}
	auditEnumSets["resource_type"] = resSet
}

// SupportedAuditResourceTypes returns the exact sorted list of supported audit resource types.
func SupportedAuditResourceTypes() []string {
	out := make([]string, 0, len(auditResourceAllowlists))
	for k := range auditResourceAllowlists {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func decodeAuditJSONUseNumber(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// sanitizeAuditPayload converts audit payloads into strict resource-type-allowlisted JSON objects,
// failing closed if resourceType is unsupported.
func sanitizeAuditPayload(resourceType string, payload any) (json.RawMessage, error) {
	if payload == nil {
		return nil, nil
	}
	rt := strings.ToLower(strings.TrimSpace(resourceType))
	allowlist, ok := auditResourceAllowlists[rt]
	if !ok {
		return nil, fmt.Errorf("unsupported audit resource type %q", resourceType)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	decoded, err := decodeAuditJSONUseNumber(raw)
	if err != nil {
		return nil, err
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("audit payload for %s must serialize as a JSON object", rt)
	}
	cleaned, _ := filterAuditObjectByAllowlist(rt, m, allowlist, true)
	out, err := json.Marshal(cleaned)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// buildRedactionOverlaySnapshot constructs a strictly resource-type-allowlisted replacement
// JSONB snapshot matching PostgreSQL validate_audit_redaction_snapshot 1:1.
func buildRedactionOverlaySnapshot(resourceType string, raw json.RawMessage) (json.RawMessage, error) {
	rt := strings.ToLower(strings.TrimSpace(resourceType))
	allowlist, ok := auditResourceAllowlists[rt]
	if !ok {
		return nil, domain.NewValidationError(map[string]string{
			"resource_type": "unsupported audit resource type for redaction",
		})
	}
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	decoded, err := decodeAuditJSONUseNumber(raw)
	if err != nil {
		return json.RawMessage(`{"redacted":true}`), nil
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		return json.RawMessage(`{"redacted":true}`), nil
	}
	cleaned, stripped := filterAuditObjectByAllowlist(rt, m, allowlist, true)
	if len(cleaned) == 0 || stripped {
		cleaned["redacted"] = true
	}
	out, err := json.Marshal(cleaned)
	if err != nil {
		return json.RawMessage(`{"redacted":true}`), nil
	}
	return out, nil
}

func filterAuditObjectByAllowlist(
	resourceType string,
	src map[string]any,
	allowlist map[string]auditFieldRule,
	allowNested bool,
) (map[string]any, bool) {
	out := make(map[string]any, len(src))
	stripped := false
	for k, v := range src {
		if len(out) >= 40 {
			stripped = true
			break
		}
		if !reAuditKey.MatchString(k) || hasASCIIControlChars(k) || strings.Contains(k, "@") || domain.ContainsPII(k) {
			stripped = true
			continue
		}
		if !allowNested && (k == "group" || k == "moved_counts" || k == "request" || k == "inspection" || k == "unit_count" || k == "released_unit_ids" || k == "reinstated_claim_ids" || k == "unit_ids") {
			if resourceType == "blockout_group" || resourceType == "maintenance_request" || resourceType == "guest" {
				stripped = true
				continue
			}
		}
		rule, allowed := allowlist[k]
		if !allowed {
			stripped = true
			continue
		}
		cleanVal, valOK, childStripped := validateAndFilterAuditValue(v, rule)
		if childStripped {
			stripped = true
		}
		if !valOK {
			stripped = true
			continue
		}
		out[k] = cleanVal
	}
	return out, stripped
}

func validateAndFilterAuditValue(v any, rule auditFieldRule) (any, bool, bool) {
	switch rule.kind {
	case ruleBoolTrue:
		b, ok := v.(bool)
		return true, ok && b, false
	case ruleBool:
		b, ok := v.(bool)
		return b, ok, false
	case ruleUUID:
		s, ok := validateAuditString(v, 36)
		return s, ok && reAuditUUID.MatchString(s), false
	case ruleUUIDOrNull:
		if v == nil {
			return nil, true, false
		}
		s, ok := validateAuditString(v, 36)
		return s, ok && reAuditUUID.MatchString(s), false
	case ruleUUIDArray:
		if v == nil {
			return nil, true, false
		}
		arr, ok := v.([]any)
		if !ok || len(arr) > 64 {
			return nil, false, true
		}
		cleaned := make([]string, 0, len(arr))
		childStripped := false
		for _, elem := range arr {
			s, sOK := validateAuditString(elem, 36)
			if !sOK || !reAuditUUID.MatchString(s) {
				childStripped = true
				continue
			}
			cleaned = append(cleaned, s)
		}
		return cleaned, true, childStripped
	case ruleTimestamp:
		s, ok := validateAuditTimestamp(v)
		return s, ok, false
	case ruleTimestampOrNull:
		if v == nil {
			return nil, true, false
		}
		s, ok := validateAuditTimestamp(v)
		return s, ok, false
	case ruleDateOrTimestamp:
		s, ok := validateAuditDateOrTimestamp(v)
		return s, ok, false
	case ruleDateOrTimestampOrNull:
		if v == nil {
			return nil, true, false
		}
		s, ok := validateAuditDateOrTimestamp(v)
		return s, ok, false
	case ruleTimeHHMM:
		s, ok := validateAuditString(v, 8)
		return s, ok && (s == "" || reAuditTimeHHMM.MatchString(s)), false
	case ruleCurrency:
		s, ok := validateAuditString(v, 3)
		return s, ok && reAuditCurrency.MatchString(s), false
	case ruleCountryCode:
		s, ok := validateAuditString(v, 2)
		return s, ok && reAuditCountryCode.MatchString(s), false
	case ruleLocaleOrEmpty:
		s, ok := validateAuditString(v, 8)
		return s, ok && (s == "" || reAuditLocale.MatchString(s)), false
	case ruleTimezone:
		s, ok := validateAuditString(v, 64)
		return s, ok && reAuditTimezone.MatchString(s), false
	case ruleSHA256:
		s, ok := validateAuditString(v, 64)
		return s, ok && reAuditSHA256.MatchString(s), false
	case ruleCode:
		s, ok := validateAuditString(v, 64)
		return s, ok && isSafeAuditCode(s, false), false
	case ruleCodeOrEmpty:
		s, ok := validateAuditString(v, 64)
		return s, ok && isSafeAuditCode(s, true), false
	case ruleOpaqueID:
		s, ok := validateAuditString(v, 128)
		return s, ok && isSafeAuditOpaqueID(s, false), false
	case ruleOpaqueIDOrEmpty:
		s, ok := validateAuditString(v, 128)
		return s, ok && isSafeAuditOpaqueID(s, true), false
	case ruleIntRange, ruleIntAuditLogID:
		n, ok := validateAuditIntRange(v, rule.min, rule.max)
		return n, ok, false
	case ruleIntOrNullRange:
		if v == nil {
			return nil, true, false
		}
		n, ok := validateAuditIntRange(v, rule.min, rule.max)
		return n, ok, false
	case ruleEnum:
		s, ok := validateAuditString(v, 64)
		if !ok {
			return nil, false, false
		}
		return s, auditEnumSets[rule.enumName][s], false
	case ruleEnumArray:
		if v == nil {
			return nil, true, false
		}
		arr, ok := v.([]any)
		if !ok || len(arr) > 64 {
			return nil, false, true
		}
		enumSet := auditEnumSets[rule.enumName]
		cleaned := make([]string, 0, len(arr))
		childStripped := false
		for _, elem := range arr {
			s, sOK := validateAuditString(elem, 64)
			if !sOK || !enumSet[s] {
				childStripped = true
				continue
			}
			cleaned = append(cleaned, s)
		}
		return cleaned, true, childStripped
	case ruleObjMovedCounts:
		if v == nil {
			return nil, true, false
		}
		obj, ok := v.(map[string]any)
		if !ok || len(obj) > 40 {
			return nil, false, true
		}
		cleaned := make(map[string]int64, len(obj))
		childStripped := false
		for mk, mv := range obj {
			if !auditMovedCountsAllowedKeys[mk] {
				childStripped = true
				continue
			}
			n, nOK := validateAuditIntRange(mv, 0, 100000)
			if !nOK {
				childStripped = true
				continue
			}
			cleaned[mk] = n
		}
		return cleaned, true, childStripped
	case ruleObjBlockoutGroupInner:
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, false, true
		}
		cleaned, childStripped := filterAuditObjectByAllowlist("blockout_group", obj, auditResourceAllowlists["blockout_group"], false)
		return cleaned, true, childStripped
	case ruleObjMaintRequestInner:
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, false, true
		}
		cleaned, childStripped := filterAuditObjectByAllowlist("maintenance_request", obj, auditResourceAllowlists["maintenance_request"], false)
		return cleaned, true, childStripped
	case ruleObjMaintInspection:
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, false, true
		}
		cleaned, childStripped := filterAuditObjectByAllowlist("maintenance_inspection", obj, maintInspectionAllowlist, false)
		return cleaned, true, childStripped
	default:
		return nil, false, true
	}
}

func validateAuditString(v any, maxLen int) (string, bool) {
	s, ok := v.(string)
	if !ok || len(s) > maxLen || len(s) > 128 {
		return "", false
	}
	if hasASCIIControlChars(s) || strings.Contains(s, "@") || domain.ContainsPII(s) {
		return "", false
	}
	return s, true
}

func validateAuditTimestamp(v any) (string, bool) {
	s, ok := validateAuditString(v, 64)
	if !ok {
		return "", false
	}
	if reAuditTimestamp.MatchString(s) {
		return s, true
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format(time.RFC3339Nano), true
	}
	return "", false
}

func validateAuditDateOrTimestamp(v any) (string, bool) {
	s, ok := validateAuditString(v, 64)
	if !ok {
		return "", false
	}
	if reAuditDate.MatchString(s) {
		return s, true
	}
	return validateAuditTimestamp(s)
}

func validateAuditIntRange(v any, minVal, maxVal int64) (int64, bool) {
	num, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	raw := num.String()
	if !reAuditInt.MatchString(raw) {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, n >= minVal && n <= maxVal
}

func isSafeAuditCode(val string, allowEmpty bool) bool {
	if val == "" {
		return allowEmpty
	}
	if len(val) > 64 || !reAuditCode.MatchString(val) || strings.Contains(val, "@") || domain.ContainsPII(val) {
		return false
	}
	compact := reAuditCodeStripper.Replace(val)
	if reAuditDigits7Plus.MatchString(compact) || reAuditUKPhoneRun.MatchString(val) || reAuditDigitRun9.MatchString(val) {
		return false
	}
	return true
}

func isSafeAuditOpaqueID(val string, allowEmpty bool) bool {
	if val == "" {
		return allowEmpty
	}
	if len(val) > 128 || !reAuditOpaqueID.MatchString(val) || strings.Contains(val, "@") {
		return false
	}
	if reAuditHexDigest.MatchString(val) && reAuditHasHexAlpha.MatchString(val) {
		return true
	}
	if reAuditUKPhoneRun.MatchString(val) || domain.ContainsPII(val) {
		return false
	}
	return true
}

func auditGuestSnapshot(g domain.Guest) map[string]any {
	return map[string]any{
		"guest_id":                 g.ID,
		"status":                   g.Status,
		"preferred_contact_method": g.PreferredContactMethod,
		"preferred_language":       g.PreferredLanguage,
		"merged_into_guest_id":     g.MergedIntoGuestID,
		"merged_at":                g.MergedAt,
		"anonymized_at":            g.AnonymizedAt,
		"has_email":                strings.TrimSpace(g.Email) != "",
		"has_phone":                strings.TrimSpace(g.Phone) != "",
		"has_notes":                strings.TrimSpace(g.Notes) != "",
		"version":                  g.Version,
	}
}

func auditGuestChangedFields(before, after domain.Guest) []string {
	changed := make([]string, 0, 10)
	if before.FirstName != after.FirstName || before.LastName != after.LastName || before.FullName != after.FullName {
		changed = append(changed, "name")
	}
	if before.Email != after.Email {
		changed = append(changed, "email")
	}
	if before.Phone != after.Phone {
		changed = append(changed, "phone")
	}
	if before.PreferredContactMethod != after.PreferredContactMethod {
		changed = append(changed, "preferred_contact_method")
	}
	if before.PreferredLanguage != after.PreferredLanguage {
		changed = append(changed, "preferred_language")
	}
	if before.Status != after.Status {
		changed = append(changed, "status")
	}
	if string(before.AddressJSON) != string(after.AddressJSON) {
		changed = append(changed, "address")
	}
	if string(before.Preferences) != string(after.Preferences) {
		changed = append(changed, "preferences")
	}
	if before.Notes != after.Notes {
		changed = append(changed, "notes")
	}
	sort.Strings(changed)
	return changed
}

func auditReservationSnapshot(r domain.Reservation) map[string]any {
	unitIDs := make([]string, 0, len(r.Units))
	for _, u := range r.Units {
		unitIDs = append(unitIDs, u.UnitID.String())
	}
	nights := 0
	if !r.ArrivalDate.IsZero() && !r.DepartureDate.IsZero() {
		nights = int(r.DepartureDate.Sub(r.ArrivalDate).Hours() / 24)
	}
	return map[string]any{
		"reservation_id":    r.ID,
		"reference":         r.Reference,
		"guest_id":          r.GuestID,
		"channel_code":      r.ChannelCode,
		"arrival_date":      domain.FormatDate(r.ArrivalDate),
		"departure_date":    domain.FormatDate(r.DepartureDate),
		"nights":            nights,
		"adults":            r.Adults,
		"children":          r.Children,
		"infants":           r.Infants,
		"status":            r.Status,
		"payment_status":    r.PaymentStatus,
		"total_pence":       r.TotalPence,
		"deposit_pence":     r.DepositPence,
		"amount_paid_pence": r.AmountPaidPence,
		"balance_due_pence": r.BalanceDuePence,
		"currency":          r.Currency,
		"unit_ids":          unitIDs,
		"version":           r.Version,
	}
}

func auditReservationChangedFields(before, after domain.Reservation) []string {
	changed := make([]string, 0, 12)
	if before.GuestName != after.GuestName {
		changed = append(changed, "guest_name")
	}
	if before.GuestEmail != after.GuestEmail {
		changed = append(changed, "guest_email")
	}
	if before.GuestPhone != after.GuestPhone {
		changed = append(changed, "guest_phone")
	}
	if before.OccupantDetails != after.OccupantDetails {
		changed = append(changed, "occupant_details")
	}
	if !before.ArrivalDate.Equal(after.ArrivalDate) || !before.DepartureDate.Equal(after.DepartureDate) {
		changed = append(changed, "stay_dates")
	}
	if before.Adults != after.Adults || before.Children != after.Children || before.Infants != after.Infants {
		changed = append(changed, "party_size")
	}
	if before.ChannelCode != after.ChannelCode {
		changed = append(changed, "channel_code")
	}
	if before.Status != after.Status {
		changed = append(changed, "status")
	}
	if before.PaymentStatus != after.PaymentStatus || before.TotalPence != after.TotalPence || before.AmountPaidPence != after.AmountPaidPence {
		changed = append(changed, "financials")
	}
	if before.InternalNotes != after.InternalNotes {
		changed = append(changed, "internal_notes")
	}
	sort.Strings(changed)
	return changed
}

func auditBookingHoldSnapshot(h domain.BookingHold) map[string]any {
	return map[string]any{
		"hold_id":                  h.ID,
		"hold_reference":           h.HoldReference,
		"guest_id":                 h.GuestID,
		"unit_type_id":             h.UnitTypeID,
		"rate_plan_id":             h.RatePlanID,
		"channel_code":             h.ChannelCode,
		"arrival_date":             h.Arrival,
		"departure_date":           h.Departure,
		"adults":                   h.Adults,
		"children":                 h.Children,
		"infants":                  h.Infants,
		"terms_version":            h.TermsVersion,
		"status":                   h.Status,
		"expires_at":               h.ExpiresAt,
		"confirmed_reservation_id": h.ConfirmedReservationID,
		"version":                  h.Version,
	}
}

func auditEnquirySnapshot(e domain.GuestEnquiry) map[string]any {
	return map[string]any{
		"enquiry_id":             e.ID,
		"reference":              e.Reference,
		"guest_id":               e.GuestID,
		"reservation_id":         e.ReservationID,
		"maintenance_request_id": e.MaintenanceRequestID,
		"category":               e.Category,
		"status":                 e.Status,
		"priority":               e.Priority,
		"source":                 e.Source,
		"assigned_user_id":       e.AssignedUserID,
		"target_response_at":     e.TargetResponseAt,
		"follow_up_at":           e.FollowUpAt,
		"resolved_at":            e.ResolvedAt,
		"closed_at":              e.ClosedAt,
		"version":                e.Version,
	}
}

func auditEnquiryMessageSnapshot(m domain.GuestEnquiryMessage) map[string]any {
	return map[string]any{
		"message_id":    m.ID,
		"enquiry_id":    m.EnquiryID,
		"direction":     m.Direction,
		"is_internal":   m.IsInternal,
		"staff_user_id": m.StaffUserID,
		"created_at":    m.CreatedAt,
	}
}

func auditCommunicationSnapshot(c domain.GuestCommunication) map[string]any {
	return map[string]any{
		"communication_id":    c.ID,
		"guest_id":            c.GuestID,
		"reservation_id":      c.ReservationID,
		"enquiry_id":          c.EnquiryID,
		"interaction_type":    c.InteractionType,
		"direction":           c.Direction,
		"template_id":         c.TemplateID,
		"template_version_id": c.TemplateVersionID,
		"rule_id":             c.RuleID,
		"trigger_event":       c.TriggerEvent,
		"email_outbox_id":     c.EmailOutboxID,
		"delivery_status":     c.DeliveryStatus,
		"follow_up_required":  c.FollowUpRequired,
		"staff_user_id":       c.StaffUserID,
		"occurred_at":         c.OccurredAt,
	}
}

func auditPreferenceSnapshot(p domain.GuestPreference) map[string]any {
	return map[string]any{
		"preference_id": p.ID,
		"guest_id":      p.GuestID,
		"category":      p.Category,
		"has_notes":     strings.TrimSpace(p.Notes) != "",
	}
}

func auditNoteSnapshot(n domain.GuestNote) map[string]any {
	return map[string]any{
		"note_id":        n.ID,
		"guest_id":       n.GuestID,
		"reservation_id": n.ReservationID,
		"category":       n.Category,
		"is_pinned":      n.IsPinned,
		"created_by":     n.CreatedBy,
	}
}

func auditMergeEventSnapshot(ev domain.GuestMergeEvent) map[string]any {
	return map[string]any{
		"merge_event_id":  ev.ID,
		"source_guest_id": ev.SourceGuestID,
		"target_guest_id": ev.TargetGuestID,
		"moved_counts":    ev.MovedCounts,
		"has_notes":       strings.TrimSpace(ev.Notes) != "",
		"merged_at":       ev.MergedAt,
	}
}

func auditConsentSnapshot(c domain.GuestConsentRecord) map[string]any {
	return map[string]any{
		"consent_record_id": c.ID,
		"guest_id":          c.GuestID,
		"consent_type":      c.ConsentType,
		"consent_status":    c.Status,
		"method":            c.Method,
		"source":            c.Source,
		"policy_version":    c.PolicyVersion,
		"occurred_at":       c.OccurredAt,
	}
}

func auditAdminUserSnapshot(u domain.AdminUser) map[string]any {
	return map[string]any{
		"user_id":              u.ID,
		"role":                 u.Role,
		"is_active":            u.IsActive,
		"must_rotate_password": u.MustRotatePassword,
		"property_id":          u.PropertyID,
		"version":              u.Version,
	}
}
