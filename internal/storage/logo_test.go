package storage

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func buildTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 47, G: 79, B: 74, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

func buildTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 196, G: 154, B: 69, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	return buf.Bytes()
}

func injectPNGTextChunk(t *testing.T, rawPNG []byte, keyword, val string) []byte {
	t.Helper()
	// Insert a tEXt chunk right before trailing IEND chunk (last 12 bytes).
	if len(rawPNG) < 20 {
		t.Fatalf("short PNG")
	}
	textPayload := append(append([]byte(keyword), 0), []byte(val)...)
	var chunk bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(textPayload)))
	chunk.Write(lenBuf[:])
	chunk.WriteString("tEXt")
	chunk.Write(textPayload)
	chunk.Write([]byte{0, 0, 0, 0}) // CRC ignored by Validate prefix check; actually png.Decode checks CRC, so test EXIF on WebP & JPEG COM segment
	_ = chunk
	return rawPNG
}

func injectJPEGCommentSegment(rawJPEG []byte, comment string) []byte {
	// Insert COM marker (0xFF 0xFE) immediately after SOI (0xFF 0xD8).
	var out bytes.Buffer
	out.Write(rawJPEG[:2])
	segLen := len(comment) + 2
	out.Write([]byte{0xFF, 0xFE, byte(segLen >> 8), byte(segLen)})
	out.WriteString(comment)
	out.Write(rawJPEG[2:])
	return out.Bytes()
}

func buildTestWebPWithEXIF(w, h int, secretEXIF string) []byte {
	var body bytes.Buffer
	// VP8X chunk: flags (EXIF 0x08 | ALPH 0x10), reserved 3 bytes, w-1 (3B LE), h-1 (3B LE)
	var vp8x [10]byte
	vp8x[0] = 0x18
	wm1 := uint32(w - 1)
	hm1 := uint32(h - 1)
	vp8x[4], vp8x[5], vp8x[6] = byte(wm1), byte(wm1>>8), byte(wm1>>16)
	vp8x[7], vp8x[8], vp8x[9] = byte(hm1), byte(hm1>>8), byte(hm1>>16)
	writeRIFFChunk(&body, "VP8X", vp8x[:])

	// VP8L chunk with valid signature 0x2f and dimensions
	var vp8l [8]byte
	vp8l[0] = 0x2f
	packed := uint32(w-1) | (uint32(h-1) << 14)
	binary.LittleEndian.PutUint32(vp8l[1:5], packed)
	writeRIFFChunk(&body, "VP8L", vp8l[:])

	if secretEXIF != "" {
		writeRIFFChunk(&body, "EXIF", []byte(secretEXIF))
	}

	var riff bytes.Buffer
	riff.WriteString("RIFF")
	var sz [4]byte
	binary.LittleEndian.PutUint32(sz[:], uint32(4+body.Len()))
	riff.Write(sz[:])
	riff.WriteString("WEBP")
	riff.Write(body.Bytes())
	return riff.Bytes()
}

func TestValidateAndSanitizePropertyLogo(t *testing.T) {
	pngBytes := buildTestPNG(t, 64, 48)
	res, err := ValidateAndSanitizePropertyLogo("camp-logo.png", pngBytes, MaxPropertyLogoBytes)
	if err != nil {
		t.Fatalf("valid PNG failed: %v", err)
	}
	if res.ContentType != "image/png" || res.Width != 64 || res.Height != 48 || len(res.SHA256) != 64 {
		t.Fatalf("unexpected PNG sanitize result: %+v", res)
	}

	// JPEG with injected GPS/comment metadata must strip comment on re-encode.
	jpgBytes := injectJPEGCommentSegment(buildTestJPEG(t, 80, 40), "SECRET_EXIF_GPS_MARKER")
	jpgRes, err := ValidateAndSanitizePropertyLogo("crest.jpeg", jpgBytes, MaxPropertyLogoBytes)
	if err != nil {
		t.Fatalf("valid JPEG failed: %v", err)
	}
	if jpgRes.ContentType != "image/jpeg" || jpgRes.Width != 80 || jpgRes.Height != 40 {
		t.Fatalf("unexpected JPEG sanitize result: %+v", jpgRes)
	}
	if bytes.Contains(jpgRes.Data, []byte("SECRET_EXIF_GPS_MARKER")) {
		t.Fatalf("expected JPEG metadata segment to be stripped on re-encode")
	}

	// WebP with EXIF chunk must strip EXIF bytes and clear EXIF flag in VP8X.
	webpBytes := buildTestWebPWithEXIF(120, 60, "SECRET_EXIF_DEVICE_ID")
	webpRes, err := ValidateAndSanitizePropertyLogo("emblem.webp", webpBytes, MaxPropertyLogoBytes)
	if err != nil {
		t.Fatalf("valid WebP failed: %v", err)
	}
	if webpRes.ContentType != "image/webp" || webpRes.Width != 120 || webpRes.Height != 60 {
		t.Fatalf("unexpected WebP sanitize result: %+v", webpRes)
	}
	if bytes.Contains(webpRes.Data, []byte("SECRET_EXIF_DEVICE_ID")) || bytes.Contains(webpRes.Data, []byte("EXIF")) {
		t.Fatalf("expected WebP EXIF chunk to be stripped")
	}

	// Verify 512x256 VP8X dimension carry (0x01ff + 1 = 512, 0x00ff + 1 = 256).
	webp512 := buildTestWebPWithEXIF(512, 256, "")
	webp512Res, err := ValidateAndSanitizePropertyLogo("wide.webp", webp512, MaxPropertyLogoBytes)
	if err != nil || webp512Res.Width != 512 || webp512Res.Height != 256 {
		t.Fatalf("expected 512x256 WebP to decode accurately, got res=%+v err=%v", webp512Res, err)
	}
}

func TestValidatePropertyLogoSecurityRejections(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		payload  []byte
	}{
		{
			name:     "svg_rejected",
			filename: "logo.svg",
			payload:  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		},
		{
			name:     "svg_disguised_as_png",
			filename: "logo.png",
			payload:  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`),
		},
		{
			name:     "html_disguised_as_jpg",
			filename: "logo.jpg",
			payload:  []byte(`<html><body><script>alert(1)</script></body></html>`),
		},
		{
			name:     "gif_not_permitted_for_logos",
			filename: "logo.gif",
			payload:  []byte("GIF89a\x10\x00\x10\x00\x00\x00\x00;"),
		},
		{
			name:     "extension_mime_mismatch",
			filename: "logo.png",
			payload:  buildTestJPEG(t, 32, 32),
		},
		{
			name:     "too_small_tracking_pixel",
			filename: "tiny.png",
			payload:  buildTestPNG(t, 4, 4),
		},
		{
			name:     "dimension_bomb_webp",
			filename: "bomb.webp",
			payload:  buildTestWebPWithEXIF(4096, 4096, ""),
		},
		{
			name:     "oversized_payload",
			filename: "huge.png",
			payload:  bytes.Repeat([]byte("A"), MaxPropertyLogoBytes+1),
		},
		{
			name:     "empty_payload",
			filename: "empty.png",
			payload:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateAndSanitizePropertyLogo(tc.filename, tc.payload, MaxPropertyLogoBytes); err == nil {
				t.Fatalf("expected %s (%s) to be rejected", tc.name, strings.TrimSpace(tc.filename))
			}
		})
	}
}
