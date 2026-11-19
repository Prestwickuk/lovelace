package backup

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/tirbach/tir-bach/internal/domain"
)

const (
	ArchiveFormatVersion       = 1
	ArchiveApplicationID       = "lovelace"
	MaxBackupArchiveBytes      = 48 << 20  // 48 MiB compressed upload/audit ceiling
	MaxUnpackedTotalBytes      = 256 << 20 // 256 MiB total uncompressed zip-bomb guard
	MaxSingleEntryBytes        = 64 << 20  // 64 MiB single entry guard
	MinArchivePassphraseLength = 12
)

var (
	encryptedMagicHeader = []byte("LVBK1\x00")

	ErrPassphraseRequired = errors.New("backup archive is encrypted; passphrase is required")
	ErrPassphraseInvalid  = errors.New("unable to decrypt backup archive (incorrect passphrase or corrupted file)")
	ErrCorruptArchive     = errors.New("backup archive integrity verification failed")
)

type MigrationRecord struct {
	Filename string `json:"filename"`
	Checksum string `json:"checksum"`
}

type ManifestFileEntry struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type Manifest struct {
	FormatVersion      int                 `json:"format_version"`
	Application        string              `json:"application"`
	CreatedAt          time.Time           `json:"created_at"`
	SchemaMigrations   []MigrationRecord   `json:"schema_migrations"`
	TableOrder         []string            `json:"table_order"`
	TableCounts        map[string]int      `json:"table_counts"`
	TotalRows          int64               `json:"total_rows"`
	AttachmentCount    int                 `json:"attachment_count"`
	ExcludedSecretSets []string            `json:"excluded_secret_sets"`
	Files              []ManifestFileEntry `json:"files"`
}

type TableDump struct {
	Table string
	Rows  []json.RawMessage
}

type BuiltArchive struct {
	Bytes     []byte
	SHA256    string
	SizeBytes int64
	Encrypted bool
	Manifest  Manifest
	Suggested string
}

type UnpackedAttachment struct {
	RelPath string
	Data    []byte
	SHA256  string
}

type UnpackedArchive struct {
	Report      domain.BackupVerificationReport
	Manifest    Manifest
	Tables      map[string][]json.RawMessage
	Attachments []UnpackedAttachment
}

var ExcludedSecretSetsList = []string{
	"admin_sessions",
	"admin_recovery_codes",
	"login_attempts",
	"guest_portal_sessions",
	"guest_portal_challenges",
	"confirmation_access_tokens",
	"channel_hmac_nonces",
	"channel_connections.encrypted_credentials",
	"backup_storage_targets",
	"admin_users.password_hash",
}

