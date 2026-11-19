package domain

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type BackupProvider string

const (
	BackupProviderLocalZip     BackupProvider = "LOCAL_ZIP"
	BackupProviderAWSS3        BackupProvider = "AWS_S3"
	BackupProviderS3Compatible BackupProvider = "S3_COMPATIBLE"
	BackupProviderGCS          BackupProvider = "GCS"
	BackupProviderAzureBlob    BackupProvider = "AZURE_BLOB"
)

func (p BackupProvider) ValidCloudTarget() bool {
	switch p {
	case BackupProviderAWSS3, BackupProviderS3Compatible, BackupProviderGCS, BackupProviderAzureBlob:
		return true
	default:
		return false
	}
}

func (p BackupProvider) Label() string {
	switch p {
	case BackupProviderLocalZip:
		return "Local .zip Download"
	case BackupProviderAWSS3:
		return "Amazon S3 (AWS)"
	case BackupProviderS3Compatible:
		return "S3-Compatible (Cloudflare R2 / Backblaze B2 / Wasabi)"
	case BackupProviderGCS:
		return "Google Cloud Storage (GCP)"
	case BackupProviderAzureBlob:
		return "Microsoft Azure Blob Storage"
	default:
		return string(p)
	}
}

type BackupTargetStatus string

const (
	BackupTargetStatusNeverRun  BackupTargetStatus = "NEVER_RUN"
	BackupTargetStatusVerified  BackupTargetStatus = "VERIFIED"
	BackupTargetStatusSucceeded BackupTargetStatus = "SUCCEEDED"
	BackupTargetStatusFailed    BackupTargetStatus = "FAILED"
)

type BackupRunKind string

const (
	BackupRunLocalDownload  BackupRunKind = "LOCAL_DOWNLOAD"
	BackupRunCloudManual    BackupRunKind = "CLOUD_MANUAL"
	BackupRunCloudScheduled BackupRunKind = "CLOUD_SCHEDULED"
	BackupRunVerifyArchive  BackupRunKind = "VERIFY_ARCHIVE"
	BackupRunRestoreEmptyDB BackupRunKind = "RESTORE_EMPTY_DB"
)

type BackupRunStatus string

const (
	BackupRunStatusRunning   BackupRunStatus = "RUNNING"
	BackupRunStatusSucceeded BackupRunStatus = "SUCCEEDED"
	BackupRunStatusFailed    BackupRunStatus = "FAILED"
)

// BackupStorageTarget represents a configured cloud bucket/container destination.
// EncryptedCredentials is never serialized to JSON responses.
type BackupStorageTarget struct {
	ID                    uuid.UUID          `json:"id"`
	Name                  string             `json:"name"`
	Provider              BackupProvider     `json:"provider"`
	BucketOrContainer     string             `json:"bucket_or_container"`
	Region                string             `json:"region"`
	EndpointURL           string             `json:"endpoint_url,omitempty"`
	KeyPrefix             string             `json:"key_prefix"`
	EncryptedCredentials  []byte             `json:"-"`
	CredentialHint        string             `json:"credential_hint"`
	EncryptArchive        bool               `json:"encrypt_archive"`
	ScheduleEnabled       bool               `json:"schedule_enabled"`
	ScheduleIntervalHours int                `json:"schedule_interval_hours"`
	RetentionCount        int                `json:"retention_count"`
	IsActive              bool               `json:"is_active"`
	LastVerifiedAt        *time.Time         `json:"last_verified_at,omitempty"`
	LastBackupAt          *time.Time         `json:"last_backup_at,omitempty"`
	LastStatus            BackupTargetStatus `json:"last_status"`
	LastErrorSummary      string             `json:"last_error_summary,omitempty"`
	NextRunAt             *time.Time         `json:"next_run_at,omitempty"`
	CreatedBy             *uuid.UUID         `json:"created_by,omitempty"`
	UpdatedBy             *uuid.UUID         `json:"updated_by,omitempty"`
	Version               int                `json:"version"`
	CreatedAt             time.Time          `json:"created_at"`
	UpdatedAt             time.Time          `json:"updated_at"`
}

