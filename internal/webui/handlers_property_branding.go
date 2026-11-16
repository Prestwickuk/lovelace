package webui

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tirbach/tir-bach/internal/auth"
	"github.com/tirbach/tir-bach/internal/branding"
	"github.com/tirbach/tir-bach/internal/httpx"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/storage"
)

type propertyBrandingDraft struct {
	DisplayName    string
	Tagline        string
	RegionLabel    string
	PrimaryColor   string
	SecondaryColor string
	LogoAlt        string
	HeaderLabel    string
	UnitSingular   string
	UnitPlural     string
	GuestSingular  string
	GuestPlural    string
	StayLabel      string
	EffectiveTerms branding.AdminTerminology
}

func (h *Handler) registerPropertyBrandingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /branding/property.css", h.requirePageAuth(false, h.handlePropertyBrandingCSS))
	mux.HandleFunc("GET /property/logo", h.requirePageAuth(false, h.handlePropertyLogoGet))
	mux.HandleFunc("POST /property/branding", h.requirePageAuth(true, h.handlePropertyBrandingPost))
	mux.HandleFunc("POST /property/branding/logo", h.handlePropertyBrandingLogoUploadPost)
	mux.HandleFunc("POST /property/branding/logo/delete", h.requirePageAuth(true, h.handlePropertyBrandingLogoDeletePost))
}

func draftFromFormOrQuery(getVal func(string) string) propertyBrandingDraft {
	terms := branding.AdminTerminology{
		UnitSingular:  strings.TrimSpace(getVal("unit_singular")),
		UnitPlural:    strings.TrimSpace(getVal("unit_plural")),
		GuestSingular: strings.TrimSpace(getVal("guest_singular")),
		GuestPlural:   strings.TrimSpace(getVal("guest_plural")),
		StayLabel:     strings.TrimSpace(getVal("stay_label")),
	}
	return propertyBrandingDraft{
		DisplayName:    strings.TrimSpace(getVal("display_name")),
		Tagline:        strings.TrimSpace(getVal("tagline")),
		RegionLabel:    strings.TrimSpace(getVal("region_label")),
		PrimaryColor:   strings.TrimSpace(getVal("primary_color")),
		SecondaryColor: strings.TrimSpace(getVal("secondary_color")),
		LogoAlt:        strings.TrimSpace(getVal("logo_alt")),
		HeaderLabel:    strings.TrimSpace(getVal("header_label")),
		UnitSingular:   terms.UnitSingular,
		UnitPlural:     terms.UnitPlural,
		GuestSingular:  terms.GuestSingular,
		GuestPlural:    terms.GuestPlural,
		StayLabel:      terms.StayLabel,
		EffectiveTerms: branding.ResolveTerminology(terms),
	}
}

func (h *Handler) handlePropertyBrandingCSS(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store, private")

	if h.svc == nil || actor.MustRotatePassword {
		_, _ = io.WriteString(w, branding.RenderPropertyCSS(branding.ResolvedPropertyPalette{}))
		return
	}

	props, err := h.svc.ListProperties(r.Context(), actor)
	if err != nil || len(props) == 0 {
		_, _ = io.WriteString(w, branding.RenderPropertyCSS(branding.ResolvedPropertyPalette{}))
		return
	}
	prop := props[0]

	if r.URL.Query().Get("preview") == "1" && auth.HasPermission(actor.Role, auth.PermPropertyWrite) {
		pHex := r.URL.Query().Get("primary_color")
		sHex := r.URL.Query().Get("secondary_color")
		pal, perr := h.svc.ValidatePropertyBrandingPreview(r.Context(), actor, prop.ID, pHex, sHex)
		if perr == nil {
			_, _ = io.WriteString(w, branding.RenderPropertyCSS(pal))
			return
		}
	}

	view, err := h.svc.GetPropertyBranding(r.Context(), actor, prop.ID)
	if err != nil {
		_, _ = io.WriteString(w, branding.RenderPropertyCSS(branding.ResolvedPropertyPalette{}))
		return
	}
	_, _ = io.WriteString(w, branding.RenderPropertyCSS(view.Palette))
}

