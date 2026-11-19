package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildVerifyEncryptAndTamperDetection(t *testing.T) {
	attDir := t.TempDir()
	subDir := filepath.Join(attDir, "prop-1")
	if err := os.MkdirAll(subDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "logo.png"), []byte("fake-png-bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	// Dotfiles like .healthcheck-* must be ignored.
	if err := os.WriteFile(filepath.Join(attDir, ".healthcheck-123"), []byte("ignore"), 0o640); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 19, 14, 45, 0, 0, time.UTC)
	migs := []MigrationRecord{{Filename: "0001_init.sql", Checksum: "abc"}}
	tables := []TableDump{
		{
			Table: "properties",
			Rows:  []json.RawMessage{json.RawMessage(`{"id":"11111111-1111-1111-1111-111111111111","slug":"test-camp"}`)},
		},
	}

	// 1. Plain .zip build & verify
	plain, err := BuildArchive(now, migs, tables, attDir, "")
	if err != nil {
		t.Fatalf("BuildArchive plain: %v", err)
	}
	if plain.Encrypted {
		t.Fatalf("expected unencrypted archive")
	}
	unpackedPlain, err := VerifyAndUnpackArchive(plain.Bytes, "")
	if err != nil {
		t.Fatalf("VerifyAndUnpackArchive plain: %v", err)
	}
	if unpackedPlain.Report.TotalRows != 1 || unpackedPlain.Report.AttachmentCount != 1 {
		t.Fatalf("unexpected report counts: %+v", unpackedPlain.Report)
	}

	// 2. Encrypted .zip.enc build & verify
	pass := "BackupPassphrase!987654"
	enc, err := BuildArchive(now, migs, tables, attDir, pass)
	if err != nil {
		t.Fatalf("BuildArchive encrypted: %v", err)
	}
	if !enc.Encrypted || !IsEncryptedArchive(enc.Bytes) {
		t.Fatalf("expected encrypted archive")
	}
	if _, err := VerifyAndUnpackArchive(enc.Bytes, ""); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("expected ErrPassphraseRequired, got %v", err)
	}
	if _, err := VerifyAndUnpackArchive(enc.Bytes, "WrongPassphrase!987654"); !errors.Is(err, ErrPassphraseInvalid) {
		t.Fatalf("expected ErrPassphraseInvalid, got %v", err)
	}
	unpackedEnc, err := VerifyAndUnpackArchive(enc.Bytes, pass)
	if err != nil {
		t.Fatalf("VerifyAndUnpackArchive with right passphrase: %v", err)
	}
	if !unpackedEnc.Report.Encrypted || len(unpackedEnc.Tables["properties"]) != 1 {
		t.Fatalf("unexpected unpacked encrypted archive: %+v", unpackedEnc.Report)
	}

	// 3. Zip-slip path traversal rejection
	var evilBuf bytes.Buffer
	zw := zip.NewWriter(&evilBuf)
	w, _ := zw.Create("../../etc/passwd")
	_, _ = w.Write([]byte("root:x:0:0"))
	_ = zw.Close()
	if _, err := VerifyAndUnpackArchive(evilBuf.Bytes(), ""); !errors.Is(err, ErrCorruptArchive) {
		t.Fatalf("expected zip-slip path traversal to fail with ErrCorruptArchive, got %v", err)
	}
}

func TestIsBlockedCloudIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",
		"::1",
		"10.0.0.5",
		"172.16.5.4",
		"192.168.1.1",
		"169.254.169.254",
		"100.64.10.20",
		"fe80::1",
	}
	for _, s := range blocked {
		if !isBlockedCloudIP(netip.MustParseAddr(s)) {
			t.Errorf("expected %s to be blocked as SSRF/non-public IP", s)
		}
	}
	if isBlockedCloudIP(netip.MustParseAddr("93.184.216.34")) {
		t.Errorf("expected public IP 93.184.216.34 to be allowed")
	}
}
