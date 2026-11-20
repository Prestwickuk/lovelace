package webui

import (
	"strings"
	"testing"

	"github.com/Prestwickuk/lovelace/internal/domain"
)

func TestBuildWebsiteEmbedSnippetsEscapesAndAppliesBrandTokens(t *testing.T) {
	prop := domain.Property{
		Slug: "meadowbrook-glamping",
		Name: `Meadowbrook "Meadow" & Woods <Site>`,
	}
	var pb domain.PropertyBrandingView
	pb.PublicProjection.DisplayName = `Meadowbrook "Meadow" & Woods <Site>`
	pb.PublicProjection.PrimaryColor = "#1F5A4C"
	pb.PublicProjection.PrimaryOnColor = "#FFFFFF"

	got := buildWebsiteEmbedSnippets("https://book.example.co.uk/extra-path/", prop, pb)

	if got.PublicOrigin != "https://book.example.co.uk" {
		t.Fatalf("PublicOrigin = %q, want https://book.example.co.uk", got.PublicOrigin)
	}
	if got.EmbedURL != "https://book.example.co.uk/embed/meadowbrook-glamping" {
		t.Fatalf("EmbedURL = %q", got.EmbedURL)
	}
	if got.DirectBookingURL != "https://book.example.co.uk/stays?property=meadowbrook-glamping" {
		t.Fatalf("DirectBookingURL = %q", got.DirectBookingURL)
	}

	for name, snippet := range map[string]string{
		"iframe":    got.IframeSnippet,
		"searchbar": got.SearchBarSnippet,
		"button":    got.ButtonSnippet,
	} {
		if strings.Contains(snippet, "<Site>") {
			t.Errorf("%s snippet contains unescaped HTML tag: %s", name, snippet)
		}
		if !strings.Contains(snippet, "&lt;Site&gt;") {
			t.Errorf("%s snippet missing escaped display name: %s", name, snippet)
		}
	}

	if !strings.Contains(got.IframeSnippet, `ev.origin !== "https://book.example.co.uk"`) {
		t.Errorf("iframe snippet missing strict postMessage origin check: %s", got.IframeSnippet)
	}
	if !strings.Contains(got.SearchBarSnippet, `background:#1F5A4C;color:#FFFFFF;`) {
		t.Errorf("search bar snippet missing verified brand colours: %s", got.SearchBarSnippet)
	}
	if !strings.Contains(got.SearchBarSnippet, `name="property" value="meadowbrook-glamping"`) {
		t.Errorf("search bar snippet missing hidden property field: %s", got.SearchBarSnippet)
	}
	if !strings.Contains(got.ButtonSnippet, `href="https://book.example.co.uk/stays?property=meadowbrook-glamping"`) {
		t.Errorf("button snippet missing direct booking URL: %s", got.ButtonSnippet)
	}
}