func (h *Handler) handlePropertyLogoGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	if h.svc == nil {
		http.NotFound(w, r)
		return
	}
	props, err := h.svc.ListProperties(r.Context(), actor)
	if err != nil || len(props) == 0 {
		http.NotFound(w, r)
		return
	}
	view, rc, err := h.svc.OpenAdminPropertyLogo(r.Context(), actor, props[0].ID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rc.Close()

	ext := ".png"
	switch view.LogoContentType {
	case "image/jpeg":
		ext = ".jpg"
	case "image/webp":
		ext = ".webp"
	}
	w.Header().Set("Content-Type", view.LogoContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="property-logo%s"`, ext))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store, private")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func (h *Handler) handlePropertyBrandingPost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Property & Site Settings", "property")
	if err != nil {
		h.renderDomainError(w, err, "Settings Unavailable", "Unable to load property branding context.")
		return
	}

	draft := draftFromFormOrQuery(r.FormValue)
	intent := strings.ToLower(strings.TrimSpace(r.FormValue("branding_action")))
	if intent == "preview" {
		if _, perr := h.svc.ValidatePropertyBrandingPreview(r.Context(), actor, base.Property.ID, draft.PrimaryColor, draft.SecondaryColor); perr != nil {
			h.redirectErr(w, r, "/property#property-branding", perr)
			return
		}
		if _, err := branding.SanitizeAndValidatePublicText("display name", draft.DisplayName, 80); err != nil {
			h.redirectErr(w, r, "/property#property-branding", err)
			return
		}
		if _, err := branding.SanitizeAndValidatePublicText("tagline", draft.Tagline, 140); err != nil {
			h.redirectErr(w, r, "/property#property-branding", err)
			return
		}
		if _, err := branding.SanitizeAndValidatePublicText("region label", draft.RegionLabel, 80); err != nil {
			h.redirectErr(w, r, "/property#property-branding", err)
			return
		}
		if _, err := branding.SanitizeAndValidatePublicText("logo alt text", draft.LogoAlt, 120); err != nil {
			h.redirectErr(w, r, "/property#property-branding", err)
			return
		}
		if _, err := branding.SanitizeAndValidatePublicText("admin workspace badge", draft.HeaderLabel, 48); err != nil {
			h.redirectErr(w, r, "/property#property-branding", err)
			return
		}
		if _, err := branding.SanitizeAndValidateTerminology(branding.AdminTerminology{
			UnitSingular:  draft.UnitSingular,
			UnitPlural:    draft.UnitPlural,
			GuestSingular: draft.GuestSingular,
			GuestPlural:   draft.GuestPlural,
			StayLabel:     draft.StayLabel,
		}); err != nil {
			h.redirectErr(w, r, "/property#property-branding", err)
			return
		}

		q := url.Values{}
		q.Set("preview", "1")
		q.Set("display_name", draft.DisplayName)
		q.Set("tagline", draft.Tagline)
		q.Set("region_label", draft.RegionLabel)
		q.Set("primary_color", strings.ToUpper(draft.PrimaryColor))
		q.Set("secondary_color", strings.ToUpper(draft.SecondaryColor))
		q.Set("logo_alt", draft.LogoAlt)
		q.Set("header_label", draft.HeaderLabel)
		q.Set("unit_singular", draft.UnitSingular)
		q.Set("unit_plural", draft.UnitPlural)
		q.Set("guest_singular", draft.GuestSingular)
		q.Set("guest_plural", draft.GuestPlural)
		q.Set("stay_label", draft.StayLabel)
		http.Redirect(w, r, "/property?"+q.Encode()+"#property-branding", http.StatusSeeOther)
		return
	}

	version := parseIntDefault(r.FormValue("branding_version"), 1)
	if _, err := h.svc.UpdatePropertyBranding(r.Context(), actor, base.Property.ID, version, branding.PropertyBrandingInput{
		DisplayName:    draft.DisplayName,
		Tagline:        draft.Tagline,
		RegionLabel:    draft.RegionLabel,
		PrimaryColor:   draft.PrimaryColor,
		SecondaryColor: draft.SecondaryColor,
		LogoAlt:        draft.LogoAlt,
		HeaderLabel:    draft.HeaderLabel,
		Terminology: branding.AdminTerminology{
			UnitSingular:  draft.UnitSingular,
			UnitPlural:    draft.UnitPlural,
			GuestSingular: draft.GuestSingular,
			GuestPlural:   draft.GuestPlural,
			StayLabel:     draft.StayLabel,
		},
	}); err != nil {
		h.redirectErr(w, r, "/property#property-branding", err)
		return
	}
	redirectMsg(w, r, "/property#property-branding", "Property branding saved.")
}

func (h *Handler) handlePropertyBrandingLogoUploadPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := httpx.ActorFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if actor.MustRotatePassword {
		http.Redirect(w, r, "/account?rotate=1", http.StatusSeeOther)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, storage.MaxPropertyLogoBytes+httpx.MaxFormBodyBytes)
	if err := r.ParseMultipartForm(storage.MaxPropertyLogoBytes + 64*1024); err != nil {
		h.renderError(w, http.StatusBadRequest, "Invalid Upload", "Property logo upload exceeded the 2 MiB limit or was malformed.")
		return
	}
	if !httpx.VerifyCSRFToken(actor, r.FormValue("csrf_token")) {
		h.renderError(w, http.StatusForbidden, "Security Token Invalid", "Missing or invalid CSRF token. Please reload the page and try again.")
		return
	}

	base, err := h.buildBaseView(r.Context(), r, actor, "Property & Site Settings", "property")
	if err != nil {
		h.renderDomainError(w, err, "Settings Unavailable", "Unable to load property branding context.")
		return
	}

	file, header, err := r.FormFile("logo")
	if err != nil {
		h.redirectErr(w, r, "/property#property-branding", fmt.Errorf("please choose a PNG, JPEG, or WebP logo file to upload"))
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, storage.MaxPropertyLogoBytes+1))
	if err != nil {
		h.redirectErr(w, r, "/property#property-branding", fmt.Errorf("failed reading uploaded logo file"))
		return
	}
	version := 0
	if rawVer := strings.TrimSpace(r.FormValue("branding_version")); rawVer != "" {
		if parsed, convErr := strconv.Atoi(rawVer); convErr == nil {
			version = parsed
		}
	}
	if _, err := h.svc.UploadPropertyLogo(r.Context(), actor, base.Property.ID, version, header.Filename, data); err != nil {
		h.redirectErr(w, r, "/property#property-branding", err)
		return
	}
	redirectMsg(w, r, "/property#property-branding", "Property logo uploaded.")
}

func (h *Handler) handlePropertyBrandingLogoDeletePost(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Property & Site Settings", "property")
	if err != nil {
		h.renderDomainError(w, err, "Settings Unavailable", "Unable to load property branding context.")
		return
	}
	version := parseIntDefault(r.FormValue("branding_version"), 0)
	if _, err := h.svc.DeletePropertyLogo(r.Context(), actor, base.Property.ID, version); err != nil {
		h.redirectErr(w, r, "/property#property-branding", err)
		return
	}
	redirectMsg(w, r, "/property#property-branding", "Property logo removed.")
}
