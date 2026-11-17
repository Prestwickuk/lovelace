package storage

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	MaxAttachmentImageDimension = 8192
	MaxAttachmentImagePixels    = 40_000_000
	healthProbeCacheTTL         = 5 * time.Second
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
	rootDir     string
	probeMu     sync.Mutex
	lastProbeAt time.Time
	lastPerm    os.FileMode
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
	if realAbs, err := filepath.EvalSymlinks(abs); err == nil {
		abs = realAbs
	}
	info, err := probeWritableDir(abs)
	if err != nil {
		return nil, fmt.Errorf("verify attachment directory writable: %w", err)
	}
	return &LocalFSStore{
		rootDir:     abs,
		lastProbeAt: time.Now(),
		lastPerm:    info.Mode().Perm(),
	}, nil
}

// HealthCheck verifies the local attachment storage directory exists, is a directory,
// and allows creating and removing files under the active process user.
// Directory existence and permissions are checked live on every call, while temporary
// write probes are throttled to prevent unauthenticated /readyz disk-write amplification.
func (s *LocalFSStore) HealthCheck() error {
	if s == nil || strings.TrimSpace(s.rootDir) == "" {
		return fmt.Errorf("attachment storage is not initialized")
	}
	info, err := os.Stat(s.rootDir)
	if err != nil {
		return fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("attachment path is not a directory")
	}
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	now := time.Now()
	if !s.lastProbeAt.IsZero() && now.Sub(s.lastProbeAt) < healthProbeCacheTTL && info.Mode().Perm() == s.lastPerm {
		return nil
	}
	probedInfo, err := probeWritableDir(s.rootDir)
	if err != nil {
		s.lastProbeAt = time.Time{}
		return err
	}
	s.lastProbeAt = now
	s.lastPerm = probedInfo.Mode().Perm()
	return nil
}

func probeWritableDir(dir string) (os.FileInfo, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("attachment path is not a directory")
	}
	probe, err := os.CreateTemp(dir, ".healthcheck-*")
	if err != nil {
		return nil, fmt.Errorf("directory is not writable: %w", err)
	}
	probePath := probe.Name()
	_ = probe.Close()
	if err := os.Remove(probePath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("cleanup healthcheck probe: %w", err)
	}
	return info, nil
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
		if err := validateAttachmentImageDimensions(expectedMIME, data); err != nil {
			return "", "", err
		}
	case "application/pdf":
		if detected != "application/pdf" {
			return "", "", fmt.Errorf("%w: invalid PDF signature", ErrUnsupportedFileType)
		}
	case "text/plain":
		if !strings.HasPrefix(detected, "text/plain") || bytes.IndexByte(data, 0) >= 0 {
			return "", "", fmt.Errorf("%w: binary payload rejected for .txt", ErrUnsupportedFileType)
		}
		lowerFull := strings.ToLower(string(data))
		if strings.Contains(lowerFull, "<html") || strings.Contains(lowerFull, "<!doctype html") || strings.Contains(lowerFull, "<script") || strings.Contains(lowerFull, "<svg") {
			return "", "", fmt.Errorf("%w: markup payload rejected", ErrUnsupportedFileType)
		}
	}

	return ext, expectedMIME, nil
}

