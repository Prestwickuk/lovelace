package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/backup"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/httpx"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func (h *Handler) registerBackupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/backups", h.requireAuth(false, h.handleGetBackupOverview))
	mux.HandleFunc("POST /api/v1/backups/export", h.requireAuth(true, h.handleExportLocalBackup))
	mux.HandleFunc("POST /api/v1/backups/targets", h.requireAuth(true, h.handleCreateBackupTarget))
	mux.HandleFunc("PATCH /api/v1/backups/targets/{id}", h.requireAuth(true, h.handleUpdateBackupTarget))
	mux.HandleFunc("POST /api/v1/backups/targets/{id}/verify", h.requireAuth(true, h.handleVerifyBackupTarget))
	mux.HandleFunc("POST /api/v1/backups/targets/{id}/run", h.requireAuth(true, h.handleTriggerCloudBackup))
	mux.HandleFunc("DELETE /api/v1/backups/targets/{id}", h.requireAuth(true, h.handleDeleteBackupTarget))
	mux.HandleFunc("POST /api/v1/backups/verify", h.requireAuth(true, h.handleVerifyBackupArchive))
	mux.HandleFunc("POST /api/v1/backups/restore", h.requireAuth(true, h.handleRestoreBackupArchive))
}

func (h *Handler) handleGetBackupOverview(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	ov, err := h.svc.GetBackupOverview(r.Context(), actor)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	ov.Targets = nonNil(ov.Targets)
	ov.RecentRuns = nonNil(ov.RecentRuns)
	writeJSON(w, http.StatusOK, ov)
}

func (h *Handler) handleExportLocalBackup(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	var body struct {
		CurrentPassword   string `json:"current_password"`
		ArchivePassphrase string `json:"archive_passphrase"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxJSONBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	built, _, err := h.svc.ExportLocalBackupArchive(r.Context(), actor, body.CurrentPassword, body.ArchivePassphrase)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", built.Suggested))
	w.Header().Set("Content-Length", strconv.FormatInt(built.SizeBytes, 10))
	w.Header().Set("X-Backup-SHA256", built.SHA256)
	w.Header().Set("Cache-Control", "no-store, private")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(built.Bytes)
}

type apiBackupTargetPayload struct {
	CurrentPassword       string            `json:"current_password"`
	Version               int               `json:"version"`
	Name                  string            `json:"name"`
	Provider              string            `json:"provider"`
	BucketOrContainer     string            `json:"bucket_or_container"`
	Region                string            `json:"region"`
	EndpointURL           string            `json:"endpoint_url"`
	KeyPrefix             string            `json:"key_prefix"`
	EncryptArchive        bool              `json:"encrypt_archive"`
	ScheduleEnabled       bool              `json:"schedule_enabled"`
	ScheduleIntervalHours int               `json:"schedule_interval_hours"`
	RetentionCount        int               `json:"retention_count"`
	IsActive              bool              `json:"is_active"`
	Credentials           map[string]string `json:"credentials"`
}

func (h *Handler) handleCreateBackupTarget(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	var p apiBackupTargetPayload
	r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxJSONBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	created, err := h.svc.SaveBackupTarget(r.Context(), actor, service.SaveBackupTargetInput{
		CurrentPassword:       p.CurrentPassword,
		Name:                  p.Name,
		Provider:              domain.BackupProvider(p.Provider),
		BucketOrContainer:     p.BucketOrContainer,
		Region:                p.Region,
		EndpointURL:           p.EndpointURL,
		KeyPrefix:             p.KeyPrefix,
		EncryptArchive:        p.EncryptArchive,
		ScheduleEnabled:       p.ScheduleEnabled,
		ScheduleIntervalHours: p.ScheduleIntervalHours,
		RetentionCount:        p.RetentionCount,
		IsActive:              p.IsActive,
		Credentials:           p.Credentials,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleUpdateBackupTarget(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "invalid target id")
		return
	}
	var p apiBackupTargetPayload
	r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxJSONBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	updated, err := h.svc.SaveBackupTarget(r.Context(), actor, service.SaveBackupTargetInput{
		ID:                    &id,
		Version:               p.Version,
		CurrentPassword:       p.CurrentPassword,
		Name:                  p.Name,
		Provider:              domain.BackupProvider(p.Provider),
		BucketOrContainer:     p.BucketOrContainer,
		Region:                p.Region,
		EndpointURL:           p.EndpointURL,
		KeyPrefix:             p.KeyPrefix,
		EncryptArchive:        p.EncryptArchive,
		ScheduleEnabled:       p.ScheduleEnabled,
		ScheduleIntervalHours: p.ScheduleIntervalHours,
		RetentionCount:        p.RetentionCount,
		IsActive:              p.IsActive,
		Credentials:           p.Credentials,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleVerifyBackupTarget(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "invalid target id")
		return
	}
	target, err := h.svc.VerifyBackupTarget(r.Context(), actor, id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"target": target,
			"error": map[string]any{
				"code":    "cloud_verify_failed",
				"message": err.Error(),
			},
		})
		return
	}
	writeJSON(w, http.StatusOK, target)
}

func (h *Handler) handleTriggerCloudBackup(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "invalid target id")
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxJSONBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	run, err := h.svc.TriggerCloudBackup(r.Context(), actor, id, body.CurrentPassword)
	if err != nil {
		if run.ID != uuid.Nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"run": run,
				"error": map[string]any{
					"code":    "cloud_backup_failed",
					"message": err.Error(),
				},
			})
			return
		}
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) handleDeleteBackupTarget(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "invalid target id")
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxJSONBodyBytes)
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := h.svc.DeleteBackupTarget(r.Context(), actor, id, body.CurrentPassword); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readBackupMultipartUpload(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, backup.MaxBackupArchiveBytes+httpx.MaxFormBodyBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return nil, err
	}
	f, _, err := r.FormFile("archive")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, backup.MaxBackupArchiveBytes+1))
}

func (h *Handler) handleVerifyBackupArchive(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	raw, err := readBackupMultipartUpload(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "backup archive file is required and must be <= 48 MiB")
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	rep, err := h.svc.VerifyUploadedBackupArchive(r.Context(), actor, raw, r.FormValue("archive_passphrase"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (h *Handler) handleRestoreBackupArchive(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	raw, err := readBackupMultipartUpload(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "backup archive file is required and must be <= 48 MiB")
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	rep, err := h.svc.RestoreBackupIntoEmptyEnvironment(
		r.Context(),
		actor,
		r.FormValue("current_password"),
		raw,
		r.FormValue("archive_passphrase"),
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
