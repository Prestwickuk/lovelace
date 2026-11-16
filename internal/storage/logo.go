package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"path/filepath"
	"strings"
)

const (
	// MaxPropertyLogoBytes caps uploaded logo payload size before and after sanitization.
	MaxPropertyLogoBytes = 2 * 1024 * 1024 // 2 MiB
	// MinPropertyLogoDimension rejects degenerate 1x1 tracking pixels / tiny blobs.
	MinPropertyLogoDimension = 16
	// MaxPropertyLogoDimension caps decoded pixel width and height to block image bombs.
	MaxPropertyLogoDimension = 2048
	// MaxPropertyLogoPixels caps total decoded pixel count.
	MaxPropertyLogoPixels = 2048 * 2048
)

var (
	// ErrInvalidLogoDimensions indicates an image violates allowed width/height/pixel caps.
	ErrInvalidLogoDimensions = errors.New("property logo dimensions are outside allowed bounds")
	// ErrCorruptLogoImage indicates a file fails image signature, container, or decode verification.
	ErrCorruptLogoImage = errors.New("property logo image could not be safely decoded")
)

var allowedPropertyLogoExtensions = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
}

// SanitizedPropertyLogo holds a verified, metadata-stripped property logo image ready for BlobStore.Put.
type SanitizedPropertyLogo struct {
	Data        []byte
	ContentType string
	Extension   string
	Width       int
	Height      int
	SHA256      string
}

// IsAllowedPropertyLogoMIME checks whether contentType is one of the strictly supported logo image formats.
func IsAllowedPropertyLogoMIME(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png", "image/jpeg", "image/webp":
		return true
	default:
		return false
	}
}

// ValidateAndSanitizePropertyLogo enforces strict allowlist, magic-byte sniffing, dimension limits,
// decompression-bomb protection, SVG/HTML rejection, and metadata/EXIF stripping for property logos.
func ValidateAndSanitizePropertyLogo(originalFilename string, data []byte, maxBytes int64) (SanitizedPropertyLogo, error) {
	if maxBytes <= 0 || maxBytes > MaxPropertyLogoBytes {
		maxBytes = MaxPropertyLogoBytes
	}
	if len(data) == 0 {
		return SanitizedPropertyLogo{}, fmt.Errorf("%w: empty file", ErrUnsupportedFileType)
	}
	if int64(len(data)) > maxBytes {
		return SanitizedPropertyLogo{}, ErrAttachmentTooLarge
	}

	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(originalFilename)))
	expectedMIME, ok := allowedPropertyLogoExtensions[ext]
	if !ok {
		return SanitizedPropertyLogo{}, fmt.Errorf("%w: only PNG, JPEG, and WebP property logos are supported", ErrUnsupportedFileType)
	}

	// Explicitly block SVG, XML, HTML, or script polyglots regardless of filename.
	sniffLen := len(data)
	if sniffLen > 512 {
		sniffLen = 512
	}
	lowerPrefix := strings.ToLower(string(data[:sniffLen]))
	if strings.Contains(lowerPrefix, "<svg") ||
		strings.Contains(lowerPrefix, "<html") ||
		strings.Contains(lowerPrefix, "<?xml") ||
		strings.Contains(lowerPrefix, "<script") {
		return SanitizedPropertyLogo{}, fmt.Errorf("%w: markup or SVG payloads are rejected", ErrUnsupportedFileType)
	}

	detected := strings.ToLower(strings.TrimSpace(http.DetectContentType(data[:sniffLen])))
	if idx := strings.IndexByte(detected, ';'); idx >= 0 {
		detected = strings.TrimSpace(detected[:idx])
	}
	if detected != expectedMIME {
		return SanitizedPropertyLogo{}, fmt.Errorf("%w: detected %s does not match %s", ErrUnsupportedFileType, detected, ext)
	}

	var (
		cleanBytes []byte
		width      int
		height     int
		err        error
	)

	switch expectedMIME {
	case "image/png":
		if !bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
			return SanitizedPropertyLogo{}, fmt.Errorf("%w: invalid PNG signature", ErrCorruptLogoImage)
		}
		cleanBytes, width, height, err = sanitizePNGLogo(data)
		ext = ".png"
	case "image/jpeg":
		if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
			return SanitizedPropertyLogo{}, fmt.Errorf("%w: invalid JPEG signature", ErrCorruptLogoImage)
		}
		cleanBytes, width, height, err = sanitizeJPEGLogo(data)
		ext = ".jpg"
	case "image/webp":
		cleanBytes, width, height, err = sanitizeWebPLogo(data)
		ext = ".webp"
	default:
		return SanitizedPropertyLogo{}, ErrUnsupportedFileType
	}
	if err != nil {
		return SanitizedPropertyLogo{}, err
	}
	if int64(len(cleanBytes)) > maxBytes || len(cleanBytes) == 0 {
		return SanitizedPropertyLogo{}, ErrAttachmentTooLarge
	}

	sum := sha256.Sum256(cleanBytes)
	return SanitizedPropertyLogo{
		Data:        cleanBytes,
		ContentType: expectedMIME,
		Extension:   ext,
		Width:       width,
		Height:      height,
		SHA256:      hex.EncodeToString(sum[:]),
	}, nil
}