// BuildArchive packages consistent table JSONL snapshots, local attachment blobs, and a SHA-256 manifest into a .zip (optionally encrypted with AES-256-GCM + Argon2id).
func BuildArchive(
	createdAt time.Time,
	migrations []MigrationRecord,
	tables []TableDump,
	attachmentDir string,
	passphrase string,
) (BuiltArchive, error) {
	createdAt = createdAt.UTC()
	passphrase = strings.TrimSpace(passphrase)
	if passphrase != "" && len(passphrase) < MinArchivePassphraseLength {
		return BuiltArchive{}, domain.NewValidationError(map[string]string{
			"passphrase": fmt.Sprintf("archive encryption passphrase must be at least %d characters", MinArchivePassphraseLength),
		})
	}

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)

	var files []ManifestFileEntry
	tableOrder := make([]string, 0, len(tables))
	tableCounts := make(map[string]int, len(tables))
	var totalRows int64

	writeEntry := func(entryPath string, data []byte) error {
		hdr := &zip.FileHeader{
			Name:     entryPath,
			Method:   zip.Deflate,
			Modified: createdAt,
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return fmt.Errorf("create zip entry %s: %w", entryPath, err)
		}
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("write zip entry %s: %w", entryPath, err)
		}
		sum := sha256.Sum256(data)
		files = append(files, ManifestFileEntry{
			Path:      entryPath,
			SizeBytes: int64(len(data)),
			SHA256:    hex.EncodeToString(sum[:]),
		})
		return nil
	}

	for idx, td := range tables {
		tableOrder = append(tableOrder, td.Table)
		tableCounts[td.Table] = len(td.Rows)
		totalRows += int64(len(td.Rows))

		var jsonl bytes.Buffer
		for _, row := range td.Rows {
			trimmed := bytes.TrimSpace(row)
			if len(trimmed) == 0 {
				continue
			}
			jsonl.Write(trimmed)
			jsonl.WriteByte('\n')
		}
		entryPath := fmt.Sprintf("tables/%02d_%s.jsonl", idx+1, td.Table)
		if err := writeEntry(entryPath, jsonl.Bytes()); err != nil {
			return BuiltArchive{}, err
		}
	}

	attachmentCount := 0
	cleanDir := strings.TrimSpace(attachmentDir)
	if cleanDir != "" {
		if info, err := os.Stat(cleanDir); err == nil && info.IsDir() {
			realRoot, err := filepath.EvalSymlinks(cleanDir)
			if err != nil {
				realRoot = cleanDir
			}
			var relPaths []string
			err = filepath.WalkDir(realRoot, func(fullPath string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if fullPath == realRoot {
					return nil
				}
				name := d.Name()
				if strings.HasPrefix(name, ".") {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if d.Type()&os.ModeSymlink != 0 {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if d.IsDir() {
					return nil
				}
				rel, err := filepath.Rel(realRoot, fullPath)
				if err != nil {
					return err
				}
				relSlash := filepath.ToSlash(rel)
				if strings.HasPrefix(relSlash, "../") || strings.Contains(relSlash, "/../") {
					return nil
				}
				relPaths = append(relPaths, relSlash)
				return nil
			})
			if err != nil {
				return BuiltArchive{}, fmt.Errorf("scan attachment directory: %w", err)
			}
			sort.Strings(relPaths)
			for _, relSlash := range relPaths {
				diskPath := filepath.Join(realRoot, filepath.FromSlash(relSlash))
				data, err := os.ReadFile(diskPath)
				if err != nil {
					return BuiltArchive{}, fmt.Errorf("read attachment %s: %w", relSlash, err)
				}
				if int64(len(data)) > MaxSingleEntryBytes {
					return BuiltArchive{}, fmt.Errorf("attachment %s exceeds backup entry size limit", relSlash)
				}
				if err := writeEntry("attachments/"+relSlash, data); err != nil {
					return BuiltArchive{}, err
				}
				attachmentCount++
			}
		}
	}

	manifest := Manifest{
		FormatVersion:      ArchiveFormatVersion,
		Application:        ArchiveApplicationID,
		CreatedAt:          createdAt,
		SchemaMigrations:   migrations,
		TableOrder:         tableOrder,
		TableCounts:        tableCounts,
		TotalRows:          totalRows,
		AttachmentCount:    attachmentCount,
		ExcludedSecretSets: ExcludedSecretSetsList,
		Files:              files,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BuiltArchive{}, fmt.Errorf("marshal manifest: %w", err)
	}
	hdr := &zip.FileHeader{
		Name:     "manifest.json",
		Method:   zip.Deflate,
		Modified: createdAt,
	}
	mw, err := zw.CreateHeader(hdr)
	if err != nil {
		return BuiltArchive{}, fmt.Errorf("create manifest.json: %w", err)
	}
	if _, err := mw.Write(manifestJSON); err != nil {
		return BuiltArchive{}, fmt.Errorf("write manifest.json: %w", err)
	}
	if err := zw.Close(); err != nil {
		return BuiltArchive{}, fmt.Errorf("finalize zip archive: %w", err)
	}

	outBytes := zipBuf.Bytes()
	encrypted := false
	ext := ".zip"
	if passphrase != "" {
		sealed, err := SealArchiveWithPassphrase(outBytes, passphrase)
		if err != nil {
			return BuiltArchive{}, err
		}
		outBytes = sealed
		encrypted = true
		ext = ".zip.enc"
	}
	if int64(len(outBytes)) > MaxBackupArchiveBytes {
		return BuiltArchive{}, fmt.Errorf("generated backup archive (%d bytes) exceeds %d byte ceiling", len(outBytes), MaxBackupArchiveBytes)
	}
	sum := sha256.Sum256(outBytes)
	ts := createdAt.Format("20060102-150405Z")
	return BuiltArchive{
		Bytes:     outBytes,
		SHA256:    hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(outBytes)),
		Encrypted: encrypted,
		Manifest:  manifest,
		Suggested: "lovelace-backup-" + ts + ext,
	}, nil
}

// IsEncryptedArchive reports whether raw starts with the LVBK1 encrypted envelope header.
func IsEncryptedArchive(raw []byte) bool {
	return bytes.HasPrefix(raw, encryptedMagicHeader)
}

func deriveArchiveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 2, 64*1024, 2, 32)
}

// SealArchiveWithPassphrase encrypts a .zip byte slice using Argon2id + AES-256-GCM.
func SealArchiveWithPassphrase(plainZip []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate archive salt: %w", err)
	}
	key := deriveArchiveKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init archive cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init archive gcm: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate archive nonce: %w", err)
	}
	aad := append(append([]byte{}, encryptedMagicHeader...), salt...)
	ciphertext := aead.Seal(nil, nonce, plainZip, aad)

	out := make([]byte, 0, len(encryptedMagicHeader)+len(salt)+len(nonce)+len(ciphertext))
	out = append(out, encryptedMagicHeader...)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

