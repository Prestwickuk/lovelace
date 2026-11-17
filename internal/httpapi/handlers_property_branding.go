package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/tirbach/tir-bach/internal/branding"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/storage"
)

func (h *Handler) registerPropertyBrandingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/branding", h.requireAuth(false, h.handleGetPropertyBranding))
	mux.HandleFunc("PUT /api/v1/properties/{propertyID}/branding", h.requireAuth(true, h.handlePutPropertyBranding))
	mux.HandleFunc("POST /api/v1/properties/{propertyID}/branding/logo", h.requireAuth(true, h.handleUploadPropertyBrandingLogo))
	mux.HandleFunc("DELETE /api/v1/properties/{propertyID}/branding/logo", h.requireAuth(true, h.handleDeletePropertyBrandingLogo))
	mux.HandleFunc("GET /api/v1/properties/{propertyID}/branding/logo", h.requireAuth(false, h.handleDownloadAdminPropertyLogo))
}

func extForLogoMIME(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func (h *Handler) handleGetPropertyBranding(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	view, err := h.svc.GetPropertyBranding(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handlePutPropertyBranding(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	var body struct {
		Version        int                       `json:"version"`
		DisplayName    string                    `json:"display_name"`
		Tagline        string                    `json:"tagline"`
		RegionLabel    string                    `json:"region_label"`
		PrimaryColor   string                    `json:"primary_color"`
		SecondaryColor string                    `json:"secondary_color"`
		LogoAlt        string                    `json:"logo_alt"`
		HeaderLabel    string                    `json:"header_label"`
		Terminology    branding.AdminTerminology `json:"terminology"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	view, err := h.svc.UpdatePropertyBranding(r.Context(), actor, propID, body.Version, branding.PropertyBrandingInput{
		DisplayName:    body.DisplayName,
		Tagline:        body.Tagline,
		RegionLabel:    body.RegionLabel,
		PrimaryColor:   body.PrimaryColor,
		SecondaryColor: body.SecondaryColor,
		LogoAlt:        body.LogoAlt,
		HeaderLabel:    body.HeaderLabel,
		Terminology:    body.Terminology,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handleUploadPropertyBrandingLogo(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, storage.MaxPropertyLogoBytes+256*1024)
	if err := r.ParseMultipartForm(storage.MaxPropertyLogoBytes + 64*1024); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "logo multipart upload exceeded limit or was malformed")
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "logo file is required")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, storage.MaxPropertyLogoBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "failed to read logo payload")
		return
	}
	version := 0
	if rawVer := strings.TrimSpace(r.FormValue("version")); rawVer != "" {
		if parsed, convErr := strconv.Atoi(rawVer); convErr == nil {
			version = parsed
		}
	}
	view, err := h.svc.UploadPropertyLogo(r.Context(), actor, propID, version, header.Filename, data)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handleDeletePropertyBrandingLogo(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	version := 0
	if rawVer := strings.TrimSpace(r.URL.Query().Get("version")); rawVer != "" {
		if parsed, err := strconv.Atoi(rawVer); err == nil {
			version = parsed
		}
	}
	view, err := h.svc.DeletePropertyLogo(r.Context(), actor, propID, version)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handleDownloadAdminPropertyLogo(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	propID, ok := parseUUIDParam(w, r, "propertyID")
	if !ok {
		return
	}
	view, rc, err := h.svc.OpenAdminPropertyLogo(r.Context(), actor, propID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", view.LogoContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="property-logo%s"`, extForLogoMIME(view.LogoContentType)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store, private")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func (h *Handler) handlePublicGetPropertyLogo(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	_, contentType, rc, err := h.svc.OpenPublicPropertyLogoBySlug(r.Context(), slug)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="property-logo%s"`, extForLogoMIME(contentType)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}