func validateLogoDimensions(w, h int) error {
	if w < MinPropertyLogoDimension || h < MinPropertyLogoDimension {
		return fmt.Errorf("%w: minimum dimensions are %dx%d pixels", ErrInvalidLogoDimensions, MinPropertyLogoDimension, MinPropertyLogoDimension)
	}
	if w > MaxPropertyLogoDimension || h > MaxPropertyLogoDimension {
		return fmt.Errorf("%w: maximum dimensions are %dx%d pixels", ErrInvalidLogoDimensions, MaxPropertyLogoDimension, MaxPropertyLogoDimension)
	}
	if int64(w)*int64(h) > MaxPropertyLogoPixels {
		return fmt.Errorf("%w: pixel count exceeds limit", ErrInvalidLogoDimensions)
	}
	return nil
}

func sanitizePNGLogo(data []byte) ([]byte, int, int, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%w: %v", ErrCorruptLogoImage, err)
	}
	if err := validateLogoDimensions(cfg.Width, cfg.Height); err != nil {
		return nil, 0, 0, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%w: %v", ErrCorruptLogoImage, err)
	}
	b := img.Bounds()
	if err := validateLogoDimensions(b.Dx(), b.Dy()); err != nil {
		return nil, 0, 0, err
	}
	// Re-encoding strips auxiliary text/eXIf/zTXt/iTXt chunks and trailing polyglot bytes.
	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&out, img); err != nil {
		return nil, 0, 0, fmt.Errorf("%w: %v", ErrCorruptLogoImage, err)
	}
	return out.Bytes(), b.Dx(), b.Dy(), nil
}

func sanitizeJPEGLogo(data []byte) ([]byte, int, int, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%w: %v", ErrCorruptLogoImage, err)
	}
	if err := validateLogoDimensions(cfg.Width, cfg.Height); err != nil {
		return nil, 0, 0, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%w: %v", ErrCorruptLogoImage, err)
	}
	b := img.Bounds()
	if err := validateLogoDimensions(b.Dx(), b.Dy()); err != nil {
		return nil, 0, 0, err
	}
	// Re-encoding strips all EXIF, GPS, IPTC, APPn, and comment segments cleanly.
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, 0, 0, fmt.Errorf("%w: %v", ErrCorruptLogoImage, err)
	}
	return out.Bytes(), b.Dx(), b.Dy(), nil
}

