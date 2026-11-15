package storage

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func minimalPNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE,
	}
}

func minimalJPEGBytes() []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xD9}
}

func minimalWEBPBytes() []byte {
	return []byte("RIFF\x18\x00\x00\x00WEBPVP8 \x0c\x00\x00\x00\x30\x01\x00\x9d\x01\x2a\x01\x00\x01\x00\x02\x00\x34\x25\xa4\x00")
}

func minimalPDFBytes() []byte {
	return []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n")
}

func TestValidateAndSniffAttachment(t *testing.T) {
	t.Run("accepts valid raster, pdf, and plain text files", func(t *testing.T) {
		cases := []struct {
			name string
			data []byte
			mime string
		}{
			{"leak.png", minimalPNGBytes(), "image/png"},
			{"gate.jpg", minimalJPEGBytes(), "image/jpeg"},
			{"tap.webp", minimalWEBPBytes(), "image/webp"},
			{"anim.gif", []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\xff\xff\xff\x00\x00\x00!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"), "image/gif"},
			{"invoice.pdf", minimalPDFBytes(), "application/pdf"},
			{"notes.txt", []byte("Replaced isolator switch and tested shower pressure."), "text/plain"},
		}
		for _, tc := range cases {
			_, gotMIME, err := ValidateAndSniffAttachment(tc.name, tc.data, 1024*1024)
			if err != nil {
				t.Fatalf("%s unexpectedly failed: %v", tc.name, err)
			}
			if gotMIME != tc.mime {
				t.Fatalf("%s MIME = %q, want %q", tc.name, gotMIME, tc.mime)
			}
		}
	})

	t.Run("rejects unsafe extensions, spoofed mimes, markup, and empty files", func(t *testing.T) {
		cases := []struct {
			name string
			data []byte
		}{
			{"diagram.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
			{"payload.html", []byte(`<html><script>alert(1)</script></html>`)},
			{"tool.exe", []byte("MZ\x90\x00\x03\x00\x00\x00")},
			{"disguised_svg.txt", []byte(`<svg onload="alert(1)">hello</svg>`)},
			{"disguised_html.txt", []byte(`<!doctype html><html><body>hi</body></html>`)},
			{"disguised_script.txt", []byte(`<script>fetch('/')</script>`)},
			{"spoofed.png", minimalJPEGBytes()},
			{"spoofed_text.jpg", []byte("not really a jpeg")},
			{"spoofed_bin.txt", []byte{0x7F, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}},
			{"empty.png", []byte{}},
		}
		for _, tc := range cases {
			_, _, err := ValidateAndSniffAttachment(tc.name, tc.data, 1024*1024)
			if !errors.Is(err, ErrUnsupportedFileType) {
				t.Fatalf("%s error = %v, want ErrUnsupportedFileType", tc.name, err)
			}
		}
	})

	t.Run("rejects oversized files", func(t *testing.T) {
		data := bytes.Repeat([]byte("a"), 2049)
		_, _, err := ValidateAndSniffAttachment("large.txt", data, 2048)
		if !errors.Is(err, ErrAttachmentTooLarge) {
			t.Fatalf("error = %v, want ErrAttachmentTooLarge", err)
		}
	})
}

func TestLocalFSStorePutOpenDeleteAndTraversal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "attachments")
	store, err := NewLocalFSStore(root)
	if err != nil {
		t.Fatalf("NewLocalFSStore: %v", err)
	}

	propID := uuid.New()
	payload := minimalPNGBytes()
	saved, err := store.Put(propID, "boiler fault.png", payload, 1024*1024)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if saved.ContentType != "image/png" || saved.SizeBytes != int64(len(payload)) || saved.SHA256 == "" {
		t.Fatalf("unexpected SavedBlob metadata: %+v", saved)
	}

	rc, err := store.Open(saved.StorageKey)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	readBack, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(readBack, payload) {
		t.Fatalf("stored bytes mismatch")
	}

	// Verify traversal and absolute keys are blocked.
	for _, badKey := range []string{"", "../secret.txt", "/etc/passwd", "subdir/../../etc/passwd", `sub\file.png`} {
		if _, err := store.Open(badKey); !errors.Is(err, ErrInvalidStorageKeyPath) {
			t.Fatalf("Open(%q) = %v, want ErrInvalidStorageKeyPath", badKey, err)
		}
		if err := store.Delete(badKey); !errors.Is(err, ErrInvalidStorageKeyPath) {
			t.Fatalf("Delete(%q) = %v, want ErrInvalidStorageKeyPath", badKey, err)
		}
	}

	// Verify deletion removes file and idempotent delete succeeds.
	if err := store.Delete(saved.StorageKey); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Open(saved.StorageKey); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("Open after delete = %v, want ErrAttachmentNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(saved.StorageKey))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("underlying file still exists after Delete")
	}
	if err := store.Delete(saved.StorageKey); err != nil {
		t.Fatalf("idempotent Delete failed: %v", err)
	}
}

func TestFilenameSanitizationAndPreviewCheck(t *testing.T) {
	got := SanitizeDownloadFilename(`../../my "weird" photo (1).png`)
	if got != "my__weird__photo__1_.png" {
		t.Fatalf("SanitizeDownloadFilename = %q", got)
	}
	if !IsInlinePreviewMIME("image/png") || !IsInlinePreviewMIME("image/jpeg") || IsInlinePreviewMIME("application/pdf") || IsInlinePreviewMIME("text/plain") {
		t.Fatalf("unexpected IsInlinePreviewMIME behavior")
	}
}

func TestLocalFSStoreHealthCheck(t *testing.T) {
	tmp := t.TempDir()
	// 1. Healthy directory succeeds on NewLocalFSStore and HealthCheck.
	attDir := filepath.Join(tmp, "healthy")
	store, err := NewLocalFSStore(attDir)
	if err != nil {
		t.Fatalf("NewLocalFSStore healthy: %v", err)
	}
	if err := store.HealthCheck(); err != nil {
		t.Fatalf("HealthCheck healthy: %v", err)
	}

	// 2. Path pointing to a regular file instead of directory must fail NewLocalFSStore.
	filePath := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write marker file: %v", err)
	}
	if _, err := NewLocalFSStore(filePath); err == nil {
		t.Fatal("expected NewLocalFSStore to reject regular file path")
	}

	// 3. Removing directory at runtime must cause HealthCheck to fail.
	if err := os.RemoveAll(attDir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if err := store.HealthCheck(); err == nil {
		t.Fatal("expected HealthCheck to fail after storage directory is removed")
	}

	// 4. Uninitialized nil receiver fails HealthCheck.
	var nilStore *LocalFSStore
	if err := nilStore.HealthCheck(); err == nil {
		t.Fatal("expected nil LocalFSStore HealthCheck to fail")
	}
}