func (t *BackupStorageTarget) Validate(allowInsecureLoopback bool) error {
	errs := make(map[string]string)
	t.Name = strings.TrimSpace(t.Name)
	t.BucketOrContainer = strings.TrimSpace(t.BucketOrContainer)
	t.Region = strings.TrimSpace(t.Region)
	t.EndpointURL = strings.TrimSpace(t.EndpointURL)
	t.KeyPrefix = NormalizeBackupPrefix(t.KeyPrefix)

	if len(t.Name) < 1 || len(t.Name) > 80 {
		errs["name"] = "must be between 1 and 80 characters"
	}
	if !t.Provider.ValidCloudTarget() {
		errs["provider"] = "must be AWS_S3, S3_COMPATIBLE, GCS, or AZURE_BLOB"
	}
	if len(t.BucketOrContainer) < 1 || len(t.BucketOrContainer) > 128 || strings.ContainsAny(t.BucketOrContainer, "/\\?#@ ") {
		errs["bucket_or_container"] = "must be a valid bucket or container name without slashes or spaces"
	}
	if t.Provider == BackupProviderAWSS3 && t.Region == "" {
		errs["region"] = "AWS region is required (e.g. eu-west-2)"
	}
	if t.Provider == BackupProviderS3Compatible && t.EndpointURL == "" {
		errs["endpoint_url"] = "HTTPS endpoint URL is required for S3-compatible storage (e.g. https://<account>.r2.cloudflarestorage.com)"
	}
	if t.EndpointURL != "" {
		u, err := url.Parse(t.EndpointURL)
		if err != nil || u.Host == "" {
			errs["endpoint_url"] = "must be a valid URL"
		} else if u.Scheme != "https" && !(allowInsecureLoopback && u.Scheme == "http") {
			errs["endpoint_url"] = "must use https://"
		} else if u.User != nil {
			errs["endpoint_url"] = "must not embed credentials in URL"
		}
	}
	if t.ScheduleIntervalHours < 1 || t.ScheduleIntervalHours > 720 {
		errs["schedule_interval_hours"] = "must be between 1 and 720 hours"
	}
	if t.RetentionCount < 1 || t.RetentionCount > 365 {
		errs["retention_count"] = "must be between 1 and 365 backups"
	}
	return NewValidationError(errs)
}

// NormalizeBackupPrefix normalizes an object storage prefix to a safe relative slash prefix.
func NormalizeBackupPrefix(raw string) string {
	s := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	s = strings.TrimLeft(s, "/")
	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	if strings.Contains(s, "..") {
		return "lovelace-backups/"
	}
	if s == "" {
		return "lovelace-backups/"
	}
	if !strings.HasSuffix(s, "/") {
		s += "/"
	}
	return s
}

// BackupRun records a local download, cloud upload, archive verification, or empty-DB restore operation.
type BackupRun struct {
	ID              uuid.UUID       `json:"id"`
	TargetID        *uuid.UUID      `json:"target_id,omitempty"`
	RunKind         BackupRunKind   `json:"run_kind"`
	Provider        BackupProvider  `json:"provider"`
	Status          BackupRunStatus `json:"status"`
	Encrypted       bool            `json:"encrypted"`
	ObjectKey       string          `json:"object_key,omitempty"`
	ArchiveSHA256   string          `json:"archive_sha256,omitempty"`
	SizeBytes       int64           `json:"size_bytes"`
	TableCount      int             `json:"table_count"`
	RowCount        int64           `json:"row_count"`
	AttachmentCount int             `json:"attachment_count"`
	ErrorSummary    string          `json:"error_summary,omitempty"`
	TriggeredBy     *uuid.UUID      `json:"triggered_by,omitempty"`
	StartedAt       time.Time       `json:"started_at"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
}

// BackupVerificationReport summarizes manifest & SHA-256 verification of a backup archive.
type BackupVerificationReport struct {
	Valid              bool           `json:"valid"`
	Encrypted          bool           `json:"encrypted"`
	FormatVersion      int            `json:"format_version"`
	Application        string         `json:"application"`
	CreatedAt          time.Time      `json:"created_at"`
	ArchiveSHA256      string         `json:"archive_sha256"`
	ArchiveSizeBytes   int64          `json:"archive_size_bytes"`
	MigrationCount     int            `json:"migration_count"`
	LatestMigration    string         `json:"latest_migration"`
	TableCount         int            `json:"table_count"`
	TotalRows          int64          `json:"total_rows"`
	AttachmentCount    int            `json:"attachment_count"`
	VerifiedFileCount  int            `json:"verified_file_count"`
	TableRowCounts     map[string]int `json:"table_row_counts"`
	ExcludedSecretSets []string       `json:"excluded_secret_sets"`
}

// FormatByteSize returns a human-readable byte size string (e.g. "1.4 MiB").
func FormatByteSize(b int64) string {
	switch {
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.2f GiB", float64(b)/(1024*1024*1024))
	case b >= 1024*1024:
		return fmt.Sprintf("%.2f MiB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KiB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