// sanitizeWebPLogo parses a RIFF/WEBP container, validates static VP8/VP8L/VP8X dimensions,
// rejects animated/malformed containers, and strips EXIF/XMP/ICCP metadata chunks.
func sanitizeWebPLogo(data []byte) ([]byte, int, int, error) {
	if len(data) < 20 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil, 0, 0, fmt.Errorf("%w: invalid WEBP RIFF header", ErrCorruptLogoImage)
	}
	riffSize := binary.LittleEndian.Uint32(data[4:8])
	if int64(riffSize)+8 != int64(len(data)) {
		return nil, 0, 0, fmt.Errorf("%w: inconsistent WEBP container size", ErrCorruptLogoImage)
	}

	var (
		body      bytes.Buffer
		width     int
		height    int
		haveDims  bool
		haveImage bool
	)

	pos := 12
	for pos < len(data) {
		if pos+8 > len(data) {
			return nil, 0, 0, fmt.Errorf("%w: truncated WEBP chunk header", ErrCorruptLogoImage)
		}
		chunkID := string(data[pos : pos+4])
		chunkLen := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		if chunkLen < 0 || pos+8+chunkLen > len(data) {
			return nil, 0, 0, fmt.Errorf("%w: invalid WEBP chunk length", ErrCorruptLogoImage)
		}
		payload := data[pos+8 : pos+8+chunkLen]
		paddedLen := chunkLen + (chunkLen & 1)
		if pos+8+paddedLen > len(data) {
			return nil, 0, 0, fmt.Errorf("%w: truncated WEBP chunk padding", ErrCorruptLogoImage)
		}

		switch chunkID {
		case "VP8X":
			if len(payload) != 10 {
				return nil, 0, 0, fmt.Errorf("%w: invalid VP8X chunk", ErrCorruptLogoImage)
			}
			// Reject animated WebP logos (ANIM bit 0x02).
			if payload[0]&0x02 != 0 {
				return nil, 0, 0, fmt.Errorf("%w: animated WebP logos are not permitted", ErrUnsupportedFileType)
			}
			w := 1 + int(payload[4]) | int(payload[5])<<8 | int(payload[6])<<16
			h := 1 + int(payload[7]) | int(payload[8])<<8 | int(payload[9])<<16
			if err := validateLogoDimensions(w, h); err != nil {
				return nil, 0, 0, err
			}
			width, height, haveDims = w, h, true
			// Clear ICCP (0x20), EXIF (0x08), XMP (0x04), ANIM (0x02) flags while keeping Alpha (0x10).
			var cleanedVP8X [10]byte
			copy(cleanedVP8X[:], payload)
			cleanedVP8X[0] = payload[0] & 0x10
			writeRIFFChunk(&body, "VP8X", cleanedVP8X[:])

		case "VP8 ":
			if len(payload) < 10 {
				return nil, 0, 0, fmt.Errorf("%w: invalid VP8 bitstream", ErrCorruptLogoImage)
			}
			if (payload[0]&0x01) != 0 || payload[3] != 0x9d || payload[4] != 0x01 || payload[5] != 0x2a {
				return nil, 0, 0, fmt.Errorf("%w: invalid VP8 keyframe signature", ErrCorruptLogoImage)
			}
			w := int(binary.LittleEndian.Uint16(payload[6:8]) & 0x3fff)
			h := int(binary.LittleEndian.Uint16(payload[8:10]) & 0x3fff)
			if err := validateLogoDimensions(w, h); err != nil {
				return nil, 0, 0, err
			}
			if !haveDims {
				width, height, haveDims = w, h, true
			}
			haveImage = true
			writeRIFFChunk(&body, chunkID, payload)

		case "VP8L":
			if len(payload) < 5 || payload[0] != 0x2f {
				return nil, 0, 0, fmt.Errorf("%w: invalid VP8L bitstream", ErrCorruptLogoImage)
			}
			bits := binary.LittleEndian.Uint32(payload[1:5])
			w := int(bits&0x3fff) + 1
			h := int((bits>>14)&0x3fff) + 1
			if err := validateLogoDimensions(w, h); err != nil {
				return nil, 0, 0, err
			}
			if !haveDims {
				width, height, haveDims = w, h, true
			}
			haveImage = true
			writeRIFFChunk(&body, chunkID, payload)

		case "ALPH":
			writeRIFFChunk(&body, chunkID, payload)

		case "ANIM", "ANMF":
			return nil, 0, 0, fmt.Errorf("%w: animated WebP logos are not permitted", ErrUnsupportedFileType)

		case "EXIF", "XMP ", "ICCP", "JUNK":
			// Strip metadata and padding chunks intentionally.

		default:
			// Drop any unrecognized ancillary chunk.
		}

		pos += 8 + paddedLen
	}

	if !haveDims || !haveImage {
		return nil, 0, 0, fmt.Errorf("%w: WebP container lacks image bitstream", ErrCorruptLogoImage)
	}

	var out bytes.Buffer
	out.WriteString("RIFF")
	var sz [4]byte
	binary.LittleEndian.PutUint32(sz[:], uint32(4+body.Len()))
	out.Write(sz[:])
	out.WriteString("WEBP")
	out.Write(body.Bytes())
	return out.Bytes(), width, height, nil
}

func writeRIFFChunk(dst *bytes.Buffer, id string, payload []byte) {
	dst.WriteString(id)
	var sz [4]byte
	binary.LittleEndian.PutUint32(sz[:], uint32(len(payload)))
	dst.Write(sz[:])
	dst.Write(payload)
	if len(payload)%2 == 1 {
		dst.WriteByte(0)
	}
}
