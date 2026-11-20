package wordpress

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestBuildPluginZipIncludesAllFilesGuardsAndEscapesPHP(t *testing.T) {
	rawZip, filename, err := BuildPluginZip(BundleConfig{
		BookingOrigin:  "https://book.example.co.uk/nested/path/",
		PropertySlug:   "tir-bach-glamping",
		PropertyName:   `O'Connor's \ Meadow\n'); phpinfo(); //`,
		PrimaryColor:   "#1F5A4C",
		PrimaryOnColor: "#FFFFFF",
	})
	if err != nil {
		t.Fatalf("BuildPluginZip() error = %v", err)
	}
	if filename != "lovelace-booking-tir-bach-glamping.zip" {
		t.Fatalf("filename = %q, want lovelace-booking-tir-bach-glamping.zip", filename)
	}

	zr, err := zip.NewReader(bytes.NewReader(rawZip), int64(len(rawZip)))
	if err != nil {
		t.Fatalf("zip.NewReader() error = %v", err)
	}

	files := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		rc, oerr := f.Open()
		if oerr != nil {
			t.Fatalf("open %s: %v", f.Name, oerr)
		}
		b, rerr := io.ReadAll(rc)
		_ = rc.Close()
		if rerr != nil {
			t.Fatalf("read %s: %v", f.Name, rerr)
		}
		files[f.Name] = string(b)
	}

	wantEntries := []string{
		"lovelace-booking/lovelace-booking.php",
		"lovelace-booking/readme.txt",
		"lovelace-booking/includes/bundled-defaults.php",
		"lovelace-booking/includes/class-lovelace-api-client.php",
		"lovelace-booking/includes/class-lovelace-settings.php",
		"lovelace-booking/includes/class-lovelace-renderer.php",
		"lovelace-booking/includes/class-lovelace-blocks.php",
		"lovelace-booking/assets/lovelace-frontend.css",
		"lovelace-booking/assets/lovelace-frontend.js",
		"lovelace-booking/assets/lovelace-blocks-editor.js",
	}
	for _, want := range wantEntries {
		if _, ok := files[want]; !ok {
			t.Errorf("missing expected zip entry %q", want)
		}
	}

	// Every PHP file in the archive must start with <?php and enforce ABSPATH direct-access guard.
	for name, src := range files {
		if !strings.HasSuffix(name, ".php") {
			continue
		}
		if !strings.HasPrefix(src, "<?php\n") {
			t.Errorf("%s does not start with <?php", name)
		}
		if !strings.Contains(src, "if ( ! defined( 'ABSPATH' ) )") {
			t.Errorf("%s missing ABSPATH direct-access guard", name)
		}
	}

	defaultsPHP := files["lovelace-booking/includes/bundled-defaults.php"]
	for _, want := range []string{
		`'booking_origin'   => 'https://book.example.co.uk'`,
		`'property_slug'    => 'tir-bach-glamping'`,
		`O\'Connor\'s \\ Meadow`,
		`\'`,
		`'primary_color'    => '#1F5A4C'`,
		`'primary_on_color' => '#FFFFFF'`,
	} {
		if !strings.Contains(defaultsPHP, want) {
			t.Errorf("bundled-defaults.php missing %q:\n%s", want, defaultsPHP)
		}
	}
}