// OpenArchiveWithPassphrase decrypts an LVBK1 encrypted archive back into raw .zip bytes.
func OpenArchiveWithPassphrase(sealed []byte, passphrase string) ([]byte, error) {
	if !IsEncryptedArchive(sealed) {
		return sealed, nil
	}
	if strings.TrimSpace(passphrase) == "" {
		return nil, ErrPassphraseRequired
	}
	headerLen := len(encryptedMagicHeader) + 16 + 12
	if len(sealed) <= headerLen+16 {
		return nil, ErrPassphraseInvalid
	}
	salt := sealed[len(encryptedMagicHeader) : len(encryptedMagicHeader)+16]
	nonce := sealed[len(encryptedMagicHeader)+16 : headerLen]
	ciphertext := sealed[headerLen:]

	key := deriveArchiveKey(strings.TrimSpace(passphrase), salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrPassphraseInvalid
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrPassphraseInvalid
	}
	aad := append(append([]byte{}, encryptedMagicHeader...), salt...)
	plain, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrPassphraseInvalid
	}
	return plain, nil
}

func isSafeZipEntryPath(name string) bool {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return false
	}
	cleaned := path.Clean(name)
	if cleaned != name || cleaned == "." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return false
	}
	return cleaned == "manifest.json" || strings.HasPrefix(cleaned, "tables/") || strings.HasPrefix(cleaned, "attachments/")
}