func validateAttachmentImageDimensions(mimeType string, data []byte) error {
	checkBounds := func(w, h int) error {
		if w <= 0 || h <= 0 || w > MaxAttachmentImageDimension || h > MaxAttachmentImageDimension || int64(w)*int64(h) > MaxAttachmentImagePixels {
			return fmt.Errorf("%w: image dimensions %dx%d exceed allowed bounds", ErrUnsupportedFileType, w, h)
		}
		return nil
	}
	switch mimeType {
	case "image/png":
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("%w: invalid PNG header", ErrUnsupportedFileType)
		}
		return checkBounds(cfg.Width, cfg.Height)
	case "image/gif":
		cfg, err := gif.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("%w: invalid GIF header", ErrUnsupportedFileType)
		}
		return checkBounds(cfg.Width, cfg.Height)
	case "image/webp":
		if len(data) < 20 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
			return fmt.Errorf("%w: invalid WebP header", ErrUnsupportedFileType)
		}
		chunkID := string(data[12:16])
		switch chunkID {
		case "VP8 ":
			if len(data) >= 30 {
				w := int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff)
				h := int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
				return checkBounds(w, h)
			}
		case "VP8L":
			if len(data) >= 25 && data[20] == 0x2f {
				bits := binary.LittleEndian.Uint32(data[21:25])
				w := int(bits&0x3fff) + 1
				h := int((bits>>14)&0x3fff) + 1
				return checkBounds(w, h)
			}
		case "VP8X":
			if len(data) >= 30 {
				w := 1 + (int(data[24]) | int(data[25])<<8 | int(data[26])<<16)
				h := 1 + (int(data[27]) | int(data[28])<<8 | int(data[29])<<16)
				return checkBounds(w, h)
			}
		}
		return fmt.Errorf("%w: truncated WebP header", ErrUnsupportedFileType)
	case "image/jpeg":
		// Scan JPEG SOF markers when present to block 65535x65535 decompression bombs.
		if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
			return fmt.Errorf("%w: invalid JPEG signature", ErrUnsupportedFileType)
		}
		i := 2
		for i+3 < len(data) {
			if data[i] != 0xFF {
				i++
				continue
			}
			for i < len(data) && data[i] == 0xFF {
				i++
			}
			if i >= len(data) {
				break
			}
			marker := data[i]
			i++
			if marker == 0xD9 || marker == 0xDA {
				break
			}
			if marker == 0x00 || (marker >= 0xD0 && marker <= 0xD8) {
				continue
			}
			if i+2 > len(data) {
				break
			}
			segLen := int(binary.BigEndian.Uint16(data[i : i+2]))
			if segLen < 2 || i+segLen > len(data) {
				break
			}
			if marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC {
				if segLen >= 7 {
					h := int(binary.BigEndian.Uint16(data[i+3 : i+5]))
					w := int(binary.BigEndian.Uint16(data[i+5 : i+7]))
					if err := checkBounds(w, h); err != nil {
						return err
					}
				}
			}
			i += segLen
		}
	}
	return nil
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
	dirPath := filepath.Dir(fullPath)
	if err := os.MkdirAll(dirPath, 0o750); err != nil {
		return SavedBlob{}, fmt.Errorf("prepare attachment subdirectory: %w", err)
	}
	if err := s.verifyContainedDir(dirPath); err != nil {
		return SavedBlob{}, err
	}
	tmpFile, err := os.CreateTemp(dirPath, ".upload-*")
	if err != nil {
		return SavedBlob{}, fmt.Errorf("create attachment temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return SavedBlob{}, fmt.Errorf("write attachment: %w", err)
	}
	if err := tmpFile.Chmod(0o640); err != nil {
		_ = tmpFile.Close()
		return SavedBlob{}, fmt.Errorf("chmod attachment: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return SavedBlob{}, fmt.Errorf("close attachment temp file: %w", err)
	}
	if err := os.Rename(tmpPath, fullPath); err != nil {
		return SavedBlob{}, fmt.Errorf("commit attachment: %w", err)
	}
	cleanupTmp = false

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
	if err := s.verifyNoSymlinkEscape(fullPath); err != nil {
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
	if err := s.verifyNoSymlinkEscape(fullPath); err != nil {
		return err
	}
	err = os.Remove(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *LocalFSStore) verifyContainedDir(dirPath string) error {
	realDir, err := filepath.EvalSymlinks(dirPath)
	if err != nil {
		return ErrInvalidStorageKeyPath
	}
	rel, err := filepath.Rel(s.rootDir, realDir)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ErrInvalidStorageKeyPath
	}
	return nil
}

func (s *LocalFSStore) verifyNoSymlinkEscape(fullPath string) error {
	info, err := os.Lstat(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidStorageKeyPath
	}
	return s.verifyContainedDir(filepath.Dir(fullPath))
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
