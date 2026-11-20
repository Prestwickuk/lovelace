package webui

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/internal/backup"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/httpx"
	"github.com/Prestwickuk/lovelace/internal/service"
)

func (h *Handler) registerBackupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings/backups", h.requirePageAuth(false, h.handleBackupsGet))
	mux.HandleFunc("POST /settings/backups/export", h.requirePageAuth(true, h.handleBackupsLocalExportPost))
	mux.HandleFunc("POST /settings/backups/targets", h.requirePageAuth(true, h.handleBackupsTargetSavePost))
	mux.HandleFunc("POST /settings/backups/targets/{id}/verify", h.requirePageAuth(true, h.handleBackupsTargetVerifyPost))
	mux.HandleFunc("POST /settings/backups/targets/{id}/run", h.requirePageAuth(true, h.handleBackupsTargetRunPost))
	mux.HandleFunc("POST /settings/backups/targets/{id}/delete", h.requirePageAuth(true, h.handleBackupsTargetDeletePost))
	// Multipart archive upload routes verify CSRF after ParseMultipartForm:
	mux.HandleFunc("POST /settings/backups/verify", h.requirePageAuth(false, h.handleBackupsVerifyArchivePost))
	mux.HandleFunc("POST /settings/backups/restore", h.requirePageAuth(false, h.handleBackupsRestoreArchivePost))
}

type backupsViewData struct {
	baseView
	Overview           service.BackupOverview
	EditingTarget      *domain.BackupStorageTarget
	VerificationReport *domain.BackupVerificationReport
}

func (h *Handler) renderBackupsView(
	w http.ResponseWriter,
	r *http.Request,
	actor service.Actor,
	verifyReport *domain.BackupVerificationReport,
) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Backups & Cloud Storage", "backups")
	if err != nil {
		h.renderDomainError(w, err, "Backups Unavailable", "Unable to load backup settings.")
		return
	}
	if !base.CanCreateProperty {
		h.renderError(w, http.StatusForbidden, "Access Denied", "Only global owner administrators can access system backups and cloud storage destinations.")
		return
	}
	ov, err := h.svc.GetBackupOverview(r.Context(), actor)
	if err != nil {
		h.renderDomainError(w, err, "Backups Unavailable", "Unable to load backup overview.")
		return
	}

	var editing *domain.BackupStorageTarget
	if editID := parseOptionalUUID(r.URL.Query().Get("edit")); editID != nil {
		for i := range ov.Targets {
			if ov.Targets[i].ID == *editID {
				cp := ov.Targets[i]
				editing = &cp
				break
			}
		}
	}

	h.renderPage(w, "backups.html", backupsViewData{
		baseView:           base,
		Overview:           ov,
		EditingTarget:      editing,
		VerificationReport: verifyReport,
	})
}

func (h *Handler) handleBackupsGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	h.renderBackupsView(w, r, actor, nil)
}

func (h *Handler) handleBackupsLocalExportPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	built, _, err := h.svc.ExportLocalBackupArchive(
		r.Context(),
		actor,
		r.FormValue("current_password"),
		r.FormValue("archive_passphrase"),
	)
	if err != nil {
		h.redirectErr(w, r, "/settings/backups", err)
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

func (h *Handler) handleBackupsTargetSavePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	targetID := parseOptionalUUID(r.FormValue("target_id"))
	creds := map[string]string{
		"access_key_id":        r.FormValue("access_key_id"),
		"secret_access_key":    r.FormValue("secret_access_key"),
		"service_account_json": r.FormValue("service_account_json"),
		"account_name":         r.FormValue("account_name"),
		"account_key":          r.FormValue("account_key"),
		"sas_token":            r.FormValue("sas_token"),
		"archive_passphrase":   r.FormValue("archive_passphrase"),
	}
	saved, err := h.svc.SaveBackupTarget(r.Context(), actor, service.SaveBackupTargetInput{
		ID:                    targetID,
		Version:               parseIntDefault(r.FormValue("version"), 1),
		CurrentPassword:       r.FormValue("current_password"),
		Name:                  r.FormValue("name"),
		Provider:              domain.BackupProvider(r.FormValue("provider")),
		BucketOrContainer:     r.FormValue("bucket_or_container"),
		Region:                r.FormValue("region"),
		EndpointURL:           r.FormValue("endpoint_url"),
		KeyPrefix:             r.FormValue("key_prefix"),
		EncryptArchive:        r.FormValue("encrypt_archive") == "true",
		ScheduleEnabled:       r.FormValue("schedule_enabled") == "true",
		ScheduleIntervalHours: parseIntDefault(r.FormValue("schedule_interval_hours"), 24),
		RetentionCount:        parseIntDefault(r.FormValue("retention_count"), 14),
		IsActive:              r.FormValue("is_active") == "true",
		Credentials:           creds,
	})
	if err != nil {
		h.redirectErr(w, r, "/settings/backups", err)
		return
	}
	redirectMsg(w, r, "/settings/backups", fmt.Sprintf("Saved cloud backup destination %q (%s).", saved.Name, saved.Provider.Label()))
}

