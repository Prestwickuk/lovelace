package webui

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Prestwickuk/lovelace/integrations/wordpress"
	"github.com/Prestwickuk/lovelace/internal/branding"
	"github.com/Prestwickuk/lovelace/internal/domain"
	"github.com/Prestwickuk/lovelace/internal/service"
)

// WebsiteEmbedSnippets holds ready-to-paste HTML snippets for embedding a property's booking flow on an external website.
type WebsiteEmbedSnippets struct {
	PublicOrigin     string
	PropertySlug     string
	EmbedURL         string
	DirectBookingURL string
	IframeSnippet    string
	SearchBarSnippet string
	ButtonSnippet    string
}

func buildWebsiteEmbedSnippets(rawPublicOrigin string, prop domain.Property, pb domain.PropertyBrandingView) WebsiteEmbedSnippets {
	origin := strings.TrimRight(strings.TrimSpace(rawPublicOrigin), "/")
	if origin == "" {
		origin = "https://your-booking-domain.example"
	} else if u, err := url.Parse(origin); err == nil && u.Scheme != "" && u.Host != "" {
		origin = u.Scheme + "://" + u.Host
	}

	slug := strings.TrimSpace(prop.Slug)
	if slug == "" {
		slug = "your-property"
	}
	escapedSlugPath := url.PathEscape(slug)
	escapedSlugQuery := url.QueryEscape(slug)

	displayName := strings.TrimSpace(pb.PublicProjection.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(prop.Name)
	}
	if displayName == "" {
		displayName = "Our Property"
	}

	primaryHex, err := branding.NormalizeOptionalHex("primary", pb.PublicProjection.PrimaryColor)
	if err != nil || primaryHex == "" {
		primaryHex = "#2F4F4A"
	}
	primaryOnHex, err := branding.NormalizeOptionalHex("primary_on", pb.PublicProjection.PrimaryOnColor)
	if err != nil || primaryOnHex == "" {
		primaryOnHex = "#FFFFFF"
	}

	embedURL := fmt.Sprintf("%s/embed/%s", origin, escapedSlugPath)
	staysBaseURL := fmt.Sprintf("%s/stays", origin)
	directBookingURL := fmt.Sprintf("%s?property=%s", staysBaseURL, escapedSlugQuery)

	escOrigin := html.EscapeString(origin)
	escEmbedURL := html.EscapeString(embedURL)
	escStaysURL := html.EscapeString(staysBaseURL)
	escDirectURL := html.EscapeString(directBookingURL)
	escSlug := html.EscapeString(slug)
	escName := html.EscapeString(displayName)
	iframeID := "lovelace-booking-" + escSlug

	iframeSnippet := fmt.Sprintf(`<!-- %s — Embedded Live Booking Section -->
<div class="lovelace-booking-embed">
  <iframe
    id="%s"
    src="%s"
    title="Book %s"
    width="100%%"
    height="760"
    frameborder="0"
    loading="lazy"
    style="width:100%%;min-height:680px;border:0;border-radius:12px;overflow:hidden;">
  </iframe>
</div>
<script>
(function () {
  var frame = document.getElementById("%s");
  if (!frame) return;
  window.addEventListener("message", function (ev) {
    if (ev.origin !== "%s" || !ev.data || ev.data.type !== "lovelace:embed:resize") return;
    if (ev.data.slug && ev.data.slug !== "%s") return;
    var h = Number(ev.data.height);
    if (h >= 420 && h <= 4000) frame.style.height = (h + 16) + "px";
  });
})();
</script>`, escName, iframeID, escEmbedURL, escName, iframeID, escOrigin, escSlug)

	searchBarSnippet := fmt.Sprintf(`<!-- %s — Drop-In Availability Search Bar (No JavaScript Required) -->
<form action="%s" method="get" target="_top" style="display:flex;flex-wrap:wrap;gap:12px;align-items:flex-end;padding:18px;border:1px solid #d6d3cb;border-radius:12px;background:#ffffff;font-family:system-ui,-apple-system,sans-serif;color:#1d2320;">
  <input type="hidden" name="property" value="%s">
  <div style="display:flex;flex-direction:column;gap:4px;flex:1 1 150px;">
    <label for="lv-arrival-%s" style="font-size:12px;font-weight:600;">Check-in</label>
    <input id="lv-arrival-%s" name="arrival" type="date" required style="padding:8px 10px;border:1px solid #b5b0a4;border-radius:8px;font:inherit;">
  </div>
  <div style="display:flex;flex-direction:column;gap:4px;flex:1 1 150px;">
    <label for="lv-departure-%s" style="font-size:12px;font-weight:600;">Check-out</label>
    <input id="lv-departure-%s" name="departure" type="date" required style="padding:8px 10px;border:1px solid #b5b0a4;border-radius:8px;font:inherit;">
  </div>
  <div style="display:flex;flex-direction:column;gap:4px;flex:0 1 120px;">
    <label for="lv-adults-%s" style="font-size:12px;font-weight:600;">Adults</label>
    <select id="lv-adults-%s" name="adults" style="padding:8px 10px;border:1px solid #b5b0a4;border-radius:8px;font:inherit;background:#ffffff;">
      <option value="1">1 Adult</option>
      <option value="2" selected>2 Adults</option>
      <option value="3">3 Adults</option>
      <option value="4">4 Adults</option>
      <option value="5">5 Adults</option>
      <option value="6">6 Adults</option>
    </select>
  </div>
  <div style="display:flex;flex-direction:column;gap:4px;flex:0 1 120px;">
    <label for="lv-children-%s" style="font-size:12px;font-weight:600;">Children</label>
    <select id="lv-children-%s" name="children" style="padding:8px 10px;border:1px solid #b5b0a4;border-radius:8px;font:inherit;background:#ffffff;">
      <option value="0" selected>0 Children</option>
      <option value="1">1 Child</option>
      <option value="2">2 Children</option>
      <option value="3">3 Children</option>
      <option value="4">4 Children</option>
    </select>
  </div>
  <button type="submit" style="padding:10px 20px;border:0;border-radius:8px;background:%s;color:%s;font:inherit;font-weight:600;cursor:pointer;">
    Check Availability
  </button>
</form>`,
		escName, escStaysURL, escSlug,
		escSlug, escSlug, escSlug, escSlug, escSlug, escSlug, escSlug, escSlug,
		primaryHex, primaryOnHex,
	)

	buttonSnippet := fmt.Sprintf(`<!-- %s — Direct Booking Button -->
<a href="%s" target="_blank" rel="noopener" style="display:inline-block;padding:12px 22px;border-radius:8px;background:%s;color:%s;font-family:system-ui,-apple-system,sans-serif;font-size:15px;font-weight:600;text-decoration:none;">
  Book Direct — %s
</a>`, escName, escDirectURL, primaryHex, primaryOnHex, escName)

	return WebsiteEmbedSnippets{
		PublicOrigin:     origin,
		PropertySlug:     slug,
		EmbedURL:         embedURL,
		DirectBookingURL: directBookingURL,
		IframeSnippet:    iframeSnippet,
		SearchBarSnippet: searchBarSnippet,
		ButtonSnippet:    buttonSnippet,
	}
}

