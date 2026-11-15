package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAttachmentTooLarge    = errors.New("attachment exceeds maximum allowed size")
	ErrUnsupportedFileType   = errors.New("unsupported attachment file type")
	ErrAttachmentNotFound    = errors.New("attachment object not found")
	ErrInvalidStorageKeyPath = errors.New("invalid attachment storage path")
)

var allowedExtensions = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
	".gif":  "image/gif",
	".pdf":  "application/pdf",
	".txt":  "text/plain",
}

// SavedBlob describes an attachment written to private blob storage.
type SavedBlob struct {
	StorageKey  string
	ContentType string
	SizeBytes   int64
	SHA256      string
}

// BlobStore persists non-public maintenance photographs and documents outside any web root.
type BlobStore interface {
	Put(propertyID uuid.UUID, originalFilename string, data []byte, maxBytes int64) (SavedBlob, error)
	Open(storageKey string) (io.ReadCloser, error)
	Delete(storageKey string) error
	HealthCheck() error
}

// LocalFSStore stores maintenance attachments inside an isolated directory outside web roots.
type LocalFSStore struct {
	rootDir string
}

func NewLocalFSStore(rootDir string) (*LocalFSStore, error) {
	cleaned := filepath.Clean(strings.TrimSpace(rootDir))
	if cleaned == "" || cleaned == "." {
		return nil, fmt.Errorf("attachment storage directory is required")
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return nil, fmt.Errorf("resolve attachment directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("create attachment directory: %w", err)
	}
	if err := probeWritableDir(abs); err != nil {
		return nil, fmt.Errorf("verify attachment directory writable: %w", err)
	}
	return &LocalFSStore{rootDir: abs}, nil
}

// HealthCheck verifies the local attachment storage directory exists, is a directory,
// and allows creating and removing files under the active process user.
func (s *LocalFSStore) HealthCheck() error {
	if s == nil || strings.TrimSpace(s.rootDir) == "" {
		return fmt.Errorf("attachment storage is not initialized")
	}
	return probeWritableDir(s.rootDir)
}

func probeWritableDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("attachment path is not a directory")
	}
	probe, err := os.CreateTemp(dir, ".healthcheck-*")
	if err != nil {
		return fmt.Errorf("directory is not writable: %w", err)
	}
	probePath := probe.Name()
	_ = probe.Close()
	if err := os.Remove(probePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cleanup healthcheck probe: %w", err)
	}
	return nil
}

// ValidateAndSniffAttachment validates file size, extension, and magic-byte MIME content type.
// Blocks executables, HTML, SVG, scripts, and disguised payloads.
func ValidateAndSniffAttachment(originalFilename string, data []byte, maxBytes int64) (string, string, error) {
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	if len(data) == 0 {
		return "", "", fmt.Errorf("%w: empty file", ErrUnsupportedFileType)
	}
	if int64(len(data)) > maxBytes {
		return "", "", ErrAttachmentTooLarge
	}

	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(originalFilename)))
	expectedMIME, ok := allowedExtensions[ext]
	if !ok {
		return "", "", ErrUnsupportedFileType
	}

	sniffLen := len(data)
	if sniffLen > 512 {
		sniffLen = 512
	}
	detected := strings.ToLower(strings.TrimSpace(http.DetectContentType(data[:sniffLen])))
	if idx := strings.IndexByte(detected, ';'); idx >= 0 {
		detected = strings.TrimSpace(detected[:idx])
	}

	switch expectedMIME {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		if detected != expectedMIME {
			return "", "", fmt.Errorf("%w: detected %s does not match %s", ErrUnsupportedFileType, detected, ext)
		}
	case "application/pdf":
		if detected != "application/pdf" {
			return "", "", fmt.Errorf("%w: invalid PDF signature", ErrUnsupportedFileType)
		}
	case "text/plain":
		if !strings.HasPrefix(detected, "text/plain") {
			return "", "", fmt.Errorf("%w: binary payload rejected for .txt", ErrUnsupportedFileType)
		}
		lowerSample := strings.ToLower(string(data[:sniffLen]))
		if strings.Contains(lowerSample, "<html") || strings.Contains(lowerSample, "<script") || strings.Contains(lowerSample, "<svg") {
			return "", "", fmt.Errorf("%w: markup payload rejected", ErrUnsupportedFileType)
		}
	}

	return ext, expectedMIME, nil
}

func (s *LocalFSStore) Put(propertyID uuid.UUID, originalFilename string, data []byte, maxBytes int64) (SavedBlob, error) {
	ext, mimeType, err := ValidateAndSniffAttachment(originalFilename, data, maxBytes)
	if err != nil {
		return SavedBlob{}, err
	}
	randBytes := make([]byte, 16)
	if _, err := rand.Read(randBytes); err != nil {
		return SavedBlob{}, fmt.Errorf("generate attachment key: %w", err)
	}
	now := time.Now().UTC()
	storageKey := filepath.ToSlash(filepath.Join(
		propertyID.String(),
		now.Format("2006-01"),
		hex.EncodeToString(randBytes)+ext,
	))
	fullPath, err := s.resolveSafePath(storageKey)
	if err != nil {
		return SavedBlob{}, err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o750); err != nil {
		return SavedBlob{}, fmt.Errorf("prepare attachment subdirectory: %w", err)
	}
	if err := os.WriteFile(fullPath, data, 0o640); err != nil {
		return SavedBlob{}, fmt.Errorf("write attachment: %w", err)
	}

	sum := sha256.Sum256(data)
	return SavedBlob{
		StorageKey:  storageKey,
		ContentType: mimeType,
		SizeBytes:   int64(len(data)),
		SHA256:      hex.EncodeToString(sum[:]),
	}, nil
}

func (s *LocalFSStore) Open(storageKey string) (io.ReadCloser, error) {
	fullPath, err := s.resolveSafePath(storageKey)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrAttachmentNotFound
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s *LocalFSStore) Delete(storageKey string) error {
	fullPath, err := s.resolveSafePath(storageKey)
	if err != nil {
		return err
	}
	err = os.Remove(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *LocalFSStore) resolveSafePath(storageKey string) (string, error) {
	trimmed := strings.TrimSpace(storageKey)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "..") || strings.ContainsRune(trimmed, '\\') {
		return "", ErrInvalidStorageKeyPath
	}
	candidate := filepath.Clean(filepath.Join(s.rootDir, filepath.FromSlash(trimmed)))
	rel, err := filepath.Rel(s.rootDir, candidate)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", ErrInvalidStorageKeyPath
	}
	return candidate, nil
}

// SanitizeDownloadFilename returns a safe ASCII filename for Content-Disposition headers.
func SanitizeDownloadFilename(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base == "/" {
		return "attachment"
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "attachment"
	}
	return out
}

func IsInlinePreviewMIME(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}