func (h *Handler) handleBackupsTargetVerifyPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Destination", "Invalid backup target ID.")
		return
	}
	target, err := h.svc.VerifyBackupTarget(r.Context(), actor, id)
	if err != nil {
		h.redirectErr(w, r, "/settings/backups", fmt.Errorf("bucket verification failed for %s: %w", target.Name, err))
		return
	}
	redirectMsg(w, r, "/settings/backups", fmt.Sprintf("Verified cloud connection to %q (%s).", target.Name, target.BucketOrContainer))
}

func (h *Handler) handleBackupsTargetRunPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Destination", "Invalid backup target ID.")
		return
	}
	run, err := h.svc.TriggerCloudBackup(r.Context(), actor, id, r.FormValue("current_password"))
	if err != nil {
		h.redirectErr(w, r, "/settings/backups", err)
		return
	}
	redirectMsg(w, r, "/settings/backups", fmt.Sprintf("Uploaded backup %s (%s) successfully.", run.ObjectKey, domain.FormatByteSize(run.SizeBytes)))
}

func (h *Handler) handleBackupsTargetDeletePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	id, err := uuid.Parse(strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Destination", "Invalid backup target ID.")
		return
	}
	if err := h.svc.DeleteBackupTarget(r.Context(), actor, id, r.FormValue("current_password")); err != nil {
		h.redirectErr(w, r, "/settings/backups", err)
		return
	}
	redirectMsg(w, r, "/settings/backups", "Cloud backup destination removed.")
}

func (h *Handler) parseBackupUploadWithCSRF(w http.ResponseWriter, r *http.Request, actor service.Actor) ([]byte, bool) {
	if actor.MustRotatePassword {
		http.Redirect(w, r, "/account?rotate=1", http.StatusSeeOther)
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, backup.MaxBackupArchiveBytes+httpx.MaxFormBodyBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Upload", "Backup archive upload exceeded the 48 MiB limit or was malformed.")
		return nil, false
	}
	if !httpx.VerifyCSRFToken(actor, r.FormValue("csrf_token")) {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		h.renderError(w, http.StatusForbidden, "Security Token Invalid", "Missing or invalid CSRF token. Please reload the page and try again.")
		return nil, false
	}
	f, _, err := r.FormFile("archive")
	if err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		h.redirectErr(w, r, "/settings/backups", fmt.Errorf("please choose a .zip or .zip.enc backup file"))
		return nil, false
	}
	defer f.Close()
	raw, rerr := io.ReadAll(io.LimitReader(f, backup.MaxBackupArchiveBytes+1))
	if r.MultipartForm != nil {
		_ = r.MultipartForm.RemoveAll()
	}
	if rerr != nil {
		h.redirectErr(w, r, "/settings/backups", fmt.Errorf("failed to read uploaded backup archive"))
		return nil, false
	}
	return raw, true
}

func (h *Handler) handleBackupsVerifyArchivePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	raw, ok := h.parseBackupUploadWithCSRF(w, r, actor)
	if !ok {
		return
	}
	rep, err := h.svc.VerifyUploadedBackupArchive(r.Context(), actor, raw, r.FormValue("archive_passphrase"))
	if err != nil {
		h.redirectErr(w, r, "/settings/backups", err)
		return
	}
	h.renderBackupsView(w, r, actor, &rep)
}

func (h *Handler) handleBackupsRestoreArchivePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	raw, ok := h.parseBackupUploadWithCSRF(w, r, actor)
	if !ok {
		return
	}
	rep, err := h.svc.RestoreBackupIntoEmptyEnvironment(
		r.Context(),
		actor,
		r.FormValue("current_password"),
		raw,
		r.FormValue("archive_passphrase"),
	)
	if err != nil {
		h.redirectErr(w, r, "/settings/backups", err)
		return
	}
	redirectMsg(w, r, "/", fmt.Sprintf("Backup restored successfully (%d rows across %d tables, %d attachment(s)).", rep.TotalRows, rep.TableCount, rep.AttachmentCount))
}