func (h *Handler) handleWordPressPluginDownloadGet(w http.ResponseWriter, r *http.Request, actor service.Actor) {
	base, err := h.buildBaseView(r.Context(), r, actor, "Site Settings", "property")
	if err != nil {
		h.renderDomainError(w, err, "WordPress Plugin Unavailable", "Unable to load property configuration.")
		return
	}
	if !base.CanReadProperty {
		h.renderError(w, http.StatusForbidden, "Access Denied", "Your administrative role does not have permission to download site integration packages.")
		return
	}

	prop := base.Property
	pb := base.PropertyBranding
	if qID := parseOptionalUUID(r.URL.Query().Get("property_id")); qID != nil && *qID != uuid.Nil {
		p, gerr := h.svc.GetProperty(r.Context(), actor, *qID)
		if gerr != nil {
			h.renderDomainError(w, gerr, "Property Not Found", "The requested property could not be loaded.")
			return
		}
		prop = p
		if loadedPB, berr := h.svc.GetPropertyBranding(r.Context(), actor, p.ID); berr == nil {
			pb = loadedPB
		}
	}

	displayName := strings.TrimSpace(pb.PublicProjection.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(prop.Name)
	}

	zipBytes, filename, err := wordpress.BuildPluginZip(wordpress.BundleConfig{
		BookingOrigin:  h.cfg.PublicOrigin,
		PropertySlug:   prop.Slug,
		PropertyName:   displayName,
		PrimaryColor:   pb.PublicProjection.PrimaryColor,
		PrimaryOnColor: pb.PublicProjection.PrimaryOnColor,
	})
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Package Error", "Unable to generate WordPress plugin archive.")
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(zipBytes)))
	w.Header().Set("Cache-Control", "no-store, private")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(zipBytes)
}