// VerifyAndUnpackArchive validates outer encryption, zip structure, zip-bomb/path-traversal bounds, manifest metadata, and per-file SHA-256 digests.
func VerifyAndUnpackArchive(raw []byte, passphrase string) (UnpackedArchive, error) {
	if len(raw) == 0 {
		return UnpackedArchive{}, fmt.Errorf("%w: archive is empty", ErrCorruptArchive)
	}
	if int64(len(raw)) > MaxBackupArchiveBytes {
		return UnpackedArchive{}, fmt.Errorf("%w: archive exceeds maximum allowed size (%d bytes)", ErrCorruptArchive, MaxBackupArchiveBytes)
	}

	outerSum := sha256.Sum256(raw)
	outerSHA := hex.EncodeToString(outerSum[:])
	wasEncrypted := IsEncryptedArchive(raw)

	zipBytes, err := OpenArchiveWithPassphrase(raw, passphrase)
	if err != nil {
		return UnpackedArchive{}, err
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return UnpackedArchive{}, fmt.Errorf("%w: invalid zip container", ErrCorruptArchive)
	}

	entryBytes := make(map[string][]byte, len(zr.File))
	var totalUnpacked int64

	for _, f := range zr.File {
		if !isSafeZipEntryPath(f.Name) {
			return UnpackedArchive{}, fmt.Errorf("%w: disallowed archive entry path %q", ErrCorruptArchive, f.Name)
		}
		if f.Mode()&os.ModeSymlink != 0 || f.FileInfo().IsDir() {
			return UnpackedArchive{}, fmt.Errorf("%w: symlinks and directory entries are not allowed (%q)", ErrCorruptArchive, f.Name)
		}
		if _, dup := entryBytes[f.Name]; dup {
			return UnpackedArchive{}, fmt.Errorf("%w: duplicate archive entry %q", ErrCorruptArchive, f.Name)
		}
		if f.UncompressedSize64 > MaxSingleEntryBytes {
			return UnpackedArchive{}, fmt.Errorf("%w: entry %q exceeds size limit", ErrCorruptArchive, f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return UnpackedArchive{}, fmt.Errorf("%w: open %q: %v", ErrCorruptArchive, f.Name, err)
		}
		limited := io.LimitReader(rc, MaxSingleEntryBytes+1)
		data, rerr := io.ReadAll(limited)
		_ = rc.Close()
		if rerr != nil {
			return UnpackedArchive{}, fmt.Errorf("%w: read %q: %v", ErrCorruptArchive, f.Name, rerr)
		}
		if int64(len(data)) > MaxSingleEntryBytes {
			return UnpackedArchive{}, fmt.Errorf("%w: entry %q exceeds size limit", ErrCorruptArchive, f.Name)
		}
		totalUnpacked += int64(len(data))
		if totalUnpacked > MaxUnpackedTotalBytes {
			return UnpackedArchive{}, fmt.Errorf("%w: unpacked archive exceeds total size limit", ErrCorruptArchive)
		}
		entryBytes[f.Name] = data
	}

	manifestRaw, ok := entryBytes["manifest.json"]
	if !ok {
		return UnpackedArchive{}, fmt.Errorf("%w: missing manifest.json", ErrCorruptArchive)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return UnpackedArchive{}, fmt.Errorf("%w: invalid manifest.json", ErrCorruptArchive)
	}
	if manifest.FormatVersion != ArchiveFormatVersion || manifest.Application != ArchiveApplicationID {
		return UnpackedArchive{}, fmt.Errorf("%w: unsupported archive format (%s v%d)", ErrCorruptArchive, manifest.Application, manifest.FormatVersion)
	}
	if len(entryBytes) != len(manifest.Files)+1 {
		return UnpackedArchive{}, fmt.Errorf("%w: archive contains undeclared or missing entries", ErrCorruptArchive)
	}

	tables := make(map[string][]json.RawMessage, len(manifest.TableOrder))
	var attachments []UnpackedAttachment

	for _, mf := range manifest.Files {
		data, exists := entryBytes[mf.Path]
		if !exists {
			return UnpackedArchive{}, fmt.Errorf("%w: missing declared file %q", ErrCorruptArchive, mf.Path)
		}
		if int64(len(data)) != mf.SizeBytes {
			return UnpackedArchive{}, fmt.Errorf("%w: size mismatch on %q", ErrCorruptArchive, mf.Path)
		}
		sum := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), mf.SHA256) {
			return UnpackedArchive{}, fmt.Errorf("%w: SHA-256 digest mismatch on %q", ErrCorruptArchive, mf.Path)
		}

		if strings.HasPrefix(mf.Path, "tables/") && strings.HasSuffix(mf.Path, ".jsonl") {
			base := strings.TrimSuffix(strings.TrimPrefix(mf.Path, "tables/"), ".jsonl")
			if underscore := strings.IndexByte(base, '_'); underscore >= 0 && underscore+1 < len(base) {
				base = base[underscore+1:]
			}
			var rows []json.RawMessage
			if len(bytes.TrimSpace(data)) > 0 {
				lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
				rows = make([]json.RawMessage, 0, len(lines))
				for _, ln := range lines {
					ln = bytes.TrimSpace(ln)
					if len(ln) == 0 {
						continue
					}
					var js map[string]any
					if jerr := json.Unmarshal(ln, &js); jerr != nil {
						return UnpackedArchive{}, fmt.Errorf("%w: invalid JSON row in %q", ErrCorruptArchive, mf.Path)
					}
					cp := make([]byte, len(ln))
					copy(cp, ln)
					rows = append(rows, json.RawMessage(cp))
				}
			}
			if expectedCnt, ok := manifest.TableCounts[base]; ok && len(rows) != expectedCnt {
				return UnpackedArchive{}, fmt.Errorf("%w: row count mismatch for table %q (%d != %d)", ErrCorruptArchive, base, len(rows), expectedCnt)
			}
			tables[base] = rows
		} else if strings.HasPrefix(mf.Path, "attachments/") {
			rel := strings.TrimPrefix(mf.Path, "attachments/")
			if rel == "" || strings.Contains(rel, "..") {
				return UnpackedArchive{}, fmt.Errorf("%w: invalid attachment path %q", ErrCorruptArchive, mf.Path)
			}
			attachments = append(attachments, UnpackedAttachment{
				RelPath: rel,
				Data:    data,
				SHA256:  strings.ToLower(mf.SHA256),
			})
		}
	}

	latestMig := ""
	if len(manifest.SchemaMigrations) > 0 {
		latestMig = manifest.SchemaMigrations[len(manifest.SchemaMigrations)-1].Filename
	}

	report := domain.BackupVerificationReport{
		Valid:              true,
		Encrypted:          wasEncrypted,
		FormatVersion:      manifest.FormatVersion,
		Application:        manifest.Application,
		CreatedAt:          manifest.CreatedAt,
		ArchiveSHA256:      outerSHA,
		ArchiveSizeBytes:   int64(len(raw)),
		MigrationCount:     len(manifest.SchemaMigrations),
		LatestMigration:    latestMig,
		TableCount:         len(manifest.TableOrder),
		TotalRows:          manifest.TotalRows,
		AttachmentCount:    len(attachments),
		VerifiedFileCount:  len(manifest.Files),
		TableRowCounts:     manifest.TableCounts,
		ExcludedSecretSets: manifest.ExcludedSecretSets,
	}

	return UnpackedArchive{
		Report:      report,
		Manifest:    manifest,
		Tables:      tables,
		Attachments: attachments,
	}, nil
}
